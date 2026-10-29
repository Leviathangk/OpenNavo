package urlnotify

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/stretchr/testify/require"
)

func TestProbeRetryDelay(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	require.Equal(t, 120*time.Second, probeRetryDelay("120", 0, now, 0))
	require.Equal(t, 120*time.Second, probeRetryDelay(now.Add(120*time.Second).Format(http.TimeFormat), 0, now, 0))
	require.Equal(t, 100*time.Millisecond, probeRetryDelay("0", 0, now, 0))
	for _, header := range []string{"2147483647", "Fri, 31 Dec 9999 23:59:59 GMT"} {
		require.Equal(t, 15*time.Minute, probeRetryDelay(header, 0, now, 0))
		require.Equal(t, 45*time.Second, probeRetryDelay(header, 0, now, 45*time.Second))
		require.Equal(t, 24*time.Hour, probeRetryDelay(header, 0, now, 48*time.Hour))
	}
	for attempt := range 3 {
		for _, header := range []string{"", "invalid", "-1", "-0", "+1", "-9999", "9999999999", now.Add(-time.Hour).Format(http.TimeFormat)} {
			delay := probeRetryDelay(header, attempt, now, 0)
			base := 500 * time.Millisecond * time.Duration(1<<attempt)
			require.GreaterOrEqual(t, delay, base)
			require.Less(t, delay, 2*base)
		}
	}
}

func TestPublicProbeBoundedRetries(t *testing.T) {
	for _, test := range []struct {
		name       string
		statuses   []int
		retryAfter string
		wantCalls  int
		wantStatus int
	}{
		{"recovers", []int{429, 503, 200}, "1", 3, 200},
		{"exhausted", []int{503}, "", 3, 0},
		{"long wait", []int{429}, "120", 1, 0},
		{"date wait", []int{503}, "Thu, 01 Jan 2026 00:02:00 GMT", 1, 0},
		{"huge wait", []int{429}, "2147483647", 1, 0},
		{"far future date", []int{503}, "Fri, 31 Dec 9999 23:59:59 GMT", 1, 0},
		{"missing", []int{404}, "", 1, 404},
		{"gone", []int{410}, "", 1, 410},
		{"other error", []int{500}, "", 1, 500},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "OpenNavo-Probe/1.0 IndexNow", r.UserAgent())
				status := test.statuses[min(calls, len(test.statuses)-1)]
				calls++
				w.Header().Set("Retry-After", test.retryAfter)
				w.WriteHeader(status)
				_, _ = w.Write([]byte("page body"))
			}))
			defer server.Close()
			now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			started := now
			var waits []time.Duration
			n := &Notifier{HTTP: server.Client(), Now: func() time.Time { return now }, Sleep: func(delay time.Duration) {
				waits = append(waits, delay)
				now = now.Add(delay)
			}}
			response, err := n.probeHTTP(context.Background(), server.URL)
			require.Equal(t, test.wantCalls, calls)
			require.Equal(t, 2*time.Second, waits[0])
			require.Less(t, now.Sub(started), probeBudget)
			if test.wantStatus == 0 {
				var wait *probeWait
				require.ErrorAs(t, err, &wait)
				require.True(t, wait.until.After(now))
				require.LessOrEqual(t, wait.until.Sub(now), 15*time.Minute)
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.wantStatus, response.StatusCode)
			body, err := io.ReadAll(response.Body)
			require.NoError(t, err, "returning a successful response must not cancel its body")
			require.Equal(t, "page body", string(body))
			require.NoError(t, response.Body.Close())
		})
	}
}

func TestProbeHonorsTotalBudgetAndCancellation(t *testing.T) {
	now := time.Now()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Retry-After", "5")
		w.WriteHeader(503)
	}))
	defer server.Close()
	n := &Notifier{Config: config.Config{IndexNowProbeInterval: 3 * time.Second}, HTTP: server.Client(), Now: func() time.Time { return now }, Sleep: func(delay time.Duration) { now = now.Add(delay) }}
	response, err := n.probeHTTP(context.Background(), server.URL)
	if response != nil {
		require.NoError(t, response.Body.Close())
	}
	var wait *probeWait
	require.ErrorAs(t, err, &wait)
	require.Equal(t, 2, calls, "third request would exceed the ten-second total budget")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	n.ProbeSleep = n.probePause
	response, err = n.probeHTTP(ctx, server.URL)
	if response != nil {
		require.NoError(t, response.Body.Close())
	}
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 2, calls)
}
