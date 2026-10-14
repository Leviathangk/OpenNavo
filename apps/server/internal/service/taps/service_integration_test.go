//go:build integration

package taps_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/jobs"
	"github.com/opennavo/opennavo/server/internal/service/catalog"
	"github.com/opennavo/opennavo/server/internal/service/management"
	"github.com/opennavo/opennavo/server/internal/service/snapshot"
	"github.com/opennavo/opennavo/server/internal/service/taps"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

// fakeGitHub serves one tap repository: the newest commit of each cask path and file contents per commit.
type fakeGitHub struct {
	mu      sync.Mutex
	head    map[string]string // path -> commit sha ("" = no history)
	dates   map[string]time.Time
	files   map[string]string // sha + "/" + path -> contents
	calls   []string
	limited bool
}

func newFakeGitHub() *fakeGitHub {
	return &fakeGitHub{head: map[string]string{}, dates: map[string]time.Time{}, files: map[string]string{}}
}

func (f *fakeGitHub) commit(path, sha, contents string, date time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.head[path], f.dates[sha] = sha, date
	if contents != "" {
		f.files[sha+"/"+path] = contents
	}
}

func (f *fakeGitHub) Get(_ context.Context, target string, _ int64, headers http.Header) ([]byte, int, http.Header, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, target)
	if f.limited {
		return nil, http.StatusForbidden, http.Header{}, nil
	}
	parsed, err := url.Parse(target)
	if err != nil {
		return nil, 0, nil, err
	}
	switch parsed.Host {
	case "api.github.com":
		if parsed.Path != "/repos/swiftvm-app/homebrew-tap/commits" {
			return nil, http.StatusNotFound, http.Header{}, nil
		}
		path := parsed.Query().Get("path")
		sha := f.head[path]
		etag := `"` + sha + `"`
		if sha != "" && headers.Get("If-None-Match") == etag {
			return nil, http.StatusNotModified, http.Header{"Etag": {etag}}, nil
		}
		if sha == "" {
			return []byte(`[]`), http.StatusOK, http.Header{"Etag": {`"empty"`}}, nil
		}
		body, _ := json.Marshal([]any{map[string]any{"sha": sha, "commit": map[string]any{"committer": map[string]any{"date": f.dates[sha]}}}})
		return body, http.StatusOK, http.Header{"Etag": {etag}}, nil
	case "raw.githubusercontent.com":
		key := strings.TrimPrefix(parsed.Path, "/swiftvm-app/homebrew-tap/")
		if contents, ok := f.files[key]; ok {
			return []byte(contents), http.StatusOK, http.Header{}, nil
		}
		return nil, http.StatusNotFound, http.Header{}, nil
	}
	return nil, http.StatusNotFound, http.Header{}, nil
}

func sha(n byte) string { return strings.Repeat(string('a'+n), 40) }

func swiftvm(version string, extra string) string {
	return `cask "swiftvm" do
  version "` + version + `"
  sha256 "` + strings.Repeat("0", 64) + `"

  url "https://github.com/swiftvm-app/swiftvm/releases/download/v#{version}/SwiftVM-#{version}.dmg"
  name "SwiftVM"
  desc "Run macOS and Linux virtual machines"
  homepage "https://github.com/swiftvm-app/swiftvm"
` + extra + `
  livecheck do
    url :url
    strategy :github_latest
  end

  depends_on arch: :arm64

  app "SwiftVM.app"
end
`
}

type officialSource struct{ casks []string }

func (s officialSource) Stream(_ context.Context, _ string, _ homebrew.Conditional, consume func(homebrew.Package) error) (homebrew.StreamResult, error) {
	for _, raw := range s.casks {
		item, err := homebrew.Normalize("cask", json.RawMessage(raw))
		if err != nil {
			return homebrew.StreamResult{}, err
		}
		if err := consume(item); err != nil {
			return homebrew.StreamResult{}, err
		}
	}
	return homebrew.StreamResult{Fetched: len(s.casks), Conditional: homebrew.Conditional{ETag: `"feed"`}}, nil
}

type queue struct{ events []string }

