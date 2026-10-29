//go:build integration

package urlnotify_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/stretchr/testify/require"
)

func queuedPurges(t *testing.T, st *store.Store) []string {
	t.Helper()
	state, err := st.URLNotifyState(context.Background())
	require.NoError(t, err)
	return state.PendingPurgePrefixes
}

// describeCask gives an existing cask a new description, a change the feed purges and announces.
func describeCask(t *testing.T, st *store.Store, token, description string) {
	t.Helper()
	p, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"`+token+`","name":["App 0"],"version":"1.0","desc":"`+description+`"}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(context.Background(), []store.CatalogMutation{{Package: p}}))
	time.Sleep(10 * time.Millisecond)
}

// A Cloudflare outage longer than the rows' retry budget kills no row: their prefixes wait in the durable queue,
// once each, which every run retries, and the rows go on to their probe and IndexNow submission.
func TestFailedPurgesWaitInTheQueueAndKillNoRow(t *testing.T) {
	ctx := context.Background()
	f := newFeed(t)
	_, err := f.run(ctx)
	require.NoError(t, err)
	before := len(f.cloudflare.requests)
	addCasks(t, f.st, "outage", 1)
	f.cloudflare.status, f.cloudflare.reply = 500, `{"success":false}`
	_, err = f.run(ctx)
	require.ErrorContains(t, err, "cloudflare purge failed")
	var prefixes []string
	require.NoError(t, f.st.DB.Raw("SELECT DISTINCT purge_prefix FROM url_notify_outbox WHERE purge_prefix<>'' ORDER BY 1").Scan(&prefixes).Error)
	require.Len(t, prefixes, 20)
	require.ElementsMatch(t, prefixes, queuedPurges(t, f.st))
	require.Zero(t, outboxRemaining(t, f.st), "the rows were probed and announced")
	require.NotEmpty(t, f.indexNow.requests)

	rows := func() (dead, attempts int64) {
		t.Helper()
		require.NoError(t, f.st.DB.Raw("SELECT count(*) FILTER (WHERE state='dead'),COALESCE(sum(attempts),0) FROM url_notify_outbox").Row().Scan(&dead, &attempts))
		return dead, attempts
	}
	for run := 0; run < 13; run++ {
		if run == 6 {
			// A later change of the same app during the outage joins the queue without a purge of its own.
			describeCask(t, f.st, "outage-000", "Does more things")
		}
		_, err = f.run(ctx)
		require.NoError(t, err, run)
		require.ElementsMatch(t, prefixes, queuedPurges(t, f.st), "each prefix is queued once")
	}
	require.Len(t, f.cloudflare.requests, before+14, "one queued purge per run, nothing else")
	dead, attempts := rows()
	require.Zero(t, dead)
	require.Zero(t, attempts)
	require.Zero(t, outboxRemaining(t, f.st))

	// The next run that reaches Cloudflare empties the queue.
	f.cloudflare.status, f.cloudflare.reply = 200, `{"success":true}`
	_, err = f.run(ctx)
	require.NoError(t, err)
	require.Empty(t, queuedPurges(t, f.st))
	last := f.cloudflare.requests[len(f.cloudflare.requests)-1]["prefixes"].([]any)
	require.Len(t, last, 20)
}

// Without edge purging there is nothing to purge, and nothing is queued.
func TestDisabledPurgesQueueNothing(t *testing.T) {
	ctx := context.Background()
	f := newFeed(t)
	configure(t, f.st, func(s *store.SEOSettings) { s.IndexNow.Enabled = true })
	_, err := f.run(ctx)
	require.NoError(t, err)
	addCasks(t, f.st, "unpurged", 1)
	f.cloudflare.status = 500
	_, err = f.run(ctx)
	require.NoError(t, err)
	require.Empty(t, f.cloudflare.requests)
	require.Empty(t, queuedPurges(t, f.st))
	require.Zero(t, outboxRemaining(t, f.st))
	require.NotEmpty(t, f.indexNow.requests)
}
