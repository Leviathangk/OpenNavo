//go:build integration

package store_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/opennavo/opennavo/server/internal/jobs"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestDeferredOutboxRetainsPayloadAndDoesNotBlockLaterBatches(t *testing.T) {
	st := testutil.NewStore(t)
	ctx := context.Background()
	for i := 0; i < 101; i++ {
		require.NoError(t, st.DB.Exec("INSERT INTO job_outbox(job_type,unique_key,payload) VALUES('analytics:sync',?,'{\"sourceHash\":\"same\",\"metadata\":{\"trigger\":\"manual\"}}')", fmt.Sprint(i)).Error)
	}
	require.NoError(t, st.DB.Exec("INSERT INTO job_outbox(job_type,unique_key,payload) VALUES('catalog:sync','unrelated','{}')").Error)
	require.NoError(t, st.DB.Exec("UPDATE job_outbox SET available_at=clock_timestamp()-interval '1 minute'").Error)
	calls := 0
	delivered, err := st.DispatchOutbox(ctx, func(_ context.Context, kind string, raw json.RawMessage) error {
		calls++
		if kind == "analytics:sync" {
			require.JSONEq(t, `{"sourceHash":"same","metadata":{"trigger":"manual"}}`, string(raw))
			return jobs.ErrOutboxDeferred
		}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, delivered)
	require.Equal(t, 102, calls)
	var count int64
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM job_outbox WHERE job_type='analytics:sync'").Scan(&count).Error)
	require.EqualValues(t, 101, count)
	require.NoError(t, st.DB.Exec("UPDATE job_outbox SET available_at=clock_timestamp()-interval '1 minute'").Error)
	delivered, err = st.DispatchOutbox(ctx, func(context.Context, string, json.RawMessage) error { return nil })
	require.NoError(t, err)
	require.Equal(t, 101, delivered)
}

func TestUpdateFeedVerificationOutboxReachesWorker(t *testing.T) {
	st := testutil.NewStore(t)
	ctx := context.Background()
	require.NoError(t, st.DB.Exec(`INSERT INTO job_outbox(job_type,unique_key,payload) VALUES('update-feeds:verify','update-feeds:verify:0','{"metadata":{"trigger":"manual"}}')`).Error)
	require.NoError(t, st.DB.Exec("UPDATE job_outbox SET available_at=clock_timestamp()-interval '1 minute'").Error)
	delivered, err := st.DispatchOutbox(ctx, func(_ context.Context, kind string, raw json.RawMessage) error {
		require.Equal(t, "update-feeds:verify", kind)
		_, metadata, err := jobs.DecodeOutbox(raw)
		require.NoError(t, err)
		require.Equal(t, "manual", metadata.Trigger)
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, delivered)
	var remaining int64
	require.NoError(t, st.DB.Table("job_outbox").Where("job_type=?", "update-feeds:verify").Count(&remaining).Error)
	require.Zero(t, remaining)
}
