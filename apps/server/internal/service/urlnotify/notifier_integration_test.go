//go:build integration

package urlnotify_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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
	status       int
	publicStatus int
	requests     []map[string]any
	paths        []string
	auth         []string
	reply        string
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
	st := testutil.NewFactsStore(t)
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
	notifier.HTTP = &http.Client{Timeout: time.Second, Transport: fixtureTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "opennavo.example" {
			web.Lock()
			status := web.publicStatus
			web.Unlock()
			if status == 0 {
				status = 200
			}
			body := fmt.Sprintf(`<html><head><link rel="canonical" href="%s"><meta name="robots" content="index, follow"></head></html>`, req.URL.String())
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: req}, nil
		}
		return http.DefaultTransport.RoundTrip(req)
	})}
	now := time.Now().Add(time.Minute)
	notifier.Now = func() time.Time { return now }
	return feed{now: &now, st: st, notifier: notifier, web: web, cloudflare: cloudflare, indexNow: indexNow}
}

func (f feed) run(ctx context.Context) (urlnotify.Result, error) {
	*f.now = f.now.Add(2 * time.Minute)
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
	for _, language := range []string{"", "/zh", "/ja", "/es", "/pt", "/ru", "/de", "/fr", "/it", "/zh-hant"} {
		for _, section := range []string{"/apps", "/articles", "/authors", "/collections", "/recent"} {
			prefixes = append(prefixes, "opennavo.example"+language+section)
		}
	}
	return prefixes
}

type fixtureTransport func(*http.Request) (*http.Response, error)

func (f fixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func outboxRemaining(t *testing.T, st *store.Store) int64 {
	t.Helper()
	var count int64
	require.NoError(t, st.DB.Table("url_notify_outbox").Where("state IN ('pending','invalidated','probed')").Count(&count).Error)
	return count
}
func finishFeed(t *testing.T, f feed) {
	t.Helper()
	for attempt := 0; attempt < 12; attempt++ {
		_, _ = f.run(context.Background())
		if outboxRemaining(t, f.st) == 0 {
			return
		}
	}
	t.Fatal("outbox did not drain")
}
func TestURLChangeFeedRefreshesPagesThenNotifies(t *testing.T) {
	ctx := context.Background()
	f := newFeed(t)
	_, err := f.run(ctx)
	require.NoError(t, err)
	addCasks(t, f.st, "editor", 1)
	f.web.status = 500
	result, err := f.run(ctx)
	require.Error(t, err)
	require.Equal(t, 1, result.Changes)
	state, err := f.st.URLNotifyState(ctx)
	require.NoError(t, err)
	require.Positive(t, state.Cursor)
	require.Contains(t, state.LastError, "web cache endpoint returned 500")
	require.Empty(t, f.cloudflare.requests)
	require.Empty(t, f.indexNow.requests)
	require.EqualValues(t, 20, outboxRemaining(t, f.st))
	cursor := state.Cursor
	f.web.status = 200
	finishFeed(t, f)
	state, err = f.st.URLNotifyState(ctx)
	require.NoError(t, err)
	require.Equal(t, cursor, state.Cursor)
	require.Empty(t, state.LastError)
	require.Len(t, f.cloudflare.requests, 1)
	require.Len(t, f.cloudflare.requests[0]["prefixes"], 20)
	require.NotEmpty(t, f.indexNow.requests)
	submitted := f.indexNow.requests[len(f.indexNow.requests)-1]
	require.Equal(t, "opennavo.example", submitted["host"])
	require.Len(t, submitted["urlList"], 6)
	require.Equal(t, "fixture-indexnow-key-0001", submitted["key"])
	require.Equal(t, "https://opennavo.example/fixture-indexnow-key-0001.txt", submitted["keyLocation"])
	calls := len(f.indexNow.requests)
	_, err = f.run(ctx)
	require.NoError(t, err)
	require.Len(t, f.indexNow.requests, calls)
}

func TestURLChangeFeedBoundsPurgeBatchesAndResumes(t *testing.T) {
	f := newFeed(t)
	_, err := f.run(context.Background())
	require.NoError(t, err)
	addCasks(t, f.st, "chunk", 17)
	_, _ = f.run(context.Background())
	require.Positive(t, outboxRemaining(t, f.st))
	restarted := *f.notifier
	f.notifier = &restarted
	finishFeed(t, f)
	seen := map[string]bool{}
	for _, r := range f.cloudflare.requests {
		prefixes := r["prefixes"].([]any)
		require.LessOrEqual(t, len(prefixes), 100)
		for _, v := range prefixes {
			seen[v.(string)] = true
		}
	}
	require.Len(t, seen, 180)
	require.Contains(t, seen, "opennavo.example/fr/recent")
	require.Contains(t, seen, "opennavo.example/fr/apps/chunk-016")
}

func TestWebReleasePurgesLongCachedSectionsOnce(t *testing.T) {
	f := newFeed(t)
	f.web.replies = map[string]string{"/cache/build": `{"build":"fixture-build-one"}`}
	_, err := f.run(context.Background())
	require.NoError(t, err)
	require.Len(t, f.cloudflare.requests, 1)
	require.Equal(t, sections(), f.cloudflare.requests[0]["prefixes"])
	_, err = f.run(context.Background())
	require.NoError(t, err)
	require.Len(t, f.cloudflare.requests, 1)
	f.web.replies["/cache/build"] = `{"build":"fixture-build-two"}`
	f.cloudflare.status = 500
	_, err = f.run(context.Background())
	require.NoError(t, err)
	stored, err := f.st.Redis.Get(context.Background(), "opennavo:url-notify:web-build").Result()
	require.NoError(t, err)
	require.Equal(t, "fixture-build-one", stored)
	f.cloudflare.status = 200
	require.NoError(t, f.st.Redis.Del(context.Background(), "opennavo:url-notify:web-build:retry").Err())
	_, err = f.run(context.Background())
	require.NoError(t, err)
	require.Len(t, f.cloudflare.requests, 3)
	stored, err = f.st.Redis.Get(context.Background(), "opennavo:url-notify:web-build").Result()
	require.NoError(t, err)
	require.Equal(t, "fixture-build-two", stored)
}

func TestPendingPurgesSurviveFailuresAndRestart(t *testing.T) {
	for _, status := range []int{429, 500, 401} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			f := newFeed(t)
			_, err := f.run(context.Background())
			require.NoError(t, err)
			addCasks(t, f.st, "retry", 1)
			f.cloudflare.status = status
			_, err = f.run(context.Background())
			if status == 429 {
				// Waiting for purge capacity defers the rows, without spending their attempts.
				require.NoError(t, err)
				require.EqualValues(t, 20, outboxRemaining(t, f.st))
				require.Empty(t, f.indexNow.requests)
			} else {
				// A failed purge waits in the durable queue; the rows go on to their probe and IndexNow.
				require.Error(t, err)
				require.Zero(t, outboxRemaining(t, f.st))
				require.NotEmpty(t, f.indexNow.requests)
				require.Len(t, queuedPurges(t, f.st), 20)
			}
			restarted := *f.notifier
			f.notifier = &restarted
			f.cloudflare.status = 200
			finishFeed(t, f)
			if status != 429 {
				_, err = f.run(context.Background())
				require.NoError(t, err)
			}
			require.NotEmpty(t, f.indexNow.requests)
			require.Empty(t, queuedPurges(t, f.st))
		})
	}
}

