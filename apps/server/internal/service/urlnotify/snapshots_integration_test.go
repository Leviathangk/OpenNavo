//go:build integration

package urlnotify_test

import (
	"context"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/service/urlnotify"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/stretchr/testify/require"
)

func TestSnapshotRevocationPurgesCDNWithoutHTMLEdgeCache(t *testing.T) {
	ctx := context.Background()
	f := newFeed(t)
	configure(t, f.st, func(settings *store.SEOSettings) {
		edgePurge(settings)
		settings.EdgeCache.Enabled = false
	})
	prefixes := []string{"cdn.example.test/snapshots/"}
	f.cloudflare.status, f.cloudflare.reply = 500, `{"success":false}`
	require.Error(t, f.notifier.PurgeSnapshots(ctx, prefixes))
	require.ElementsMatch(t, prefixes, queuedPurges(t, f.st))
	f.cloudflare.status, f.cloudflare.reply = 200, `{"success":true}`
	*f.now = f.now.Add(2 * time.Minute)
	require.NoError(t, f.notifier.PurgeSnapshots(ctx, prefixes))
	last := f.cloudflare.requests[len(f.cloudflare.requests)-1]
	require.Equal(t, []any{"cdn.example.test/snapshots/"}, last["prefixes"])
	require.Contains(t, f.cloudflare.paths[len(f.cloudflare.paths)-1], "/purge_cache")
}

// The deferred error must stay distinguishable with errors.Is: snapshot revocation treats only that
// case as a warning and everything else as a blocking failure (#88 AC-88-03/AC-88-09).
func TestSnapshotPurgeReportsDeferredWithItsCause(t *testing.T) {
	ctx := context.Background()
	f := newFeed(t)
	configure(t, f.st, func(settings *store.SEOSettings) {
		settings.Cloudflare.PurgeEnabled = false
	})
	err := f.notifier.PurgeSnapshots(ctx, []string{"cdn.example.test/snapshots/"})
	require.ErrorIs(t, err, urlnotify.ErrPurgeDeferred)
	require.Contains(t, err.Error(), "Cloudflare purge disabled")
	// The prefix is durably queued even though the purge cannot run now.
	require.ElementsMatch(t, []string{"cdn.example.test/snapshots/"}, queuedPurges(t, f.st))
}

// A queue write failure is a persistence failure, not a deferral: the caller must block instead of
// acknowledging the revocation (#88 AC-88-09).
func TestSnapshotPurgeQueueFailureIsNotDeferred(t *testing.T) {
	ctx := context.Background()
	f := newFeed(t)
	configure(t, f.st, edgePurge)
	require.NoError(t, f.st.DB.Exec("ALTER TABLE url_notify_state RENAME TO url_notify_state_broken").Error)
	t.Cleanup(func() {
		require.NoError(t, f.st.DB.Exec("ALTER TABLE IF EXISTS url_notify_state_broken RENAME TO url_notify_state").Error)
	})
	err := f.notifier.PurgeSnapshots(ctx, []string{"cdn.example.test/snapshots/"})
	require.Error(t, err)
	require.NotErrorIs(t, err, urlnotify.ErrPurgeDeferred)
	require.Empty(t, f.cloudflare.requests, "a queue failure must stop before the CDN request")
}
