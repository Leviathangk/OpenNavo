//go:build integration

package urlnotify_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/service/urlnotify"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/opennavo/opennavo/server/seeds"
	"github.com/stretchr/testify/require"
)

type recorder struct {
	sync.Mutex
	status   int
	requests []map[string]any
	paths    []string
	auth     []string
	reply    string
	// replies overrides reply per request path.
	replies map[string]string
}

func (r *recorder) server(t *testing.T) *httptest.Server {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// GETs (the web build poll) carry no body and are answered without being recorded.
		get := req.Method == http.MethodGet
		body, _ := io.ReadAll(req.Body)
		var payload map[string]any
		if !get {
			require.NoError(t, json.Unmarshal(body, &payload))
		}
		r.Lock()
		if !get {
			r.requests = append(r.requests, payload)
			r.paths = append(r.paths, req.URL.Path)
			r.auth = append(r.auth, req.Header.Get("Authorization"))
		}
		status, reply := r.status, r.reply
		if custom, ok := r.replies[req.URL.Path]; ok {
			reply = custom
		}
		r.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(server.Close)
	return server
}

// feed is a seeded store and a notifier pointed at recorders for the web's internal port, Cloudflare
// and IndexNow.
type feed struct {
	now                       *time.Time
	st                        *store.Store
	notifier                  *urlnotify.Notifier
	web, cloudflare, indexNow *recorder
}

// newFeed starts a feed with IndexNow, HTML edge caching and Cloudflare purging configured.
func newFeed(t *testing.T) feed {
	st := testutil.NewStore(t)
	data, err := seeds.Load()
	require.NoError(t, err)
	require.NoError(t, st.Seed(context.Background(), data, "", ""))
	web, cloudflare, indexNow := &recorder{status: 200, reply: `{"removed":3}`}, &recorder{status: 200, reply: `{"success":true}`}, &recorder{status: 200}
	webServer, cfServer, indexNowServer := web.server(t), cloudflare.server(t), indexNow.server(t)
	cfg := config.Config{JWTSecret: strings.Repeat("a", 32), WebBaseURL: "https://opennavo.example", WebInternalURL: webServer.URL, WebInternalSecret: strings.Repeat("s", 32)}
	key, err := cfg.EncryptSEOSecret("indexnow", "fixture-indexnow-key-0001")
	require.NoError(t, err)
	token, err := cfg.EncryptSEOSecret("cloudflare", "fixture-cloudflare-credential")
	require.NoError(t, err)
	require.NoError(t, st.DB.Exec("UPDATE seo_settings SET indexnow_key_ciphertext=?,cloudflare_token_ciphertext=?", key, token).Error)
	configure(t, st, func(settings *store.SEOSettings) {
		settings.IndexNow.Enabled = true
		edgePurge(settings)
	})
	notifier := &urlnotify.Notifier{Store: st, Config: cfg, Settle: time.Nanosecond, IndexNowEndpoint: indexNowServer.URL, CloudflareAPI: cfServer.URL, Sleep: func(time.Duration) {}}
	now := time.Now().Add(time.Minute)
	notifier.Now = func() time.Time { return now }
	return feed{now: &now, st: st, notifier: notifier, web: web, cloudflare: cloudflare, indexNow: indexNow}
}

func (f feed) run(ctx context.Context) (urlnotify.Result, error) {
	*f.now = f.now.Add(30 * time.Second)
	return f.notifier.Run(ctx)
}

// configure saves the default SEO settings with change applied.
func configure(t *testing.T, st *store.Store, change func(*store.SEOSettings)) {
	settings := store.DefaultSEOSettings()
	change(&settings)
	document, err := json.Marshal(settings)
	require.NoError(t, err)
	require.NoError(t, st.DB.Exec("UPDATE seo_settings SET settings=?::jsonb", string(document)).Error)
}

// edgePurge turns on HTML edge caching and Cloudflare purging.
func edgePurge(settings *store.SEOSettings) {
	settings.EdgeCache.Enabled = true
	settings.Cloudflare.ZoneID, settings.Cloudflare.PurgeEnabled = strings.Repeat("0f", 16), true
}