func (q *queue) Enqueue(_ context.Context, taskType string, payload jobs.Payload, _ jobs.Metadata) (string, error) {
	q.events = append(q.events, taskType+":"+strings.TrimSpace(string(must(json.Marshal(payload)))))
	return taskType, nil
}
func (q *queue) Trigger(context.Context, string, int64) (string, error) { return "", nil }

func must[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}

func appCode(t *testing.T, err error) string {
	t.Helper()
	var appErr *domain.AppError
	require.True(t, errors.As(err, &appErr), "error %v", err)
	return appErr.Code
}

func TestTapCaskLifecycle(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	github := newFakeGitHub()
	start := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	github.commit("Casks/swiftvm.rb", sha(0), swiftvm("1.2.0", ""), start)
	tapService := &taps.Service{Store: st, GitHub: github}
	jobsQueue := &queue{}
	admin := &management.Service{Store: st, Taps: tapService, Queue: jobsQueue}
	official := `{"token":"official-app","tap":"homebrew/cask","version":"1.0","name":["Official App"],"url":"https://example.com/official.dmg","artifacts":[{"app":["Official.app"]}]}`
	sync := &catalog.Service{Store: st, Source: officialSource{casks: []string{official}}, Queue: jobsQueue}
	_, err := sync.Run(ctx)
	require.NoError(t, err)

	// A dry run evaluates the cask without writing anything.
	dryRun := map[string]any{"dryrun": true}
	preview, err := admin.Execute(ctx, "AddTapPackage", management.Input{Params: dryRun, Body: map[string]any{"cask": "swiftvm-app/tap/swiftvm"}})
	require.NoError(t, err)
	encoded, _ := json.Marshal(preview)
	require.Contains(t, string(encoded), `"fullToken":"swiftvm-app/tap/swiftvm"`)
	require.Contains(t, string(encoded), `"version":"1.2.0"`)
	var count int64
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM packages WHERE token='swiftvm'").Scan(&count).Error)
	require.Zero(t, count)

	added, err := admin.Execute(ctx, "AddTapPackage", management.Input{Body: map[string]any{"cask": "SwiftVM-App/homebrew-tap/swiftvm"}})
	require.NoError(t, err)
	view := added.(map[string]any)
	id := view["id"].(int64)
	require.Equal(t, "swiftvm-app/tap/swiftvm", view["fullToken"])
	require.Equal(t, "https://github.com/swiftvm-app/homebrew-tap/blob/HEAD/Casks/swiftvm.rb", view["sourceUrl"])
	var row struct {
		Tap, FullToken, Version string
		SupportsArm64           bool `gorm:"column:supports_arm64"`
		SupportsX8664           bool `gorm:"column:supports_x86_64"`
		RemovedAt               *time.Time
	}
	read := func() {
		t.Helper()
		require.NoError(t, st.DB.Raw("SELECT tap,full_token,version,supports_arm64,supports_x86_64,removed_at FROM packages WHERE id=?", id).Scan(&row).Error)
	}
	read()
	require.Equal(t, "swiftvm-app/tap", row.Tap)
	require.Equal(t, "swiftvm-app/tap/swiftvm", row.FullToken)
	require.Equal(t, "1.2.0", row.Version)
	require.True(t, row.SupportsArm64)
	require.False(t, row.SupportsX8664)
	var committed time.Time
	require.NoError(t, st.DB.Raw("SELECT brew_committed_at FROM package_versions WHERE package_id=? AND version='1.2.0'", id).Scan(&committed).Error)
	require.WithinDuration(t, start, committed, 0)

	_, err = admin.Execute(ctx, "AddTapPackage", management.Input{Body: map[string]any{"cask": "swiftvm-app/tap/swiftvm"}})
	require.Equal(t, domain.CodeConflict, appCode(t, err), "a cask is added once")
	github.commit("Casks/official-app.rb", sha(9), strings.ReplaceAll(swiftvm("1.0", ""), `cask "swiftvm"`, `cask "official-app"`), start)
	_, err = admin.Execute(ctx, "AddTapPackage", management.Input{Body: map[string]any{"cask": "swiftvm-app/tap/official-app"}})
	require.Equal(t, domain.CodeConflict, appCode(t, err), "official tokens stay with Homebrew")
	_, err = admin.Execute(ctx, "AddTapPackage", management.Input{Body: map[string]any{"cask": "swiftvm-app/tap/missing"}})
	require.Equal(t, domain.CodeValidation, appCode(t, err))
	_, err = admin.Execute(ctx, "AddTapPackage", management.Input{Body: map[string]any{"cask": "homebrew/cask/firefox"}})
	require.Equal(t, domain.CodeValidation, appCode(t, err))

	listed, err := admin.Execute(ctx, "ListTapPackages", management.Input{})
	require.NoError(t, err)
	records := listed.(map[string]any)["records"].([]map[string]any)
	require.Len(t, records, 1)
	require.Equal(t, sha(0), records[0]["headSha"])
	require.Nil(t, records[0]["lastError"])

	// The official sync neither removes nor renames the tap cask.
	_, err = sync.Run(ctx)
	require.NoError(t, err)
	read()
	require.Nil(t, row.RemovedAt)
	require.Equal(t, "swiftvm-app/tap", row.Tap)

	// Clients see the tap on the catalog item; official items carry none.
	changes, err := (&snapshot.Service{Store: st}).Changes(ctx, 0, 100)
	require.NoError(t, err)
	itemTaps := map[string]*string{}
	for _, change := range changes.Changes {
		if change.Item != nil {
			itemTaps[change.Token] = change.Item.Tap
		}
	}
	require.NotNil(t, itemTaps["swiftvm"])
	require.Equal(t, "swiftvm-app/tap", *itemTaps["swiftvm"])
	require.Nil(t, itemTaps["official-app"])
	detail, err := (&catalog.PublicService{Store: st}).Detail(ctx, "cask", "swiftvm", "en-US")
	require.NoError(t, err)
	require.Equal(t, "brew install --cask swiftvm-app/tap/swiftvm", detail.InstallCommand)

	// An unchanged tap is a conditional request.
	stats, err := tapService.Run(ctx, 0)
	require.NoError(t, err)
	require.Equal(t, 1, stats["unchanged"])

	// A new commit with a new version updates the catalog and dates the version.
	updated := start.Add(48 * time.Hour)
	github.commit("Casks/swiftvm.rb", sha(1), swiftvm("1.3.0", ""), updated)
	stats, err = tapService.Run(ctx, 0)
	require.NoError(t, err)
	require.Equal(t, 1, stats["updated"])
	read()
	require.Equal(t, "1.3.0", row.Version)
	require.NoError(t, st.DB.Raw("SELECT brew_committed_at FROM package_versions WHERE package_id=? AND version='1.3.0'", id).Scan(&committed).Error)
	require.WithinDuration(t, updated, committed, 0)

	// DSL the reader cannot evaluate keeps the last good version and reports the line.
	github.commit("Casks/swiftvm.rb", sha(2), swiftvm("1.4.0", `  arch arm: "arm64", intel: "x64"`), updated.Add(time.Hour))
	stats, err = tapService.Run(ctx, 0)
	require.NoError(t, err)
	require.Equal(t, 1, stats["failed"])
	read()
	require.Equal(t, "1.3.0", row.Version)
	cask, err := st.TapCask(ctx, id)
	require.NoError(t, err)
	require.Contains(t, cask.LastError, "line 9: `arch`")

	// Rate limits are recorded without touching the catalog.
	github.limited = true
	stats, err = tapService.Run(ctx, id)
	require.NoError(t, err)
	require.Equal(t, 1, stats["failed"])
	github.limited = false

	// Deleting the file from the tap removes the package; restoring it brings it back.
	github.commit("Casks/swiftvm.rb", sha(3), "", updated.Add(2*time.Hour))
	stats, err = tapService.Run(ctx, 0)
	require.NoError(t, err)
	require.Equal(t, 1, stats["removed"])
	read()
	require.NotNil(t, row.RemovedAt)
	github.commit("Casks/swiftvm.rb", sha(4), swiftvm("1.5.0", ""), updated.Add(3*time.Hour))
	stats, err = tapService.Run(ctx, 0)
	require.NoError(t, err)
	require.Equal(t, 1, stats["updated"])
	read()
	require.Nil(t, row.RemovedAt)
	require.Equal(t, "1.5.0", row.Version)
	cask, err = st.TapCask(ctx, id)
	require.NoError(t, err)
	require.Empty(t, cask.LastError)

	// package_refresh reads one tap cask through the tap sync.
	_, err = admin.Execute(ctx, "ResyncPackage", management.Input{ID: id})
	require.NoError(t, err)
	require.Contains(t, jobsQueue.events, `taps:sync:{"packageId":`+jsonNumber(id)+`}`)

	// Removing the tap cask stops tracking it and removes it from the catalog.
	_, err = admin.Execute(ctx, "RemoveTapPackage", management.Input{ID: id, Params: dryRun})
	require.NoError(t, err)
	read()
	require.Nil(t, row.RemovedAt, "a dry run removes nothing")
	_, err = admin.Execute(ctx, "RemoveTapPackage", management.Input{ID: id})
	require.NoError(t, err)
	read()
	require.NotNil(t, row.RemovedAt)
	cask, err = st.TapCask(ctx, id)
	require.NoError(t, err)
	require.Nil(t, cask)
	_, err = admin.Execute(ctx, "RemoveTapPackage", management.Input{ID: id})
	require.Equal(t, domain.CodeNotFound, appCode(t, err))

	// It can be added again later; the removed row is revived.
	added, err = admin.Execute(ctx, "AddTapPackage", management.Input{Body: map[string]any{"cask": "swiftvm-app/tap/swiftvm"}})
	require.NoError(t, err)
	require.Equal(t, id, added.(map[string]any)["id"])
	read()
	require.Nil(t, row.RemovedAt)
}

