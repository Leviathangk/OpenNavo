//go:build integration

package urlnotify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/seo"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

type contentGateTransport func(*http.Request) (*http.Response, error)

func (f contentGateTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type contentGateFixture struct {
	t                   *testing.T
	st                  *store.Store
	n                   *Notifier
	now                 time.Time
	id                  int64
	probes, invalidates int
	submitted           []string
	status              int
	logs                bytes.Buffer
	onProbe, onSubmit   func()
}

func newContentGateFixture(t *testing.T, st *store.Store) *contentGateFixture {
	t.Helper()
	f := &contentGateFixture{t: t, st: st, now: time.Now().UTC().Truncate(time.Microsecond).Add(time.Minute), status: 200}
	f.exec(`INSERT INTO packages(kind,token,full_token,tap,name,version,version_base,raw,raw_hash,source_locale,first_seen_at,desc_en)
 VALUES('cask','gate-app','gate-app','homebrew/cask','Gate App','3','3','{}','fixture','en-US','2020-01-01','Useful editor')`)
	require.NoError(t, st.DB.Raw("SELECT id FROM packages WHERE token='gate-app'").Scan(&f.id).Error)
	f.exec(`INSERT INTO package_meta(package_id,index_override) VALUES(?,'index')`, f.id)
	f.exec(`INSERT INTO package_i18n(package_id,locale,display_name,summary,description,status,source,source_locale,source_hash,updated_at)
 VALUES(?,'en-US','Gate App','An editor','A useful editor','source','human','en-US',repeat('a',64),'2020-01-01')`, f.id)
	for _, version := range []string{"1", "2", "3"} {
		f.exec(`INSERT INTO releases(package_id,source,source_key,version,body_markdown,source_url,source_locale)
 VALUES(?,'manual',?,?,?,'https://vendor.example/notes','en-US')`, f.id, version, version, strings.Repeat("Fixed a crash when opening large files. ", 12))
	}
	f.refresh()
	f.exec("UPDATE package_page_state SET content_modified_at=? WHERE package_id=?", f.now.Add(-2*time.Hour), f.id)
	settings := store.DefaultSEOSettings()
	settings.IndexNow.Enabled = true
	document, err := json.Marshal(settings)
	require.NoError(t, err)
	f.exec("UPDATE seo_settings SET settings=?::jsonb", string(document))
	cfg := config.Config{IndexNowContentGate: true, JWTSecret: strings.Repeat("a", 32), WebBaseURL: "https://site.example", WebInternalURL: "https://site.example", WebInternalSecret: "fixture-secret"}
	key, err := cfg.EncryptSEOSecret("indexnow", "fixture-indexnow-key")
	require.NoError(t, err)
	f.exec("UPDATE seo_settings SET indexnow_key_ciphertext=?", key)
	f.n = &Notifier{Store: st, Config: cfg, Now: func() time.Time { return f.now }, Sleep: func(time.Duration) {}, IndexNowEndpoint: "https://site.example/indexnow", Logger: slog.New(slog.NewTextHandler(&f.logs, nil))}
	f.n.HTTP = &http.Client{Transport: contentGateTransport(func(r *http.Request) (*http.Response, error) {
		status, body := 200, ""
		switch r.URL.Path {
		case "/indexnow":
			var request struct {
				URLs []string `json:"urlList"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
			f.submitted = append(f.submitted, request.URLs...)
			if f.onSubmit != nil {
				f.onSubmit()
			}
			status = f.status
		case "/cache/invalidate":
			f.invalidates++
			body = `{"removed":1,"settingsCleared":true}`
		default:
			f.probes++
			if f.onProbe != nil {
				f.onProbe()
			}
			body = fmt.Sprintf(`<link rel="canonical" href="%s"><meta name="robots" content="%s">`, r.URL.String(), seo.IndexableRobots)
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
	})}
	f.n.defaults()
	return f
}

func (f *contentGateFixture) exec(sql string, args ...any) {
	f.t.Helper()
	require.NoError(f.t, f.st.DB.Exec(sql, args...).Error)
}

func (f *contentGateFixture) refresh() {
	f.t.Helper()
	count, err := f.st.RefreshPackagePageStates(context.Background(), 100)
	require.NoError(f.t, err)
	require.Positive(f.t, count)
}

func (f *contentGateFixture) changed(at time.Time) {
	f.t.Helper()
	f.exec("UPDATE packages SET desc_en=desc_en || ' changed' WHERE id=?", f.id)
	f.exec("UPDATE package_page_dirty SET changed_at=? WHERE package_id=?", at, f.id)
	f.refresh()
}

func (f *contentGateFixture) queue(path, robots, phase string) store.URLNotification {
	f.t.Helper()
	note := store.URLNotification{URL: "https://site.example" + path, Path: path, Locale: "en-US", Announce: true, Robots: robots}
	require.NoError(f.t, store.QueueURLNotification(context.Background(), f.st.DB, note))
	f.exec("UPDATE url_notify_outbox SET state=?,next_attempt_at=? WHERE url=? AND state<>'announced'", phase, f.now, note.URL)
	return note
}

func (f *contentGateFixture) receipt(path, robots, canonical string) {
	f.t.Helper()
	f.exec(`INSERT INTO indexnow_url_state(url,announced_at,robots,canonical_path) VALUES(?,?,?,?)
 ON CONFLICT(url) DO UPDATE SET announced_at=EXCLUDED.announced_at,robots=EXCLUDED.robots,canonical_path=EXCLUDED.canonical_path`, "https://site.example"+path, f.now.Add(-time.Hour), robots, canonical)
}

func (f *contentGateFixture) drain() int {
	f.t.Helper()
	_, announced, err := f.n.drainOutbox(context.Background(), "fixture-owner")
	require.NoError(f.t, err)
	return announced
}

type contentGateReceipt struct {
	State, SkipReason, LastError, Robots, CanonicalPath string
	Announce                                            bool
	Attempts                                            int
	NextAttemptAt                                       time.Time
}

func (f *contentGateFixture) read(path string) contentGateReceipt {
	f.t.Helper()
	var row contentGateReceipt
	require.NoError(f.t, f.st.DB.Raw("SELECT * FROM url_notify_outbox WHERE path=? ORDER BY id DESC LIMIT 1", path).Scan(&row).Error)
	return row
}

func TestIndexNowContentGate(t *testing.T) {
	base := testutil.NewSEOStore(t)
	fixture := func(t *testing.T) *contentGateFixture {
		t.Helper()
		tx := base.DB.Begin()
		require.NoError(t, tx.Error)
		t.Cleanup(func() { require.NoError(t, tx.Rollback().Error) })
		return newContentGateFixture(t, &store.Store{DB: tx})
	}
	t.Run("unchanged skips probe and budget after invalidation", func(t *testing.T) {
		f := fixture(t)
		f.receipt("/apps/gate-app", seo.IndexableRobots, "")
		f.queue("/apps/gate-app", seo.IndexableRobots, "pending")
		require.Zero(t, f.drain())
		require.Equal(t, 1, f.invalidates)
		require.Zero(t, f.probes)
		require.Empty(t, f.submitted)
		row := f.read("/apps/gate-app")
		require.Equal(t, "announced", row.State)
		require.False(t, row.Announce)
		require.Equal(t, "unchanged", row.SkipReason)
		require.Empty(t, row.LastError)
		require.Equal(t, seo.IndexableRobots, row.Robots)
		var budgets int64
		require.NoError(t, f.st.DB.Table("indexnow_daily_budget").Count(&budgets).Error)
		require.Zero(t, budgets)
		require.Contains(t, f.logs.String(), "skipped_unchanged=1")
	})
	for _, tc := range []struct {
		name, robots, canonical string
		skip                    bool
	}{
		{"preview directives do not change intent", "index, follow", "", true},
		{"index intent changed", "noindex, follow", "", false},
		{"follow intent changed", "index, nofollow", "", false},
		{"canonical changed", seo.IndexableRobots, "/apps/previous", false},
		{"retired receipt", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := fixture(t)
			f.receipt("/apps/gate-app", tc.robots, tc.canonical)
			f.queue("/apps/gate-app", seo.IndexableRobots, "invalidated")
			want := 1
			if tc.skip {
				want = 0
			}
			require.Equal(t, want, f.drain())
			require.Equal(t, want, f.probes)
		})
	}
	for _, scenario := range []string{"changed", "no record", "missing page state", "unknown package", "retired URL", "article", "disabled gate"} {
		t.Run(scenario, func(t *testing.T) {
			f := fixture(t)
			path, robots := "/apps/gate-app", seo.IndexableRobots
			if scenario != "no record" {
				f.receipt(path, robots, "")
			}
			switch scenario {
			case "changed":
				f.changed(f.now.Add(-45 * time.Minute))
			case "missing page state":
				f.exec("DELETE FROM package_page_state WHERE package_id=?", f.id)
			case "unknown package":
				path = "/apps/unknown"
			case "retired URL":
				robots = ""
			case "article":
				path = "/articles/fixture"
				f.receipt(path, robots, "")
			case "disabled gate":
				f.n.Config.IndexNowContentGate = config.Config{}.IndexNowContentGate
			}
			f.queue(path, robots, "invalidated")
			var expected, accepted time.Time
			if scenario == "changed" || scenario == "no record" {
				require.NoError(t, f.st.DB.Raw("SELECT content_modified_at FROM package_page_state WHERE package_id=? AND locale='en-US' AND page_kind='overview' AND page_number=1", f.id).Scan(&expected).Error)
			}
			require.Equal(t, 1, f.drain())
			require.Equal(t, 1, f.probes)
			require.Len(t, f.submitted, 1)
			require.NoError(t, f.st.DB.Raw("SELECT announced_at FROM indexnow_url_state WHERE url=?", "https://site.example"+path).Scan(&accepted).Error)
			require.True(t, accepted.Equal(expected), "receipt must retain only the verified pre-send content date")
			require.Empty(t, f.read(path).SkipReason)
		})
	}
	t.Run("dirty waits then refreshed unchanged skips", func(t *testing.T) {
		f := fixture(t)
		f.receipt("/apps/gate-app", seo.IndexableRobots, "")
		f.queue("/apps/gate-app", seo.IndexableRobots, "invalidated")
		f.exec("INSERT INTO package_page_dirty(package_id,changed_at) VALUES(?,?)", f.id, f.now.Add(-time.Minute))
		require.Zero(t, f.drain())
		row := f.read("/apps/gate-app")
		require.Equal(t, "invalidated", row.State)
		require.Zero(t, row.Attempts)
		require.Equal(t, f.now.Add(3*time.Minute), row.NextAttemptAt.UTC())
		require.Contains(t, f.logs.String(), "waiting_page_state=1")
		f.refresh()
		f.now = row.NextAttemptAt
		require.Zero(t, f.drain())
		require.Equal(t, "unchanged", f.read("/apps/gate-app").SkipReason)
		require.Zero(t, f.probes)
	})
	t.Run("hash upgrade sentinel does not wait", func(t *testing.T) {
		f := fixture(t)
		f.receipt("/apps/gate-app", seo.IndexableRobots, "")
		f.queue("/apps/gate-app", seo.IndexableRobots, "invalidated")
		f.exec("INSERT INTO package_page_dirty(package_id,changed_at) VALUES(?,'0001-01-01')", f.id)
		require.Zero(t, f.drain())
		require.Equal(t, "unchanged", f.read("/apps/gate-app").SkipReason)
		require.Zero(t, f.n.contentGate.waitingPageState)
	})
	t.Run("stalled dirty sends even with unchanged receipt", func(t *testing.T) {
		f := fixture(t)
		f.receipt("/apps/gate-app", seo.IndexableRobots, "")
		f.queue("/apps/gate-app", seo.IndexableRobots, "invalidated")
		f.exec("INSERT INTO package_page_dirty(package_id,changed_at) VALUES(?,?)", f.id, f.now.Add(-7*time.Hour))
		require.Equal(t, 1, f.drain())
		require.Contains(t, f.logs.String(), "level=WARN")
		require.Contains(t, f.logs.String(), "page state refresh stalled")
	})
	t.Run("quiet period expires", func(t *testing.T) {
		f := fixture(t)
		f.receipt("/apps/gate-app", seo.IndexableRobots, "")
		f.changed(f.now.Add(-10 * time.Minute))
		f.queue("/apps/gate-app", seo.IndexableRobots, "invalidated")
		require.Zero(t, f.drain())
		row := f.read("/apps/gate-app")
		require.Zero(t, row.Attempts)
		require.Equal(t, "invalidated", row.State)
		require.Equal(t, f.now.Add(20*time.Minute), row.NextAttemptAt.UTC())
		require.Contains(t, f.logs.String(), "waiting_quiet=1")
		f.now = row.NextAttemptAt
		require.Equal(t, 1, f.drain())
	})
	t.Run("versions uses its own date and uninstall uses overview", func(t *testing.T) {
		f := fixture(t)
		f.changed(f.now.Add(-45 * time.Minute))
		for _, path := range []string{"/apps/gate-app/versions", "/apps/gate-app/uninstall"} {
			f.receipt(path, seo.IndexableRobots, "")
			f.queue(path, seo.IndexableRobots, "invalidated")
		}
		require.Equal(t, 1, f.drain())
		require.Equal(t, []string{"https://site.example/apps/gate-app/uninstall"}, f.submitted)
		require.Equal(t, "unchanged", f.read("/apps/gate-app/versions").SkipReason)
	})
	t.Run("failed submission has no receipt and retries", func(t *testing.T) {
		f := fixture(t)
		f.status = 500
		f.queue("/apps/gate-app", seo.IndexableRobots, "invalidated")
		_, announced, err := f.n.drainOutbox(context.Background(), "fixture-owner")
		require.ErrorContains(t, err, "IndexNow returned 500")
		require.Zero(t, announced)
		row := f.read("/apps/gate-app")
		require.Equal(t, "probed", row.State)
		require.Equal(t, 1, row.Attempts)
		var receipts int64
		require.NoError(t, f.st.DB.Table("indexnow_url_state").Count(&receipts).Error)
		require.Zero(t, receipts)
	})
	t.Run("accepted response records despite revision change", func(t *testing.T) {
		f := fixture(t)
		f.status = 202
		note := f.queue("/apps/gate-app", seo.IndexableRobots, "probed")
		f.onSubmit = func() { require.NoError(t, store.QueueURLNotification(context.Background(), f.st.DB, note)) }
		require.Equal(t, 1, f.drain())
		var receipts int64
		require.NoError(t, f.st.DB.Table("indexnow_url_state").Count(&receipts).Error)
		require.EqualValues(t, 1, receipts)
		require.Equal(t, "unchanged", f.read(note.Path).SkipReason, "new revision is rechecked against the accepted request")
	})
	t.Run("content changed during accepted request is submitted again", func(t *testing.T) {
		f := fixture(t)
		note := f.queue("/apps/gate-app", seo.IndexableRobots, "probed")
		f.onSubmit = func() {
			f.onSubmit = nil
			// This new content date follows the checked snapshot but predates the response.
			f.changed(f.now.Add(-45 * time.Minute))
			require.NoError(t, store.QueueURLNotification(context.Background(), f.st.DB, note))
		}
		require.Equal(t, 1, f.drain())
		require.Empty(t, f.read(note.Path).SkipReason)
		require.Equal(t, 1, f.drain())
		require.Equal(t, []string{note.URL, note.URL}, f.submitted)
		require.Equal(t, "announced", f.read(note.Path).State)
		require.Empty(t, f.read(note.Path).SkipReason)
	})
	t.Run("second gate catches dirty edit during another probe", func(t *testing.T) {
		f := fixture(t)
		f.queue("/apps/gate-app", seo.IndexableRobots, "probed")
		f.queue("/articles/fixture", seo.IndexableRobots, "invalidated")
		f.onProbe = func() { f.exec("INSERT INTO package_page_dirty(package_id,changed_at) VALUES(?,?)", f.id, f.now) }
		require.Equal(t, 1, f.drain())
		require.Equal(t, []string{"https://site.example/articles/fixture"}, f.submitted)
		require.Equal(t, "probed", f.read("/apps/gate-app").State)
		require.Zero(t, f.read("/apps/gate-app").Attempts)
	})
	t.Run("skip fences revision lease and expiry", func(t *testing.T) {
		f := fixture(t)
		note := f.queue("/apps/gate-app", seo.IndexableRobots, "invalidated")
		ctx := context.Background()
		rows, err := f.st.ClaimURLNotifications(ctx, "fixture-owner", f.now, 1)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		for _, change := range []func(*store.URLNotificationRow){
			func(r *store.URLNotificationRow) { r.Revision-- },
			func(r *store.URLNotificationRow) { r.LeaseToken = "other-owner" },
		} {
			row := rows[0]
			change(&row)
			skipped, err := f.st.SkipUnchangedURLAnnouncement(ctx, row, f.now)
			require.NoError(t, err)
			require.False(t, skipped)
		}
		skipped, err := f.st.SkipUnchangedURLAnnouncement(ctx, rows[0], f.now.Add(3*time.Minute))
		require.NoError(t, err)
		require.False(t, skipped)
		require.Equal(t, "invalidated", f.read(note.Path).State)
		skipped, err = f.st.SkipUnchangedURLAnnouncement(ctx, rows[0], f.now)
		require.NoError(t, err)
		require.True(t, skipped)
	})
}

func TestIndexNowContentGateRunStatus(t *testing.T) {
	f := newContentGateFixture(t, testutil.NewFactsStore(t))
	f.exec("UPDATE url_notify_state SET cursor=(SELECT COALESCE(max(seq),0) FROM catalog_changes),content_cursor=(SELECT COALESCE(max(seq),0) FROM content_url_changes),index_backfill=?", indexBackfill)
	f.receipt("/apps/gate-app", seo.IndexableRobots, "")
	f.queue("/apps/gate-app", seo.IndexableRobots, "invalidated")
	result, err := f.n.Run(context.Background())
	require.NoError(t, err)
	require.Zero(t, result.Announced)
	state, err := f.st.URLNotifyState(context.Background())
	require.NoError(t, err)
	require.Equal(t, "outbox: 0 announced, 1 unchanged skipped", state.LastIndexnowStatus)
}