// addCasks adds count new casks named prefix-000 onwards and lets their changes settle.
func addCasks(t *testing.T, st *store.Store, prefix string, count int) {
	mutations := make([]store.CatalogMutation, 0, count)
	for i := range count {
		p, err := homebrew.Normalize("cask", json.RawMessage(fmt.Sprintf(`{"token":"%s-%03d","name":["App %d"],"version":"1.0","desc":"Does things"}`, prefix, i, i)))
		require.NoError(t, err)
		mutations = append(mutations, store.CatalogMutation{Package: p})
	}
	require.NoError(t, st.ApplyCatalogBatch(context.Background(), mutations))
	time.Sleep(10 * time.Millisecond)
}

// sections are the long-cached sections in every language, as purge prefixes.
func sections() []any {
	prefixes := []any{}
	for _, language := range []string{"", "/zh", "/ja", "/es", "/pt", "/ru"} {
		for _, section := range []string{"/apps", "/articles", "/authors"} {
			prefixes = append(prefixes, "opennavo.example"+language+section)
		}
	}
	return prefixes
}

func TestURLChangeFeedRefreshesPagesThenNotifies(t *testing.T) {
	ctx := context.Background()
	f := newFeed(t)
	st, web, cloudflare, indexNow := f.st, f.web, f.cloudflare, f.indexNow
	result, err := f.run(ctx)
	require.NoError(t, err)
	require.Zero(t, result.Changes, "the feed starts at the current position")

	p, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"editor","name":["Editor"],"version":"2.0","desc":"Edits text"}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p}}))
	time.Sleep(10 * time.Millisecond)

	// A failing web endpoint keeps the cursor and skips Cloudflare and IndexNow.
	web.status = 500
	_, err = f.run(ctx)
	require.Error(t, err)
	state, err := st.URLNotifyState(ctx)
	require.NoError(t, err)
	require.Equal(t, "page cache: web cache endpoint returned 500", state.LastError)
	require.Empty(t, cloudflare.requests)
	require.Empty(t, indexNow.requests)
	cursor := state.Cursor

	web.status = 200
	result, err = f.run(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, result.Changes)
	require.Equal(t, 6, result.Paths)
	// Page caches first, then a probe of a few changed pages before IndexNow.
	require.Equal(t, []string{"/cache/invalidate", "/cache/invalidate", "/cache/probe"}, web.paths)
	paths := web.requests[1]["paths"].([]any)
	require.Len(t, paths, 6)
	require.Contains(t, paths, "/apps/editor")
	require.Contains(t, paths, "/zh/apps/editor")
	require.Len(t, web.requests[2]["paths"], 3)
	require.Equal(t, "Bearer "+strings.Repeat("s", 32), web.auth[len(web.auth)-1])

	require.Len(t, cloudflare.requests, 1, "6 prefixes fit one purge")
	require.Equal(t, []any{"opennavo.example/apps/editor", "opennavo.example/zh/apps/editor", "opennavo.example/ja/apps/editor", "opennavo.example/es/apps/editor", "opennavo.example/pt/apps/editor", "opennavo.example/ru/apps/editor"}, cloudflare.requests[0]["prefixes"])
	require.Equal(t, "Bearer fixture-cloudflare-credential", cloudflare.auth[0])
	require.Len(t, indexNow.requests, 1)
	submitted := indexNow.requests[0]
	require.Equal(t, "opennavo.example", submitted["host"])
	require.Equal(t, "fixture-indexnow-key-0001", submitted["key"])
	require.Equal(t, "https://opennavo.example/fixture-indexnow-key-0001.txt", submitted["keyLocation"])
	require.Contains(t, submitted["urlList"], "https://opennavo.example/ru/apps/editor")

	state, err = st.URLNotifyState(ctx)
	require.NoError(t, err)
	require.Greater(t, state.Cursor, cursor)
	require.Equal(t, "ok: 6 prefixes", state.LastPurgeStatus)
	require.Equal(t, "ok: 6 URLs", state.LastIndexnowStatus)
	require.Empty(t, state.LastError)

	// Nothing new: no calls. Rate limits retry, and a pending key verification is reported apart.
	result, err = f.run(ctx)
	require.NoError(t, err)
	require.Zero(t, result.Changes)
	require.Len(t, indexNow.requests, 1)
	p.Version = "2.1"
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p, Previous: &store.CatalogRow{ID: 1, Version: "2.0"}}}))
	time.Sleep(10 * time.Millisecond)
	indexNow.status = 202
	_, err = f.run(ctx)
	require.NoError(t, err)
	state, err = st.URLNotifyState(ctx)
	require.NoError(t, err)
	require.Equal(t, "accepted: 6 URLs, key verification pending", state.LastIndexnowStatus)

	// A page still failing after the refresh holds back the announcement.
	p.Version = "2.2"
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p, Previous: &store.CatalogRow{ID: 1, Version: "2.1"}}}))
	time.Sleep(10 * time.Millisecond)
	web.replies = map[string]string{"/cache/probe": `{"statuses":{"/apps/editor":503}}`}
	before := len(indexNow.requests)
	_, err = f.run(ctx)
	require.NoError(t, err)
	state, err = st.URLNotifyState(ctx)
	require.NoError(t, err)
	require.Equal(t, "skipped: probe /apps/editor returned 503", state.LastIndexnowStatus)
	require.Len(t, indexNow.requests, before)
}