func jsonNumber(value int64) string { return string(must(json.Marshal(value))) }

func TestOfficialCaskTakesOverTapToken(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	github := newFakeGitHub()
	github.commit("Casks/swiftvm.rb", sha(0), swiftvm("1.2.0", ""), time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC))
	tapService := &taps.Service{Store: st, GitHub: github}
	admin := &management.Service{Store: st, Taps: tapService, Queue: &queue{}}
	_, err := admin.Execute(ctx, "AddTapPackage", management.Input{Body: map[string]any{"cask": "swiftvm-app/tap/swiftvm"}})
	require.NoError(t, err)
	// Homebrew later adds the same token: the official sync owns the row from then on and the tap sync backs off.
	official := `{"token":"swiftvm","tap":"homebrew/cask","version":"2.0","name":["SwiftVM"],"url":"https://example.com/swiftvm.dmg","artifacts":[{"app":["SwiftVM.app"]}]}`
	sync := &catalog.Service{Store: st, Source: officialSource{casks: []string{official}}, Queue: &queue{}}
	_, err = sync.Run(ctx)
	require.NoError(t, err)
	_, err = sync.Run(ctx)
	require.NoError(t, err)
	var row struct{ Tap, Version string }
	require.NoError(t, st.DB.Raw("SELECT tap,version FROM packages WHERE token='swiftvm'").Scan(&row).Error)
	require.Equal(t, "homebrew/cask", row.Tap)
	require.Equal(t, "2.0", row.Version)
	stats, err := tapService.Run(ctx, 0)
	require.NoError(t, err)
	require.Equal(t, 1, stats["failed"])
	require.NoError(t, st.DB.Raw("SELECT tap,version FROM packages WHERE token='swiftvm'").Scan(&row).Error)
	require.Equal(t, "homebrew/cask", row.Tap, "the tap sync never overwrites an official cask")
	var changes int64
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM catalog_changes WHERE token='swiftvm' AND event_type='created'").Scan(&changes).Error)
	require.Equal(t, int64(2), changes, "the tap add and the official takeover; later official syncs see an unchanged row")
	tracked, err := st.TapCasks(ctx)
	require.NoError(t, err)
	require.Len(t, tracked, 1)
	require.NoError(t, st.UnregisterTapCask(ctx, tracked[0].PackageID))
	var active int64
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM packages WHERE token='swiftvm' AND tap='homebrew/cask' AND removed_at IS NULL").Scan(&active).Error)
	require.Equal(t, int64(1), active)
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM catalog_changes WHERE token='swiftvm' AND op='delete'").Scan(&changes).Error)
	require.Zero(t, changes)

}

