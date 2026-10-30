//go:build integration

package urlnotify_test

import (
	"context"
	"testing"
	"time"

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