func TestIndexBackfillAnnouncesPausedAppsOnce(t *testing.T) {
	f := newFeed(t)
	addCasks(t, f.st, "paused", 1)
	addCasks(t, f.st, "gone", 1)
	addCasks(t, f.st, "hidden", 1)
	require.NoError(t, f.st.DB.Exec("UPDATE packages SET stale_disabled=true").Error)
	require.NoError(t, f.st.DB.Exec("UPDATE packages SET disabled=true WHERE token='gone-000'").Error)
	require.NoError(t, f.st.DB.Exec("INSERT INTO package_meta(package_id,hidden) SELECT id,true FROM packages WHERE token='hidden-000'").Error)
	require.NoError(t, f.st.DB.Exec("UPDATE url_notify_state SET cursor=(SELECT max(seq) FROM catalog_changes)").Error)
	f.web.publicStatus = 503
	_, err := f.run(context.Background())
	require.NoError(t, err, "temporary probe overload defers delivery without exhausting it")
	require.Empty(t, f.indexNow.requests)
	state, err := f.st.URLNotifyState(context.Background())
	require.NoError(t, err)
	require.Equal(t, "2026-10-18-paused-apps", state.IndexBackfill)
	f.web.publicStatus = 200
	finishFeed(t, f)
	for _, r := range f.indexNow.requests {
		for _, raw := range r["urlList"].([]any) {
			require.Contains(t, raw, "/apps/paused-000")
		}
	}
	calls := len(f.indexNow.requests)
	_, err = f.run(context.Background())
	require.NoError(t, err)
	require.Len(t, f.indexNow.requests, calls)
}

func TestLegacyPendingPurgesAreNotLostOnUpgrade(t *testing.T) {
	f := newFeed(t)
	require.NoError(t, f.st.DB.Exec(`UPDATE url_notify_state SET pending_purges='["opennavo.example/apps/legacy"]'`).Error)
	_, err := f.run(context.Background())
	require.NoError(t, err)
	require.Len(t, f.cloudflare.requests, 1)
	require.Equal(t, []any{"opennavo.example/apps/legacy"}, f.cloudflare.requests[0]["prefixes"])
	state, err := f.st.URLNotifyState(context.Background())
	require.NoError(t, err)
	require.Empty(t, state.PendingPurgePrefixes)
}