// The callback runs after the HTTP response was prepared, while the sync holds no catalog lock.
type interceptGitHub struct {
	base  *fakeGitHub
	after func()
}

func (f *interceptGitHub) Get(ctx context.Context, target string, maximum int64, headers http.Header) ([]byte, int, http.Header, error) {
	body, status, response, err := f.base.Get(ctx, target, maximum, headers)
	if strings.Contains(target, "raw.githubusercontent.com") && f.after != nil {
		fn := f.after
		f.after = nil
		fn()
	}
	return body, status, response, err
}

func TestTapSyncDoesNotReviveUnregisteredCask(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		t.Run(map[bool]string{false: "update", true: "delete"}[deleted], func(t *testing.T) {
			ctx := context.Background()
			st := testutil.NewStore(t)
			github := newFakeGitHub()
			now := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
			github.commit("Casks/swiftvm.rb", sha(0), swiftvm("1", ""), now)
			client := &interceptGitHub{base: github}
			service := &taps.Service{Store: st, GitHub: client}
			admin := &management.Service{Store: st, Taps: service, Queue: &queue{}}
			added, err := admin.Execute(ctx, "AddTapPackage", management.Input{Body: map[string]any{"cask": "swiftvm-app/tap/swiftvm"}})
			require.NoError(t, err)
			id := added.(map[string]any)["id"].(int64)
			content := swiftvm("2", "")
			if deleted {
				content = ""
			}
			github.commit("Casks/swiftvm.rb", sha(1), content, now.Add(time.Hour))
			client.after = func() { require.NoError(t, st.UnregisterTapCask(ctx, id)) }
			stats, err := service.Run(ctx, id)
			require.NoError(t, err)
			require.Equal(t, 0, stats["updated"])
			var row struct {
				Version   string
				RemovedAt *time.Time
			}
			require.NoError(t, st.DB.Raw("SELECT version,removed_at FROM packages WHERE id=?", id).Scan(&row).Error)
			require.Equal(t, "1", row.Version)
			require.NotNil(t, row.RemovedAt)
			tracked, err := st.TapCask(ctx, id)
			require.NoError(t, err)
			require.Nil(t, tracked)
			var removals int64
			require.NoError(t, st.DB.Raw("SELECT count(*) FROM catalog_changes WHERE package_id=? AND op='delete'", id).Scan(&removals).Error)
			require.Equal(t, int64(1), removals)
		})
	}
}

