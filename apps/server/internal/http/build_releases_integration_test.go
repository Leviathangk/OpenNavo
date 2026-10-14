//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	httpserver "github.com/opennavo/opennavo/server/internal/http"
	"github.com/opennavo/opennavo/server/internal/http/public"
	"github.com/opennavo/opennavo/server/internal/service/catalog"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

// Casks such as Amore keep 0.1 while every build ships new notes: a build with notes of its own is
// its own timeline entry, and the other builds share the entry of their base version.
func TestPublicReleaseTimelineListsBuildsWithTheirOwnNotes(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	// builds records a cask whose current build is the first version, then older builds; it returns build IDs.
	builds := func(token string, versions map[string]string, current string) (int64, map[string]int64) {
		t.Helper()
		p, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"`+token+`","version":"`+current+`"}`))
		require.NoError(t, err)
		require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p}}))
		var pid int64
		require.NoError(t, st.DB.Raw("SELECT id FROM packages WHERE token=?", token).Scan(&pid).Error)
		ids := map[string]int64{}
		for version, committed := range versions {
			base, _, _ := strings.Cut(version, ",")
			require.NoError(t, st.DB.Exec(`INSERT INTO package_versions(package_id,version,version_base,brew_committed_at) VALUES(?,?,?,?) ON CONFLICT(package_id,version) DO UPDATE SET brew_committed_at=EXCLUDED.brew_committed_at`, pid, version, base, committed).Error)
			var id int64
			require.NoError(t, st.DB.Raw("SELECT id FROM package_versions WHERE package_id=? AND version=?", pid, version).Scan(&id).Error)
			ids[version] = id
		}
		return pid, ids
	}
	note := func(pid int64, version, body, published string, build int64) {
		t.Helper()
		var owner any
		key := version
		if build > 0 {
			owner = build
			require.NoError(t, st.DB.Raw("SELECT version FROM package_versions WHERE id=?", build).Scan(&key).Error)
		}
		require.NoError(t, st.DB.Exec(`INSERT INTO releases(package_id,source,source_key,version,body_markdown,published_at,package_version_id) VALUES(?,'editorial',?,?,?,?,?)`, pid, "editorial:"+key, version, body, published, owner).Error)
	}
	amore, amoreBuilds := builds("amore", map[string]string{"0.1,49": "2026-10-13T09:47:05Z", "0.1,48": "2026-10-02T15:40:35Z", "0.1,47": "2026-08-07T00:43:22Z", "0.1,36": "2026-05-23T07:21:22Z"}, "0.1,49")
	note(amore, "0.1", "Shared notes", "2026-07-11T03:47:32Z", 0)
	note(amore, "0.1", "Build 49 notes", "2026-10-13T09:23:27Z", amoreBuilds["0.1,49"])
	note(amore, "0.1", "Build 47 notes", "2026-08-07T00:43:22Z", amoreBuilds["0.1,47"])
	plain, _ := builds("plain", map[string]string{"2.0,7": "2026-10-01T00:00:00Z", "2.0,6": "2026-09-20T00:00:00Z"}, "2.0,7")
	note(plain, "2.0", "Plain notes", "2026-09-20T00:00:00Z", 0)
	stash, stashBuilds := builds("stash", map[string]string{"4.4.0,563": "2026-10-13T22:33:46Z", "4.4.0,558": "2026-10-10T07:41:05Z"}, "4.4.0,563")
	note(stash, "4.4.0", "Shared notes", "2026-10-10T06:38:52Z", 0)
	note(stash, "4.4.0", "Build 558 notes", "2026-10-10T06:38:52Z", stashBuilds["4.4.0,558"])
	every, everyBuilds := builds("every", map[string]string{"1.0,2": "2026-09-10T00:00:00Z", "1.0,1": "2026-09-01T00:00:00Z"}, "1.0,2")
	note(every, "1.0", "Shared notes", "2026-08-30T00:00:00Z", 0)
	note(every, "1.0", "Build 1 notes", "2026-09-01T00:00:00Z", everyBuilds["1.0,1"])
	note(every, "1.0", "Build 2 notes", "2026-09-10T00:00:00Z", everyBuilds["1.0,2"])

	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	svc := &catalog.PublicService{Store: st, Cache: cache.Redis{Client: st.Redis}, Now: func() time.Time { return now }}
	server, err := httpserver.New(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), st, httpserver.Dependencies{Public: &public.Handler{Catalog: svc}})
	require.NoError(t, err)
	spec, err := publicapi.GetSpec()
	require.NoError(t, err)
	call := func(method, path, body string) []byte {
		t.Helper()
		r := httptest.NewRequest(method, "/api/v1"+path, strings.NewReader(body))
		r.Header.Set("Accept-Language", "en-US")
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		server.Engine.ServeHTTP(w, r)
		testutil.ValidateResponse(t, spec, "/api/v1", r, w)
		require.Equal(t, 200, w.Code, w.Body.String())
		return w.Body.Bytes()
	}
	timeline := func(token string) publicapi.ReleasePageResponse {
		t.Helper()
		var page publicapi.ReleasePageResponse
		require.NoError(t, json.Unmarshal(call("GET", "/packages/cask/"+token+"/releases?size=100", ""), &page))
		return page
	}
	type row struct{ Version, Base, Build, Body, Committed string }
	rows := func(page publicapi.ReleasePageResponse) []row {
		out := []row{}
		for _, entry := range page.Data.Records {
			r := row{Version: entry.Version, Base: *entry.VersionBase}
			if entry.Build != nil {
				r.Build = *entry.Build
			}
			if entry.BodyMarkdown != nil {
				r.Body = *entry.BodyMarkdown
			}
			if entry.BrewCommittedAt != nil {
				r.Committed = entry.BrewCommittedAt.UTC().Format("2006-01-02")
			}
			out = append(out, r)
		}
		return out
	}

	page := timeline("amore")
	require.Equal(t, []row{
		{"0.1,49", "0.1", "49", "Build 49 notes", "2026-10-13"},
		{"0.1", "0.1", "", "Shared notes", "2026-05-23"},
		{"0.1,47", "0.1", "47", "Build 47 notes", "2026-08-07"},
	}, rows(page), "builds without notes of their own (36, 48) share the 0.1 entry, dated by the first of them")
	require.True(t, page.Data.Records[0].IsLatest)
	require.Equal(t, 1, page.Data.Stats.Count30d, "build 49 counts as a release")
	var detail struct {
		Data struct {
			LatestRelease publicapi.ReleaseEntry `json:"latestRelease"`
			ReleaseCount  int                    `json:"releaseCount"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(call("GET", "/packages/cask/amore", ""), &detail))
	require.Equal(t, "0.1,49", detail.Data.LatestRelease.Version)
	require.Equal(t, "49", *detail.Data.LatestRelease.Build)
	require.Equal(t, 3, detail.Data.ReleaseCount)
	var lookup struct {
		Data []struct {
			LatestRelease struct{ Version string } `json:"latestRelease"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(call("POST", "/packages/lookup", `{"items":[{"kind":"cask","token":"amore"}]}`), &lookup))
	require.Len(t, lookup.Data, 1)
	require.Equal(t, "0.1,49", lookup.Data[0].LatestRelease.Version, "the updates list shows the newest build's notes")

	require.Equal(t, []row{{"2.0", "2.0", "", "Plain notes", "2026-09-20"}}, rows(timeline("plain")), "without build notes builds keep sharing one entry")
	page = timeline("stash")
	require.Equal(t, []row{
		{"4.4.0", "4.4.0", "", "Shared notes", "2026-10-13"},
		{"4.4.0,558", "4.4.0", "558", "Build 558 notes", "2026-10-10"},
	}, rows(page), "the current build 563 has no notes of its own, so the shared entry it belongs to comes first")
	require.True(t, page.Data.Records[0].IsLatest)
	require.Equal(t, []row{
		{"1.0,2", "1.0", "2", "Build 2 notes", "2026-09-10"},
		{"1.0,1", "1.0", "1", "Build 1 notes", "2026-09-01"},
		{"1.0", "1.0", "", "Shared notes", "2026-09-01"},
	}, rows(timeline("every")), "a shared note no build shows any more comes after the builds")
}
