//go:build integration

package store_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestIndexNowDailyBudgetPersistsAndResetsAtUTC(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 26, 23, 59, 0, 0, time.UTC)
	var total atomic.Int64
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			n, err := st.ReserveIndexNowBudget(ctx, now, 5, 2)
			require.NoError(t, err)
			total.Add(int64(n))
		})
	}
	wg.Wait()
	require.Equal(t, int64(5), total.Load())
	n, err := st.ReserveIndexNowBudget(ctx, now.In(time.FixedZone("east", 8*3600)), 5, 2)
	require.NoError(t, err)
	require.Zero(t, n)
	accepted, err := st.RecordIndexNowAccepted(ctx, now, 3)
	require.NoError(t, err)
	require.Equal(t, 3, accepted)
	n, err = st.ReserveIndexNowBudget(ctx, now.Add(time.Minute), 5, 10)
	require.NoError(t, err)
	require.Equal(t, 5, n)
}
