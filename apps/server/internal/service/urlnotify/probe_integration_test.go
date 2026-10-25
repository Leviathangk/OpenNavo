//go:build integration

package urlnotify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

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
		return &Notifier{Store: st, Config: config.Config{WebBaseURL: server.URL}, HTTP: server.Client(), Now: func() time.Time { return now }, Sleep: func(d time.Duration) { require.Equal(t, time.Second, d); waits++ }}
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
	require.NoError(t, st.RecordURLProbe(ctx, now, now, true))
	due, err := st.URLProbeReadyAt(ctx)
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
	require.Error(t, err)
	require.Equal(t, 3, requests)
	var attempts []int
	require.NoError(t, st.DB.Raw("SELECT attempts FROM url_notify_outbox ORDER BY id").Scan(&attempts).Error)
	require.Equal(t, []int{1, 0}, attempts)

}