func TestTapScheduledDisableWithoutCommitChange(t *testing.T) {
	for _, conditional := range []bool{true, false} {
		t.Run(map[bool]string{true: "304", false: "same-sha"}[conditional], func(t *testing.T) {
			ctx := context.Background()
			st := testutil.NewStore(t)
			github := newFakeGitHub()
			now := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
			github.commit("Casks/swiftvm.rb", sha(0), swiftvm("1", `  disable! date: "2026-10-02", because: :discontinued`), now)
			service := &taps.Service{Store: st, GitHub: github, Now: func() time.Time { return now }}
			admin := &management.Service{Store: st, Taps: service, Queue: &queue{}}
			added, err := admin.Execute(ctx, "AddTapPackage", management.Input{Body: map[string]any{"cask": "swiftvm-app/tap/swiftvm"}})
			require.NoError(t, err)
			id := added.(map[string]any)["id"].(int64)
			var disabled bool
			require.NoError(t, st.DB.Raw("SELECT disabled FROM packages WHERE id=?", id).Scan(&disabled).Error)
			require.False(t, disabled)
			now = now.Add(24 * time.Hour)
			if !conditional {
				require.NoError(t, st.DB.Exec("UPDATE tap_casks SET etag=NULL WHERE package_id=?", id).Error)
			}
			stats, err := service.Run(ctx, id)
			require.NoError(t, err)
			require.Equal(t, 1, stats["updated"])
			require.NoError(t, st.DB.Raw("SELECT disabled FROM packages WHERE id=?", id).Scan(&disabled).Error)
			require.True(t, disabled)
			stats, err = service.Run(ctx, id)
			require.NoError(t, err)
			require.Equal(t, 1, stats["unchanged"])
		})
	}
}

