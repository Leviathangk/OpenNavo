//go:build integration

package main

import (
	"context"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestSEOReplayIsolatedAndRollsBack(t *testing.T) {
	st := testutil.NewSEOStore(t)
	result, err := replaySEO(context.Background(), st.DB)
	require.NoError(t, err)
	require.Equal(t, 0, result["paidCalls"])
	require.Len(t, result["checks"], 8)
	for _, table := range []string{"seo_actions", "seo_tasks", "seo_cost_receipts", "seo_control_receipts", "seo_gate_events"} {
		var n int64
		require.NoError(t, st.DB.Table(table).Count(&n).Error)
		require.Zero(t, n, table)
	}
}
