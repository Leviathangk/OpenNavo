//go:build integration

package store_test

import (
	"context"
	"testing"

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
	// The feed's own drain still owns the queue: saving its state replaces it.
	state, err := st.URLNotifyState(ctx)
	require.NoError(t, err)
	state.PendingPurgePrefixes = []string{}
	require.NoError(t, st.SaveURLNotifyState(ctx, state))
	require.Empty(t, queue())
	require.NoError(t, st.QueueURLPurges(ctx, []string{"site.example/zh-hant/apps/a", "site.example/zh-hant/apps/b"}, 1, []string{"site.example/apps", "site.example/zh/apps", "site.example/zh-hant/apps"}))
	require.Equal(t, []string{"site.example/zh-hant/apps"}, queue(), "overflow preserves the changed language")
}