func TestURLChangeFeedPurgesByPrefixAndFallsBackToSections(t *testing.T) {
	ctx := context.Background()
	f := newFeed(t)
	_, err := f.run(ctx)
	require.NoError(t, err)

	// 17 apps in six languages are 102 prefixes: one request of 100, then one of 2.
	addCasks(t, f.st, "chunk", 17)
	result, err := f.run(ctx)
	require.NoError(t, err)
	require.Equal(t, 17, result.Changes)
	require.Equal(t, 100, result.Purged)
	require.Len(t, f.cloudflare.requests, 1)
	state, err := f.st.URLNotifyState(ctx)
	require.NoError(t, err)
	require.Len(t, state.PendingPurgePrefixes, 2)
	cursor := state.Cursor
	restarted := *f.notifier
	f.notifier = &restarted
	result, err = f.run(ctx)
	require.NoError(t, err)
	require.Zero(t, result.Changes)
	require.Equal(t, 2, result.Purged)
	require.Len(t, f.cloudflare.requests, 2)
	require.Len(t, f.cloudflare.requests[0]["prefixes"], 100)
	require.Len(t, f.cloudflare.requests[1]["prefixes"], 2)
	sent := slices.Concat(f.cloudflare.requests[0]["prefixes"].([]any), f.cloudflare.requests[1]["prefixes"].([]any))
	require.Contains(t, sent, "opennavo.example/apps/chunk-000")
	require.Contains(t, sent, "opennavo.example/ru/apps/chunk-016")
	state, err = f.st.URLNotifyState(ctx)
	require.NoError(t, err)
	require.Equal(t, "ok: 2 prefixes", state.LastPurgeStatus)

	require.Equal(t, cursor, state.Cursor)
	require.Empty(t, state.PendingPurgePrefixes)

	// 167 apps are 1002 prefixes, more than a run sends one by one: a single request purges the
	// long-cached sections instead.
	addCasks(t, f.st, "bulk", 167)
	result, err = f.run(ctx)
	require.NoError(t, err)
	require.Equal(t, 167, result.Changes)
	require.Len(t, f.cloudflare.requests, 3)
	require.Equal(t, sections(), f.cloudflare.requests[2]["prefixes"])
	state, err = f.st.URLNotifyState(ctx)
	require.NoError(t, err)
	require.Equal(t, "ok: 18 prefixes", state.LastPurgeStatus)
}

