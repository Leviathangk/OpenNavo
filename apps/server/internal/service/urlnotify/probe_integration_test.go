//go:build integration

package urlnotify

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestProbeRetryAfterCapIsPersisted(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	for _, status := range []int{429, 503, 500} {
		for _, header := range []string{"2147483647", "Fri, 31 Dec 9999 23:59:59 GMT"} {
			for _, configured := range []time.Duration{0, 45 * time.Second} {
				t.Run(fmt.Sprintf("%d/%s/%s", status, header, configured), func(t *testing.T) {
					require.NoError(t, st.DB.Exec("UPDATE url_notify_state SET probe_ready_at=NULL,probe_failures=0").Error)
					now := time.Now().UTC().Truncate(time.Microsecond)
					var requests atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						if requests.Add(1) == 1 {
							w.Header().Set("Retry-After", header)
							w.WriteHeader(status)
						}
					}))
					defer server.Close()
					makeNotifier := func() *Notifier {
						return &Notifier{Store: st, Config: config.Config{WebBaseURL: server.URL, IndexNowProbeMaxRetryAfter: configured}, HTTP: server.Client(), Now: func() time.Time { return now }, Sleep: func(time.Duration) {}}
					}
					row := store.URLNotificationRow{}
					row.URL, row.Announce = server.URL+"/apps/example", true
					require.Error(t, makeNotifier().probeNotification(ctx, row))
					due, err := st.URLProbeReadyAt(ctx, now, configured)
					require.NoError(t, err)
					cap := configured
					if cap == 0 {
						cap = 15 * time.Minute
					}
					require.Equal(t, now.Add(cap), due.UTC())
					var wait *probeWait
					require.ErrorAs(t, makeNotifier().probeNotification(ctx, row), &wait)
					require.Equal(t, int32(1), requests.Load(), "a new notifier must honor the persisted cooldown")
					now = due
					require.NoError(t, makeNotifier().probeNotification(ctx, row))
					require.Equal(t, int32(2), requests.Load(), "probing resumes when the cap expires")
				})
			}
		}
	}
}

func TestProbeRecoversFromLegacyCooldownAndOutboxAfterUpgrade(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	for _, configured := range []time.Duration{0, 45 * time.Second} {
		t.Run(configured.String(), func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Microsecond)
			cap := config.ProbeRetryAfterCap(configured)
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
			defer server.Close()
			require.NoError(t, st.DB.Exec("DELETE FROM url_notify_outbox").Error)
			require.NoError(t, store.QueueURLNotification(ctx, st.DB, store.URLNotification{URL: server.URL + "/articles/example", Path: "/articles/example", Locale: "en-US", Announce: true}))
			require.NoError(t, st.DB.Exec("UPDATE url_notify_outbox SET state='invalidated',next_attempt_at='9999-12-31',updated_at=?", now).Error)
			require.NoError(t, st.DB.Exec("UPDATE url_notify_state SET probe_ready_at='9999-12-31',probe_failures=7").Error)
			makeNotifier := func() *Notifier {
				return &Notifier{Store: st, Config: config.Config{WebBaseURL: server.URL, IndexNowProbeMaxRetryAfter: configured}, HTTP: server.Client(), Now: func() time.Time { return now }, Sleep: func(time.Duration) {}}
			}
			row := store.URLNotificationRow{URLNotification: store.URLNotification{URL: server.URL + "/articles/example", Announce: true}}
			var wait *probeWait
			require.ErrorAs(t, makeNotifier().probeNotification(ctx, row), &wait)
			require.Equal(t, now.Add(cap), wait.until.UTC())
			rows, err := st.ClaimURLNotifications(ctx, "upgrade", now, 10, configured)
			require.NoError(t, err)
			require.Empty(t, rows)
			due := now.Add(cap)
			now = now.Add(cap / 2)
			require.ErrorAs(t, makeNotifier().probeNotification(ctx, row), &wait)
			require.Equal(t, due, wait.until.UTC(), "reads after restart must not extend the repaired cooldown")
			rows, err = st.ClaimURLNotifications(ctx, "restart", now, 10, configured)
			require.NoError(t, err)
			require.Empty(t, rows)
			now = due
			rows, err = st.ClaimURLNotifications(ctx, "recovered", now, 10, configured)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.NoError(t, makeNotifier().probeNotification(ctx, rows[0]))
			require.Equal(t, int32(1), requests.Load())
			require.NoError(t, st.CompleteURLNotification(ctx, rows[0], "probed", nil, now))
		})
	}
}

