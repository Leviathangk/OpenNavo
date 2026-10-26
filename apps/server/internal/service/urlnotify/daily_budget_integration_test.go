//go:build integration

package urlnotify_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/service/urlnotify"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestDailyBudgetDefersBackfillWithoutSpendingAttempts(t *testing.T) {
	for _, phase := range []string{"probed", "pending", "invalidated"} {
		t.Run(phase, func(t *testing.T) { testDailyBudgetPriority(t, phase) })
	}
}

func testDailyBudgetPriority(t *testing.T, phase string) {
	st := testutil.NewFactsStore(t)
	ctx := context.Background()
	submitted := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/apps/updated" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.URL.Path == "/indexnow" {
			var body struct {
				URLs []string `json:"urlList"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			submitted = append(submitted, body.URLs...)
			w.WriteHeader(200)
			return
		}
		_, _ = w.Write([]byte(`{"build":"","removed":1,"settingsCleared":true}`))
	}))
	defer server.Close()
	cfg := config.Config{IndexNowDailyLimit: 1, JWTSecret: strings.Repeat("a", 32), WebBaseURL: server.URL, WebInternalURL: server.URL, WebInternalSecret: "fixture-secret"}
	key, err := cfg.EncryptSEOSecret("indexnow", "fixture-indexnow-key")
	require.NoError(t, err)
	require.NoError(t, st.DB.Exec("UPDATE seo_settings SET indexnow_key_ciphertext=?", key).Error)
	configure(t, st, func(s *store.SEOSettings) { s.IndexNow.Enabled = true })
	require.NoError(t, st.DB.Exec("UPDATE url_notify_state SET index_backfill='2026-10-18-paused-apps'").Error)
	now := time.Now().UTC().Truncate(time.Microsecond).Add(time.Hour)
	for _, reason := range []string{"index", "updated"} {
		require.NoError(t, store.QueueURLNotification(ctx, st.DB, store.URLNotification{URL: server.URL + "/apps/" + reason, Path: "/apps/" + reason, Locale: "en-US", Reason: reason, Announce: true}))
	}
	require.NoError(t, st.DB.Exec("UPDATE url_notify_outbox SET state='probed'").Error)
	require.NoError(t, st.DB.Exec("UPDATE url_notify_outbox SET state=? WHERE reason='updated'", phase).Error)
	run := func() {
		n := &urlnotify.Notifier{Store: st, Config: cfg, HTTP: server.Client(), Now: func() time.Time { return now }, Sleep: func(time.Duration) {}, IndexNowEndpoint: server.URL + "/indexnow"}
		_, err := n.Run(ctx)
		require.NoError(t, err)
	}
	run()
	require.Equal(t, []string{server.URL + "/apps/updated"}, submitted)
	var deferred struct {
		State         string
		Attempts      int
		NextAttemptAt time.Time
	}
	require.NoError(t, st.DB.Raw("SELECT state,attempts,next_attempt_at FROM url_notify_outbox WHERE reason='index'").Scan(&deferred).Error)
	require.Equal(t, "probed", deferred.State)
	require.Zero(t, deferred.Attempts)
	if phase != "probed" {
		require.Equal(t, now.Add(time.Minute), deferred.NextAttemptAt.UTC())
		now = now.Add(2 * time.Minute)
		run()
		require.NoError(t, st.DB.Raw("SELECT state,attempts,next_attempt_at FROM url_notify_outbox WHERE reason='index'").Scan(&deferred).Error)
	}
	require.Equal(t, time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC), deferred.NextAttemptAt.UTC())
	run()
	require.Len(t, submitted, 1)
	now = deferred.NextAttemptAt.Add(time.Second)
	run()
	require.Equal(t, []string{server.URL + "/apps/updated", server.URL + "/apps/index"}, submitted)
}