func TestWebReleasePurgesLongCachedSectionsOnce(t *testing.T) {
	ctx := context.Background()
	f := newFeed(t)
	secret := f.notifier.Config.WebInternalSecret
	// The web's internal port tells its build only to the shared secret, and answers page-cache calls.
	var mu sync.Mutex
	build := "6f1c2a9e-3b7d-4c55-9a1e-0d2f8b7c6e41"
	release := func(id string) {
		mu.Lock()
		defer mu.Unlock()
		build = id
	}
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case req.Header.Get("Authorization") != "Bearer "+secret:
			w.WriteHeader(http.StatusUnauthorized)
		case req.Method == http.MethodGet && req.URL.Path == "/cache/build":
			_, _ = fmt.Fprintf(w, `{"build":%q}`, build)
		default:
			_, _ = w.Write([]byte(`{"removed":0,"statuses":{}}`))
		}
	}))
	t.Cleanup(web.Close)
	run := func() urlnotify.Result {
		result, err := f.run(ctx)
		require.NoError(t, err)
		return result
	}
	purgeStatus := func() string {
		state, err := f.st.URLNotifyState(ctx)
		require.NoError(t, err)
		return state.LastPurgeStatus
	}
	stored := func() string {
		value, _ := f.st.Redis.Get(ctx, "opennavo:url-notify:web-build").Result()
		return value
	}

	// An unreachable or refusing web port is skipped silently, and asked again next run.
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	f.notifier.Config.WebInternalURL = closed.URL
	run()
	f.notifier.Config.WebInternalURL, f.notifier.Config.WebInternalSecret = web.URL, strings.Repeat("x", 32)
	run()
	require.Empty(t, f.cloudflare.requests)
	require.Empty(t, stored())
	require.Empty(t, purgeStatus())

	// A new build purges the long-cached sections once, also in a run without changes.
	f.notifier.Config.WebInternalSecret = secret
	require.Zero(t, run().Changes)
	require.Len(t, f.cloudflare.requests, 1)
	require.Equal(t, sections(), f.cloudflare.requests[0]["prefixes"])
	require.Equal(t, "Bearer fixture-cloudflare-credential", f.cloudflare.auth[0])
	require.Equal(t, "ok: release 6f1c2a9e (18 prefixes)", purgeStatus())
	require.Equal(t, "6f1c2a9e-3b7d-4c55-9a1e-0d2f8b7c6e41", stored())
	run()
	require.Len(t, f.cloudflare.requests, 1, "an unchanged build is not purged again")

	// Without Redis the build cannot be remembered, so releases are not purged.
	release("9d4e7b21-0c3a-4f8e-b6d5-2a1f9e8c7b30")
	withoutRedis := *f.notifier
	withoutRedis.Store = &store.Store{DB: f.st.DB}
	_, err := withoutRedis.Run(ctx)
	require.NoError(t, err)
	require.Len(t, f.cloudflare.requests, 1)

	// A failed purge leaves the build unrecorded and pauses attempts for a while; once the pause is
	// over the next run purges again.
	f.cloudflare.status = 500
	run()
	require.Len(t, f.cloudflare.requests, 2, "failed requests are not immediately retried")
	require.Equal(t, "error: Cloudflare returned 500 for release 9d4e7b21", purgeStatus())
	require.Equal(t, "6f1c2a9e-3b7d-4c55-9a1e-0d2f8b7c6e41", stored())
	f.cloudflare.status = 200
	run()
	require.Len(t, f.cloudflare.requests, 2, "paused after the failure")
	ttl, err := f.st.Redis.TTL(ctx, "opennavo:url-notify:web-build:retry").Result()
	require.NoError(t, err)
	require.InDelta(t, 5*time.Minute, ttl, float64(time.Minute))
	require.NoError(t, f.st.Redis.Del(ctx, "opennavo:url-notify:web-build:retry").Err())
	run()
	require.Len(t, f.cloudflare.requests, 3)
	require.Equal(t, sections(), f.cloudflare.requests[2]["prefixes"])
	require.Equal(t, "ok: release 9d4e7b21 (18 prefixes)", purgeStatus())
	require.Equal(t, "9d4e7b21-0c3a-4f8e-b6d5-2a1f9e8c7b30", stored())

	// Without HTML edge caching Cloudflare holds no pages: the build is only recorded.
	configure(t, f.st, func(*store.SEOSettings) {})
	release("c3b5a7e9-1d2f-4a6b-8c0e-5f7a9b1c3d5e")
	run()
	require.Len(t, f.cloudflare.requests, 3)
	require.Equal(t, "c3b5a7e9-1d2f-4a6b-8c0e-5f7a9b1c3d5e", stored())
	require.Equal(t, "ok: release 9d4e7b21 (18 prefixes)", purgeStatus())

	// A release and changed pages in one run: the sections go first, and the status keeps both.
	configure(t, f.st, edgePurge)
	release("1e2d3c4b-5a69-4788-9a0b-c1d2e3f4a5b6")
	addCasks(t, f.st, "released", 1)
	require.Equal(t, 1, run().Changes)
	require.Len(t, f.cloudflare.requests, 4)
	require.Equal(t, sections(), f.cloudflare.requests[3]["prefixes"])
	require.Contains(t, purgeStatus(), "pending: 6 prefixes")
	require.Zero(t, run().Changes)
	require.Len(t, f.cloudflare.requests[4]["prefixes"], 6)
	require.Equal(t, "ok: 6 prefixes", purgeStatus())
}

