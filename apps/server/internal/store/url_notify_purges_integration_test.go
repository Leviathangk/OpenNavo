//go:build integration

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

// The durable purge queue keeps each prefix once and in order, and collapses into the section purges beyond
// its limit.
func TestQueueURLPurgesDeduplicatesAndCollapses(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	queue := func() []string {
		t.Helper()
		state, err := st.URLNotifyState(ctx)
		require.NoError(t, err)
		return state.PendingPurgePrefixes
	}
	sections := []string{"site.example/apps", "site.example/articles"}
	require.Empty(t, queue())
	require.NoError(t, st.QueueURLPurges(ctx, []string{"site.example/apps/a", "site.example/apps/b", "site.example/apps/a", ""}, 4, sections))
	require.Equal(t, []string{"site.example/apps/a", "site.example/apps/b"}, queue())
	require.NoError(t, st.QueueURLPurges(ctx, []string{"site.example/apps/b", "site.example/ja/apps/a"}, 4, sections))
	require.Equal(t, []string{"site.example/apps/a", "site.example/apps/b", "site.example/ja/apps/a"}, queue())
	require.NoError(t, st.QueueURLPurges(ctx, nil, 4, sections))
	require.Len(t, queue(), 3)
	require.NoError(t, st.QueueURLPurges(ctx, []string{"site.example/apps/c", "site.example/apps/d"}, 4, sections))
	require.Equal(t, []string{"site.example/apps", "site.example/ja/apps/a"}, queue(), "only queued sections collapse; an unknown section retains its exact prefix")
	// Saving feed progress must not drop debt queued by a concurrent snapshot revocation.
	state, err := st.URLNotifyState(ctx)
	require.NoError(t, err)
	state.PendingPurgePrefixes = nil
	require.NoError(t, st.SaveURLNotifyState(ctx, state))
	require.Len(t, queue(), 2, "saving progress leaves the queue alone")
	require.NoError(t, st.DequeueURLPurges(ctx, []string{"site.example/apps", "site.example/ja/apps/a"}))
	require.Empty(t, queue())
	require.NoError(t, st.QueueURLPurges(ctx, []string{"site.example/zh-hant/apps/a", "site.example/zh-hant/apps/b"}, 1, []string{"site.example/apps", "site.example/zh/apps", "site.example/zh-hant/apps"}))
	require.Equal(t, []string{"site.example/zh-hant/apps"}, queue(), "overflow preserves the changed language")
}

// Snapshot debt is alarmed separately from page backlog: only a queued snapshot prefix moves the
// snapshot metric, and clearing it stops the clock.
func TestSnapshotPurgeDebtTracksOnlySnapshotPrefixes(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	require.NoError(t, st.QueueURLPurges(ctx, []string{"site.example/apps/a"}, 1000, nil))
	state, err := st.MetricState(ctx, now.Add(time.Minute))
	require.NoError(t, err)
	require.Zero(t, state.OldestPurgeDebtSeconds, "page backlog is not snapshot debt")
	require.NoError(t, st.QueueURLPurges(ctx, []string{"cdn.example.test/snapshots/catalog.json.gz"}, 1000, nil))
	state, err = st.MetricState(ctx, now.Add(time.Minute))
	require.NoError(t, err)
	require.Greater(t, state.OldestPurgeDebtSeconds, float64(0))
	require.NoError(t, st.DequeueURLPurges(ctx, []string{"cdn.example.test/snapshots/catalog.json.gz"}))
	state, err = st.MetricState(ctx, now.Add(2*time.Minute))
	require.NoError(t, err)
	require.Zero(t, state.OldestPurgeDebtSeconds)
}