func TestProbePacingAndDurableCooldown(t *testing.T) {
	st := testutil.NewFactsStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	requests := 0
	status := 503
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		require.Equal(t, "OpenNavo-Probe/1.0 IndexNow", r.UserAgent())
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(status)
	}))
	defer server.Close()
	waits := 0
	makeNotifier := func() *Notifier {
		return &Notifier{Store: st, Config: config.Config{WebBaseURL: server.URL}, HTTP: server.Client(), Now: func() time.Time { return now }, Sleep: func(d time.Duration) { require.Equal(t, 2*time.Second, d); waits++ }}
	}
	row := store.URLNotificationRow{}
	row.URL, row.Announce = server.URL+"/apps/example", true
	require.Error(t, makeNotifier().probeNotification(ctx, row))
	require.Equal(t, 1, requests)
	var wait *probeWait
	require.ErrorAs(t, makeNotifier().probeNotification(ctx, row), &wait)
	require.WithinDuration(t, now.Add(120*time.Second), wait.until, time.Millisecond)
	require.Equal(t, 1, requests)
	now = now.Add(121 * time.Second)
	status = 200
	require.NoError(t, makeNotifier().probeNotification(ctx, row))
	require.Equal(t, 2, requests)
	require.Equal(t, 2, waits)
	// After recovery a new overload starts from the initial backoff.
	require.NoError(t, st.RecordURLProbe(ctx, now, now, true, 0))
	due, err := st.URLProbeReadyAt(ctx, now, 0)
	require.NoError(t, err)
	require.WithinDuration(t, now.Add(30*time.Second), due, time.Millisecond)
	// A failed batch makes one request; other rows wait without spending attempts.
	now = now.Add(time.Hour)
	status = 503
	for _, path := range []string{"/apps/one", "/apps/two"} {
		require.NoError(t, store.QueueURLNotification(ctx, st.DB, store.URLNotification{URL: server.URL + path, Path: path, Locale: "en-US", Announce: true}))
	}
	var payload string
	require.NoError(t, st.DB.Raw("SELECT payload::text FROM job_outbox WHERE unique_key='cache:invalidate:url-notify'").Scan(&payload).Error)
	require.NotContains(t, payload, `"c:*"`)
	require.NotContains(t, payload, `"c:browse:*"`)
	require.Contains(t, payload, store.BrowseRefreshPattern)
	require.NoError(t, st.DB.Exec("UPDATE url_notify_outbox SET state='invalidated'").Error)
	n := makeNotifier()
	n.defaults()
	_, _, err = n.drainOutbox(ctx, "fixture-owner")
	require.NoError(t, err)
	require.Equal(t, 3, requests)
	var attempts []int
	require.NoError(t, st.DB.Raw("SELECT attempts FROM url_notify_outbox ORDER BY id").Scan(&attempts).Error)
	require.Equal(t, []int{0, 0}, attempts)

}

func TestProbeComparesIndexingIntentAndWaitsOutLoadShedding(t *testing.T) {
	st := testutil.NewFactsStore(t)
	ctx := context.Background()
	// PostgreSQL persists timestamps at microsecond precision; compare the same clock precision.
	now := time.Now().UTC().Truncate(time.Microsecond)
	var server *httptest.Server
	shed := 0
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Pages render fixed paths, never the request's own, so the fixture echoes no input.
		page := func(path, robots string) {
			_, _ = fmt.Fprintf(w, `<html><head><link rel="canonical" href="%s%s"><meta name="robots" content="%s"></head></html>`, server.URL, path, robots)
		}
		switch r.URL.Path {
		case "/articles/preview":
			// The @nuxtjs/robots default on indexable article pages.
			page("/articles/preview", "index, follow, max-image-preview:large, max-snippet:-1, max-video-preview:-1")
		case "/articles/header-noindex":
			w.Header().Set("X-Robots-Tag", "noindex")
			page("/articles/header-noindex", "index, follow")
		case "/articles/shed":
			shed++
			w.Header().Set("X-OpenNavo-Load-Shed", "mild")
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(http.StatusServiceUnavailable)
		case "/articles/limited":
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(http.StatusTooManyRequests)
		case "/articles/broken":
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer server.Close()
	n := &Notifier{Store: st, Config: config.Config{WebBaseURL: server.URL}, HTTP: server.Client(), Now: func() time.Time { return now }, Sleep: func(time.Duration) {}}
	resetCooldown := func() {
		require.NoError(t, st.DB.Exec("UPDATE url_notify_state SET probe_ready_at=NULL,probe_failures=0").Error)
	}
	probe := func(path string) error {
		resetCooldown()
		row := store.URLNotificationRow{}
		row.URL, row.Announce, row.Robots = server.URL+path, true, "index, follow"
		return n.probeNotification(ctx, row)
	}
	require.NoError(t, probe("/articles/preview"))
	require.ErrorContains(t, probe("/articles/header-noindex"), `robots "noindex, follow", want "index, follow"`)
	var wait *probeWait
	require.ErrorAs(t, probe("/articles/shed"), &wait)
	require.False(t, wait.until.Before(now.Add(120*time.Second)))
	require.ErrorAs(t, probe("/articles/limited"), &wait)
	require.False(t, wait.until.Before(now.Add(30*time.Second)))
	// Every 503 is temporary, even when an intermediary removed the shedding header.
	err := probe("/articles/broken")
	require.ErrorAs(t, err, &wait)
	require.False(t, wait.until.Before(now.Add(120*time.Second)))

	// Shedding is no delivery failure: the receipt keeps its retry budget and waits for the cooldown.
	resetCooldown()
	require.NoError(t, store.QueueURLNotification(ctx, st.DB, store.URLNotification{URL: server.URL + "/articles/shed", Path: "/articles/shed", Locale: "en-US", Announce: true, Robots: "index, follow"}))
	require.NoError(t, st.DB.Exec("UPDATE url_notify_outbox SET state='invalidated',next_attempt_at=?", now.Add(-time.Second)).Error)
	n.defaults()
	before := shed
	_, _, err = n.drainOutbox(ctx, "fixture-owner")
	require.NoError(t, err)
	require.Equal(t, before+1, shed)
	var receipt struct {
		Attempts      int
		LastError     string
		NextAttemptAt time.Time
	}
	require.NoError(t, st.DB.Raw("SELECT attempts,last_error,next_attempt_at FROM url_notify_outbox").Scan(&receipt).Error)
	require.Zero(t, receipt.Attempts)
	require.Empty(t, receipt.LastError)
	require.False(t, receipt.NextAttemptAt.Before(now.Add(120*time.Second)))
	queue, err := st.URLNotifyQueue(ctx, now)
	require.NoError(t, err)
	require.Equal(t, 1, queue.PendingAnnouncements)
	require.NotNil(t, queue.OldestPendingAt)
	require.Zero(t, queue.DeadAnnouncements)
	require.Equal(t, 1, queue.OverloadedProbes)
	require.NotNil(t, queue.ProbeCooldownUntil)
}