func TestPendingPurgesSurviveFailuresAndRestart(t *testing.T) {
	ctx := context.Background()
	for _, status := range []int{http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusUnauthorized} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			f := newFeed(t)
			_, err := f.run(ctx)
			require.NoError(t, err)
			addCasks(t, f.st, "retry", 17)
			result, err := f.run(ctx)
			require.NoError(t, err)
			require.Equal(t, 100, result.Purged)
			state, err := f.st.URLNotifyState(ctx)
			require.NoError(t, err)
			remaining := slices.Clone(state.PendingPurgePrefixes)
			require.Len(t, remaining, 2)
			cursor := state.Cursor

			f.cloudflare.status = status
			result, err = f.run(ctx)
			require.NoError(t, err)
			require.Zero(t, result.Purged)
			require.Len(t, f.cloudflare.requests, 2, "one failed attempt, no immediate retries")
			state, err = f.st.URLNotifyState(ctx)
			require.NoError(t, err)
			require.Equal(t, remaining, state.PendingPurgePrefixes)

			// A new worker without Redis still observes the database rate limit and queue.
			restarted := *f.notifier
			restarted.Store = &store.Store{DB: f.st.DB}
			f.notifier = &restarted
			f.cloudflare.status = http.StatusOK
			_, err = f.notifier.Run(ctx)
			require.NoError(t, err)
			require.Len(t, f.cloudflare.requests, 2, "restart cannot reset the limiter")
			result, err = f.run(ctx)
			require.NoError(t, err)
			require.Zero(t, result.Changes)
			require.Equal(t, 2, result.Purged)
			require.Equal(t, f.cloudflare.requests[1]["prefixes"], f.cloudflare.requests[2]["prefixes"])
			state, err = f.st.URLNotifyState(ctx)
			require.NoError(t, err)
			require.Empty(t, state.PendingPurgePrefixes)
			require.Equal(t, cursor, state.Cursor)
		})
	}
}