func TestFailedTapSyncPreservesNewRegistration(t *testing.T) {
	for _, path := range []string{"Casks/swiftvm.rb", "Casks/s/swiftvm.rb"} {
		t.Run(path, func(t *testing.T) {
			ctx := context.Background()
			st := testutil.NewStore(t)
			github := newFakeGitHub()
			now := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
			github.commit("Casks/swiftvm.rb", sha(0), swiftvm("1", ""), now)
			client := &interceptGitHub{base: github}
			service := &taps.Service{Store: st, GitHub: client}
			admin := &management.Service{Store: st, Taps: service, Queue: &queue{}}
			add := func() int64 {
				added, err := admin.Execute(ctx, "AddTapPackage", management.Input{Body: map[string]any{"cask": "swiftvm-app/tap/swiftvm"}})
				require.NoError(t, err)
				return added.(map[string]any)["id"].(int64)
			}
			id := add()
			github.commit("Casks/swiftvm.rb", sha(1), swiftvm("2", `  arch arm: "arm64", intel: "x64"`), now)
			client.after = func() {
				require.NoError(t, st.UnregisterTapCask(ctx, id))
				github.commit("Casks/swiftvm.rb", sha(2), "", now)
				github.commit(path, sha(3), swiftvm("3", ""), now)
				require.Equal(t, id, add())
			}
			stats, err := service.Run(ctx, id)
			require.NoError(t, err)
			require.Equal(t, 1, stats["failed"])
			current, err := st.TapCask(ctx, id)
			require.NoError(t, err)
			require.NotNil(t, current)
			require.Equal(t, path, current.SourcePath)
			require.Equal(t, sha(3), current.HeadSHA)
			require.Equal(t, `"`+sha(3)+`"`, current.ETag)
			require.Empty(t, current.LastError)
			stats, err = service.Run(ctx, id)
			require.NoError(t, err)
			require.Equal(t, 1, stats["unchanged"])
			current, err = st.TapCask(ctx, id)
			require.NoError(t, err)
			require.Nil(t, current.RemovedAt)
			require.Equal(t, "3", current.Version)
		})
	}
}

func TestTapSyncFollowsCaskMoves(t *testing.T) {
	for _, missingHistory := range []bool{false, true} {
		t.Run(map[bool]string{false: "deleted-old-path", true: "missing-old-history"}[missingHistory], func(t *testing.T) {
			ctx := context.Background()
			st := testutil.NewStore(t)
			github := newFakeGitHub()
			now := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
			flat, sharded := "Casks/swiftvm.rb", "Casks/s/swiftvm.rb"
			github.commit(flat, sha(0), swiftvm("1", ""), now)
			service := &taps.Service{Store: st, GitHub: github}
			admin := &management.Service{Store: st, Taps: service, Queue: &queue{}}
			added, err := admin.Execute(ctx, "AddTapPackage", management.Input{Body: map[string]any{"cask": "swiftvm-app/tap/swiftvm"}})
			require.NoError(t, err)
			id := added.(map[string]any)["id"].(int64)
			for i, paths := range [][2]string{{flat, sharded}, {sharded, flat}} {
				oldSHA := sha(byte(1 + i*2))
				if missingHistory {
					oldSHA = ""
				}
				github.commit(paths[0], oldSHA, "", now)
				newSHA := sha(byte(2 + i*2))
				github.commit(paths[1], newSHA, swiftvm("1", ""), now)
				stats, err := service.Run(ctx, id)
				require.NoError(t, err)
				require.Equal(t, 0, stats["removed"])
				current, err := st.TapCask(ctx, id)
				require.NoError(t, err)
				require.Equal(t, paths[1], current.SourcePath)
				require.Equal(t, newSHA, current.HeadSHA)
				require.Nil(t, current.RemovedAt)
				stats, err = service.Run(ctx, id)
				require.NoError(t, err)
				require.Equal(t, 1, stats["unchanged"])
			}
			var removals int64
			require.NoError(t, st.DB.Raw("SELECT count(*) FROM catalog_changes WHERE package_id=? AND op='delete'", id).Scan(&removals).Error)
			require.Zero(t, removals)
		})
	}
}
