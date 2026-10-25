//go:build integration

package main

import (
	"context"
	"testing"

	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestSEOReplayIsolatedAndRollsBack(t *testing.T) {
	// Since 8ebc5cb (direct agent editing) WriteTranslations writes at once: there is no proposal mode and no SEO
	// action to compensate, so the replay's proposal and compensation steps no longer match the system. CI used to
	// hide this failure; skip it explicitly until the replay is redesigned (ops/verify-hermes-mcp.py runs it too).
	t.Skip("the SEO replay predates direct translation writes (8ebc5cb) and needs a redesign")
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