func TestIndexBackfillAnnouncesPausedAppsOnce(t *testing.T) {
	ctx := context.Background()
	f := newFeed(t)
	st := f.st
	for _, raw := range []string{
		`{"token":"paused-app","name":["Paused App"],"version":"5.0","desc":"Still in Homebrew"}`,
		`{"token":"gone-app","name":["Gone"],"version":"1","desc":"Old tool","disabled":true,"disable_date":"2026-01-01"}`,
		`{"token":"hidden-paused","name":["Hidden"],"version":"1","desc":"Hidden tool"}`,
		`{"token":"fresh-app","name":["Fresh"],"version":"1","desc":"New tool"}`,
	} {
		p, err := homebrew.Normalize("cask", json.RawMessage(raw))
		require.NoError(t, err)
		require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p}}))
	}
	for _, sql := range []string{
		`UPDATE packages SET stale_disabled=true WHERE token IN ('paused-app','gone-app','hidden-paused')`,
		`INSERT INTO package_meta(package_id,hidden) SELECT id,true FROM packages WHERE token='hidden-paused'`,
		// Only the rule changed: the feed itself has no catalog change left to announce them.
		`UPDATE url_notify_state SET cursor=(SELECT max(seq) FROM catalog_changes)`,
	} {
		require.NoError(t, st.DB.Exec(sql).Error, sql)
	}

	// A page that still fails holds the announcement back, and the backfill waits instead of
	// refreshing every run.
	f.web.replies = map[string]string{"/cache/probe": `{"statuses":{"/apps/paused-app":503}}`}
	_, err := f.run(ctx)
	require.NoError(t, err)
	state, err := st.URLNotifyState(ctx)
	require.NoError(t, err)
	require.Equal(t, "index backfill skipped: probe /apps/paused-app returned 503", state.LastIndexnowStatus)
	require.Equal(t, "2026-10-18-paused-apps:purging", state.IndexBackfill)
	require.Empty(t, f.indexNow.requests)
	calls := len(f.web.requests)
	_, err = f.run(ctx)
	require.NoError(t, err)
	require.Len(t, f.web.requests, calls, "waiting: no second refresh")

	// Then it refreshes, purges and announces only the paused app that Homebrew still offers.
	require.NoError(t, st.Redis.Del(ctx, "opennavo:url-notify:index-backfill:retry").Err())
	f.web.replies = nil
	f.web.requests, f.web.paths, f.cloudflare.requests = nil, nil, nil
	_, err = f.run(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"/cache/invalidate", "/cache/probe"}, f.web.paths)
	require.ElementsMatch(t, []any{"/apps/paused-app", "/zh/apps/paused-app", "/ja/apps/paused-app", "/es/apps/paused-app", "/pt/apps/paused-app", "/ru/apps/paused-app"}, f.web.requests[0]["paths"])
	require.Empty(t, f.cloudflare.requests, "completed backfill purges must not be requeued")
	require.Len(t, f.indexNow.requests, 1)
	urls := f.indexNow.requests[0]["urlList"].([]any)
	require.Len(t, urls, 6)
	require.Equal(t, "https://opennavo.example/apps/paused-app", urls[0])
	for _, url := range urls {
		require.Contains(t, url, "/apps/paused-app")
	}
	state, err = st.URLNotifyState(ctx)
	require.NoError(t, err)
	require.Equal(t, "2026-10-18-paused-apps", state.IndexBackfill)
	require.Equal(t, "index backfill ok: 6 URLs", state.LastIndexnowStatus)
	var queued int
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM job_outbox WHERE unique_key='cache:invalidate:url-notify:index-backfill'").Scan(&queued).Error)
	require.Zero(t, queued, "the Go API cache invalidation was published before the pages were refreshed")

	// Once finished it never runs again.
	_, err = f.run(ctx)
	require.NoError(t, err)
	require.Len(t, f.indexNow.requests, 1)
}

func TestIndexBackfillPurgeProgressSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	f := newFeed(t)
	for i := 0; i < 40; i++ {
		p, err := homebrew.Normalize("cask", json.RawMessage(fmt.Sprintf(
			`{"token":"paused-%d","name":["Paused"],"version":"1","desc":"Still available"}`, i)))
		require.NoError(t, err)
		require.NoError(t, f.st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p}}))
	}
	require.NoError(t, f.st.DB.Exec("UPDATE packages SET stale_disabled=true").Error)
	require.NoError(t, f.st.DB.Exec("UPDATE url_notify_state SET cursor=(SELECT max(seq) FROM catalog_changes)").Error)
	seen := map[string]bool{}
	for attempt := 0; attempt < 5; attempt++ {
		require.NoError(t, f.st.Redis.Del(ctx, "opennavo:url-notify:index-backfill:retry").Err())
		// A fresh worker must use the persisted phase and remaining prefixes.
		restarted := *f.notifier
		f.notifier = &restarted
		_, err := f.run(ctx)
		require.NoError(t, err)
		for _, request := range f.cloudflare.requests {
			for _, value := range request["prefixes"].([]any) {
				prefix := value.(string)
				require.False(t, seen[prefix], "completed prefix requeued: %s", prefix)
				seen[prefix] = true
			}
		}
		f.cloudflare.requests = nil
		state, err := f.st.URLNotifyState(ctx)
		require.NoError(t, err)
		if state.IndexBackfill == "2026-10-18-paused-apps" {
			require.Len(t, seen, 240)
			require.NotEmpty(t, f.indexNow.requests)
			return
		}
		require.Empty(t, f.indexNow.requests)
	}
	t.Fatal("backfill did not finish")
}
