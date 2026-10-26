package urlnotify

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/stretchr/testify/require"
)

func TestAnnounceAllBatchesAfterAccepted(t *testing.T) {
	for _, codes := range [][]int{{202, 202}, {202, 200}, {200, 202}, {200, 200}, {202, 403}} {
		t.Run(fmt.Sprint(codes), func(t *testing.T) {
			urls := make([]string, 10002)
			for i := range urls {
				urls[i] = fmt.Sprintf("https://opennavo.example/apps/app-%d", i)
			}
			var batches [][]string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					URLs []string `json:"urlList"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				batches = append(batches, body.URLs)
				w.WriteHeader(codes[len(batches)-1])
			}))
			defer server.Close()
			n := &Notifier{
				Config: config.Config{WebBaseURL: "https://opennavo.example"},
				HTTP:   server.Client(), IndexNowEndpoint: server.URL, Sleep: func(time.Duration) {},
			}
			status, count := n.announceURLs(context.Background(), "fixture-indexnow-key", urls)
			require.Len(t, batches, 2)
			require.Len(t, batches[0], 10000)
			require.Equal(t, urls[10000:], batches[1])
			require.Equal(t, urls[:10000], batches[0])
			if codes[1] == 403 {
				require.Equal(t, 10000, count)
				require.Equal(t, "error: IndexNow returned 403", status)
			} else {
				require.Equal(t, 10002, count)
				if codes[0] == 202 || codes[1] == 202 {
					require.Equal(t, "accepted: 10002 URLs, key verification pending", status)
				} else {
					require.Equal(t, "ok: 10002 URLs", status)
				}
			}
		})
	}
}

func TestAnnounceFailureDoesNotRetryAgainstTheSameReservation(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls++; w.WriteHeader(http.StatusServiceUnavailable) }))
	defer server.Close()
	n := &Notifier{Config: config.Config{WebBaseURL: "https://site.example"}, HTTP: server.Client(), IndexNowEndpoint: server.URL, Sleep: func(time.Duration) { t.Fatal("IndexNow retries must use the durable outbox") }}
	status, count := n.announceURLs(context.Background(), "fixture-key", []string{"https://site.example/apps/fixture"})
	require.Equal(t, 1, calls)
	require.Zero(t, count)
	require.Equal(t, "error: IndexNow returned 503", status)
}
