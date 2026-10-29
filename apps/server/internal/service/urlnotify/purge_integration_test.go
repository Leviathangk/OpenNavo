//go:build integration

package urlnotify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestPurgeCooldownSurvivesRestartWithoutExhaustingAttempts(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	var calls atomic.Int32
	var throttled atomic.Bool
	throttled.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cache/invalidate" {
			_, _ = w.Write([]byte(`{"removed":1}`))
			return
		}
		calls.Add(1)
		if throttled.Load() {
			w.Header().Set("Retry-After", "180")
			w.WriteHeader(429)
			return
		}
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	t.Cleanup(server.Close)
	cfg := config.Config{JWTSecret: strings.Repeat("a", 32), WebInternalURL: server.URL, WebInternalSecret: "fixture-secret"}
	secret, err := cfg.EncryptSEOSecret("cloudflare", "fixture-cloudflare-token")
	require.NoError(t, err)
	settings := store.DefaultSEOSettings()
	settings.EdgeCache.Enabled = true
	settings.Cloudflare.ZoneID, settings.Cloudflare.PurgeEnabled = strings.Repeat("0f", 16), true
	raw, err := json.Marshal(settings)
	require.NoError(t, err)
	require.NoError(t, st.DB.Exec("UPDATE seo_settings SET settings=?::jsonb,cloudflare_token_ciphertext=?", string(raw), secret).Error)
	now := time.Now().UTC().Add(time.Minute).Truncate(time.Microsecond)
	note := store.URLNotification{URL: "https://site.example/apps/fixture", Path: "/apps/fixture", Locale: "en-US", PurgePrefix: "site.example/apps/fixture"}
	require.NoError(t, store.QueueURLNotification(ctx, st.DB, note))
	require.NoError(t, st.DB.Exec("UPDATE url_notify_outbox SET attempts=11").Error)
	allowed, err := st.ReserveURLPurge(ctx, now, 12*time.Second)
	require.NoError(t, err)
	require.True(t, allowed)
	run := func() {
		// A fresh notifier/store on every call exercises persistent scheduling.
		n := &Notifier{Store: &store.Store{DB: st.DB}, Config: cfg, CloudflareAPI: server.URL, HTTP: server.Client(), Now: func() time.Time { return now }}
		n.defaults()
		_, _, err := n.drainOutbox(ctx, "fixture-owner")
		require.NoError(t, err)
	}
	checkPending := func(due time.Time) {
		var row struct {
			State         string
			Attempts      int
			NextAttemptAt time.Time
			LeaseToken    *string
		}
		require.NoError(t, st.DB.Table("url_notify_outbox").First(&row).Error)
		require.Equal(t, "pending", row.State)
		require.Equal(t, 11, row.Attempts)
		require.WithinDuration(t, due, row.NextAttemptAt, time.Microsecond)
		require.Nil(t, row.LeaseToken)
	}
	run()
	require.Zero(t, calls.Load(), "local capacity wait must not reach Cloudflare")
	checkPending(now.Add(12 * time.Second))
	now = now.Add(12 * time.Second)
	run()
	require.EqualValues(t, 1, calls.Load())
	due := now.Add(180 * time.Second)
	checkPending(due)
	ready, err := st.URLPurgeReadyAt(ctx, now, 0)
	require.NoError(t, err)
	require.Equal(t, due.UnixMicro(), ready.UnixMicro())
	// Even a newly queued URL must honor another job's persisted cooldown.
	note.URL, note.Path, note.PurgePrefix = "https://site.example/apps/next", "/apps/next", "site.example/apps/next"
	require.NoError(t, store.QueueURLNotification(ctx, st.DB, note))
	now = due.Add(-time.Second)
	run()
	require.EqualValues(t, 1, calls.Load())
	throttled.Store(false)
	now = due
	run()
	require.EqualValues(t, 2, calls.Load())
	var unfinished int64
	require.NoError(t, st.DB.Table("url_notify_outbox").Where("state<>'announced' OR attempts<>0").Count(&unfinished).Error)
	require.Zero(t, unfinished)
}

func TestDependenciesOutboxSuppressesObsoleteAnnouncement(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Add(time.Minute)
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash,desc_en)
 VALUES(941,'cask','empty-dependencies','empty-dependencies','homebrew/cask','Fixture','1','1','{}','fixture','Useful fixture')`).Error)
	n := &Notifier{Store: st, Batch: 10, Now: func() time.Time { return now }}
	for _, state := range []string{"invalidated", "probed"} {
		note := store.URLNotification{URL: "https://site.example/apps/empty-dependencies/dependencies", Path: "/apps/empty-dependencies/dependencies", Locale: "en-US", Announce: true, Robots: "index, follow"}
		require.NoError(t, store.QueueURLNotification(ctx, st.DB, note))
		require.NoError(t, st.DB.Exec("UPDATE url_notify_outbox SET state=? WHERE state='pending'", state).Error)
		// HTTP is intentionally unset: obsolete jobs must finish before any probe or submission.
		_, announced, err := n.drainOutbox(ctx, "fixture-owner")
		require.NoError(t, err)
		require.Zero(t, announced)
		var row store.URLNotificationRow
		require.NoError(t, st.DB.Raw("SELECT * FROM url_notify_outbox ORDER BY id DESC LIMIT 1").Scan(&row).Error)
		require.Equal(t, "announced", row.State)
		require.False(t, row.Announce)
		require.Equal(t, "noindex, follow", row.Robots)
		require.Equal(t, "/apps/empty-dependencies", row.CanonicalPath)
	}
}
