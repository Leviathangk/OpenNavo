//go:build integration

package urlnotify_test

import (
	"context"
	"encoding/json"
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
		body, _ := io.ReadAll(req.Body)
		var payload map[string]any
		require.NoError(t, json.Unmarshal(body, &payload))
		r.Lock()
		r.requests = append(r.requests, payload)
		r.paths = append(r.paths, req.URL.Path)
		r.auth = append(r.auth, req.Header.Get("Authorization"))
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

func TestURLChangeFeedRefreshesPagesThenNotifies(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	data, err := seeds.Load()
	require.NoError(t, err)
	require.NoError(t, st.Seed(ctx, data, "", ""))
	web, cloudflare, indexNow := &recorder{status: 200, reply: `{"removed":3}`}, &recorder{status: 200, reply: `{"success":true}`}, &recorder{status: 200}
	webServer, cfServer, indexNowServer := web.server(t), cloudflare.server(t), indexNow.server(t)
	cfg := config.Config{JWTSecret: strings.Repeat("a", 32), WebBaseURL: "https://opennavo.example", WebInternalURL: webServer.URL, WebInternalSecret: strings.Repeat("s", 32)}
	key, err := cfg.EncryptSEOSecret("indexnow", "fixture-indexnow-key-0001")
	require.NoError(t, err)
	token, err := cfg.EncryptSEOSecret("cloudflare", "fixture-cloudflare-credential")
	require.NoError(t, err)
	settings := store.DefaultSEOSettings()
	settings.IndexNow.Enabled = true
	settings.EdgeCache.Enabled = true
	settings.Cloudflare.ZoneID, settings.Cloudflare.PurgeEnabled = strings.Repeat("0f", 16), true
	document, err := json.Marshal(settings)
	require.NoError(t, err)
	require.NoError(t, st.DB.Exec("UPDATE seo_settings SET settings=?::jsonb,indexnow_key_ciphertext=?,cloudflare_token_ciphertext=?", string(document), key, token).Error)

	notifier := &urlnotify.Notifier{Store: st, Config: cfg, Settle: time.Nanosecond, IndexNowEndpoint: indexNowServer.URL, CloudflareAPI: cfServer.URL, Sleep: func(time.Duration) {}}
	result, err := notifier.Run(ctx)
	require.NoError(t, err)
	require.Zero(t, result.Changes, "the feed starts at the current position")

	p, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"editor","name":["Editor"],"version":"2.0","desc":"Edits text"}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p}}))
	time.Sleep(10 * time.Millisecond)

	// A failing web endpoint keeps the cursor and skips Cloudflare and IndexNow.
	web.status = 500
	_, err = notifier.Run(ctx)
	require.Error(t, err)
	state, err := st.URLNotifyState(ctx)
	require.NoError(t, err)
	require.Equal(t, "page cache: web cache endpoint returned 500", state.LastError)
	require.Empty(t, cloudflare.requests)
	require.Empty(t, indexNow.requests)
	cursor := state.Cursor

	web.status = 200
	result, err = notifier.Run(ctx)
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

	require.Len(t, cloudflare.requests, 1, "24 URLs fit one 30-file purge")
	require.Len(t, cloudflare.requests[0]["files"], 24)
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
	require.Equal(t, "ok: 24 URLs", state.LastPurgeStatus)
	require.Equal(t, "ok: 6 URLs", state.LastIndexnowStatus)
	require.Empty(t, state.LastError)

	// Nothing new: no calls. Rate limits retry, and a pending key verification is reported apart.
	result, err = notifier.Run(ctx)
	require.NoError(t, err)
	require.Zero(t, result.Changes)
	require.Len(t, indexNow.requests, 1)
	p.Version = "2.1"
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p, Previous: &store.CatalogRow{ID: 1, Version: "2.0"}}}))
	time.Sleep(10 * time.Millisecond)
	indexNow.status = 202
	_, err = notifier.Run(ctx)
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
	_, err = notifier.Run(ctx)
	require.NoError(t, err)
	state, err = st.URLNotifyState(ctx)
	require.NoError(t, err)
	require.Equal(t, "skipped: probe /apps/editor returned 503", state.LastIndexnowStatus)
	require.Len(t, indexNow.requests, before)
}
