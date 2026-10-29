//go:build integration

package urlnotify_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/service/urlnotify"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDurableOutboxFailuresRestartAndFencing(t *testing.T) {
	st := testutil.NewFactsStore(t)
	ctx := context.Background()
	var invalidateFail, probeFail, announceFail atomic.Bool
	var settingsFail atomic.Bool
	var invalidations, probes, announcements atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cache/invalidate":
			invalidations.Add(1)
			var request struct {
				Settings bool `json:"settings"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				w.WriteHeader(400)
				return
			}
			if request.Settings && settingsFail.Load() {
				_, _ = w.Write([]byte(`{"removed":1}`))
				return
			}
			if invalidateFail.Load() {
				select {
				case <-r.Context().Done():
				case <-time.After(300 * time.Millisecond):
				}
				return
			}
			_, _ = w.Write([]byte(`{"removed":1,"settingsCleared":true}`))
		case "/cache/build":
			_, _ = w.Write([]byte(`{"build":""}`))
		case "/indexnow":
			announcements.Add(1)
			if announceFail.Load() {
				w.WriteHeader(503)
				return
			}
			w.WriteHeader(200)
		default:
			probes.Add(1)
			if probeFail.Load() {
				w.WriteHeader(503)
				return
			}
			_, _ = fmt.Fprintf(w, `<html><head><link rel="canonical" href="http://%s%s"><meta name="robots" content="index, follow"></head></html>`, html.EscapeString(r.Host), html.EscapeString(r.URL.Path))
		}
	}))
	t.Cleanup(server.Close)
	cfg := config.Config{JWTSecret: strings.Repeat("a", 32), WebBaseURL: server.URL, WebInternalURL: server.URL, WebInternalSecret: "fixture-secret"}
	key, err := cfg.EncryptSEOSecret("indexnow", "fixture-indexnow-key")
	require.NoError(t, err)
	require.NoError(t, st.DB.Exec("UPDATE seo_settings SET indexnow_key_ciphertext=?", key).Error)
	configure(t, st, func(s *store.SEOSettings) { s.IndexNow.Enabled = true })
	require.NoError(t, st.DB.Exec("UPDATE url_notify_state SET index_backfill='2026-10-18-paused-apps'").Error)
	now := time.Now().Add(time.Hour)
	newNotifier := func() *urlnotify.Notifier {
		return &urlnotify.Notifier{Store: st, Config: cfg, HTTP: &http.Client{Timeout: 150 * time.Millisecond}, Now: func() time.Time { return now }, Sleep: func(time.Duration) {}, IndexNowEndpoint: server.URL + "/indexnow"}
	}
	note := store.URLNotification{URL: server.URL + "/apps/example", Path: "/apps/example", Locale: "en-US", Reason: "updated", Announce: true, Robots: "index, follow"}
	state := func() string {
		var v string
		require.NoError(t, st.DB.Raw("SELECT state FROM url_notify_outbox ORDER BY id DESC LIMIT 1").Scan(&v).Error)
		return v
	}

	t.Run("business rollback", func(t *testing.T) {
		err := st.DB.Transaction(func(tx *gorm.DB) error {
			if err := store.QueueURLNotification(ctx, tx, note); err != nil {
				return err
			}
			return errors.New("rollback")
		})
		require.Error(t, err)
		var count int64
		require.NoError(t, st.DB.Table("url_notify_outbox").Count(&count).Error)
		require.Zero(t, count)
		require.NoError(t, st.DB.Table("job_outbox").Count(&count).Error)
		require.Zero(t, count)
	})
	t.Run("feed rollback and cursor fencing", func(t *testing.T) {
		before, err := st.URLNotifyState(ctx)
		require.NoError(t, err)
		after := before
		after.Cursor = 42
		bad := note
		bad.Locale = "invalid"
		require.Error(t, st.QueueURLFeed(ctx, before, after, []store.URLNotification{note, bad}))
		current, err := st.URLNotifyState(ctx)
		require.NoError(t, err)
		require.Equal(t, before.Cursor, current.Cursor)
		require.NoError(t, st.QueueURLFeed(ctx, before, after, []store.URLNotification{note}))
		require.Error(t, st.QueueURLFeed(ctx, before, after, []store.URLNotification{note}))
	})
	t.Run("timeout probe failure restart and IndexNow retry", func(t *testing.T) {
		invalidateFail.Store(true)
		_, err := newNotifier().Run(ctx)
		require.Error(t, err)
		require.Equal(t, "pending", state())
		require.Zero(t, probes.Load())
		require.Zero(t, announcements.Load())
		invalidateFail.Store(false)
		probeFail.Store(true)
		now = now.Add(time.Hour)
		_, err = newNotifier().Run(ctx)
		require.NoError(t, err, "exhausted 503 probes are temporary and keep the delivery pending")
		require.Equal(t, "invalidated", state())
		var attempts int
		require.NoError(t, st.DB.Raw("SELECT attempts FROM url_notify_outbox ORDER BY id DESC LIMIT 1").Scan(&attempts).Error)
		require.Zero(t, attempts)
		require.Zero(t, announcements.Load())
		invalidateCount := invalidations.Load()
		probeFail.Store(false)
		announceFail.Store(true)
		now = now.Add(time.Hour)
		_, err = newNotifier().Run(ctx)
		require.Error(t, err)
		require.Equal(t, "probed", state())
		require.Equal(t, invalidateCount, invalidations.Load())
		require.EqualValues(t, 1, announcements.Load(), "one HTTP attempt per daily reservation")
		probeCount := probes.Load()
		announceFail.Store(false)
		now = now.Add(time.Hour)
		_, err = newNotifier().Run(ctx)
		require.NoError(t, err)
		require.Equal(t, "announced", state())
		require.Equal(t, probeCount, probes.Load())
		require.EqualValues(t, 2, announcements.Load())
		var reserved int
		require.NoError(t, st.DB.Raw("SELECT sum(reserved) FROM indexnow_daily_budget").Scan(&reserved).Error)
		require.Equal(t, 2, reserved, "the failed request and its durable retry each consume budget")
	})
	t.Run("disabled IndexNow retains delivery", func(t *testing.T) {
		configure(t, st, func(s *store.SEOSettings) { s.IndexNow.Enabled = false })
		require.NoError(t, st.DB.Transaction(func(tx *gorm.DB) error { return store.QueueURLNotification(ctx, tx, note) }))
		now = now.Add(time.Hour)
		_, err := newNotifier().Run(ctx)
		require.NoError(t, err)
		require.Equal(t, "probed", state())
		require.EqualValues(t, 2, announcements.Load())
		configure(t, st, func(s *store.SEOSettings) { s.IndexNow.Enabled = true })
		now = now.Add(time.Hour)
		_, err = newNotifier().Run(ctx)
		require.NoError(t, err)
		require.Equal(t, "announced", state())
	})
	t.Run("settings acknowledgement gates other pages", func(t *testing.T) {
		settingsFail.Store(true)
		root := store.URLNotification{URL: server.URL + "/", Path: "/", Locale: "en-US", Reason: "settings"}
		require.NoError(t, st.DB.Transaction(func(tx *gorm.DB) error {
			if err := store.QueueURLNotification(ctx, tx, note); err != nil {
				return err
			}
			return store.QueueURLNotification(ctx, tx, root)
		}))
		before := announcements.Load()
		now = now.Add(time.Hour)
		_, err := newNotifier().Run(ctx)
		require.ErrorContains(t, err, "settings cache was not cleared")
		require.Equal(t, before, announcements.Load())
		var appState string
		require.NoError(t, st.DB.Raw("SELECT state FROM url_notify_outbox WHERE url=? ORDER BY id DESC LIMIT 1", note.URL).Scan(&appState).Error)
		require.Equal(t, "pending", appState)
		settingsFail.Store(false)
		for range 2 {
			now = now.Add(time.Hour)
			_, err = newNotifier().Run(ctx)
			require.NoError(t, err)
		}
		require.Greater(t, announcements.Load(), before)
	})
	t.Run("old revision and expired owner cannot acknowledge", func(t *testing.T) {
		require.NoError(t, st.DB.Transaction(func(tx *gorm.DB) error { return store.QueueURLNotification(ctx, tx, note) }))
		rows, err := st.ClaimURLNotifications(ctx, "old", now, 1)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.NoError(t, st.DB.Transaction(func(tx *gorm.DB) error { return store.QueueURLNotification(ctx, tx, note) }))
		owned, err := st.RenewURLNotification(ctx, rows[0], now)
		require.NoError(t, err)
		require.False(t, owned)
		require.NoError(t, st.CompleteURLNotification(ctx, rows[0], "announced", nil, now))
		require.Equal(t, "pending", state())
		fresh, err := st.ClaimURLNotifications(ctx, "fresh", now, 1)
		require.NoError(t, err)
		require.Len(t, fresh, 1)
		require.Greater(t, fresh[0].Revision, rows[0].Revision)
		owned, err = st.RenewURLNotification(ctx, fresh[0], now.Add(3*time.Minute))
		require.NoError(t, err)
		require.False(t, owned)
		require.NoError(t, st.CompleteURLNotification(ctx, fresh[0], "announced", nil, now.Add(3*time.Minute)))
		require.Equal(t, "pending", state())
	})
}
