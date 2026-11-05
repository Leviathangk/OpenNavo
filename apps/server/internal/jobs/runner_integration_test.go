//go:build integration

package jobs_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/hibiken/asynq"
	"github.com/opennavo/opennavo/server/internal/jobs"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// A failed run used to persist only "job execution failed", which hid the record-not-found regression
// behind 991 identical rows. The ledger must now keep a sanitized cause without copying handler text.
func TestFailedRunPersistsSanitizedCause(t *testing.T) {
	st := testutil.NewStore(t)
	ctx := context.Background()
	const secret = "secret-upstream-token"

	err := jobs.WrapHandler(st, st.Redis, func(context.Context, jobs.Payload) (map[string]any, error) {
		return nil, fmt.Errorf("%s: %w", secret, gorm.ErrRecordNotFound)
	})(ctx, asynq.NewTask("changelog:schedule", []byte(`{}`)))
	require.Error(t, err)
	require.NotContains(t, err.Error(), secret)

	var run struct {
		Status string
		Error  *string
	}
	require.NoError(t, st.DB.WithContext(ctx).Raw("SELECT status,error FROM sync_runs ORDER BY id DESC LIMIT 1").Scan(&run).Error)
	require.Equal(t, "failed", run.Status)
	require.NotNil(t, run.Error)
	require.Contains(t, *run.Error, "record_not_found")
	require.Contains(t, *run.Error, "record not found")
	require.NotContains(t, *run.Error, secret)

	// An unclassified error keeps its type but never its message, which may contain upstream data.
	err = jobs.WrapHandler(st, st.Redis, func(context.Context, jobs.Payload) (map[string]any, error) {
		return nil, fmt.Errorf("%s: %w", secret, errors.New("upstream body "+secret))
	})(ctx, asynq.NewTask("catalog:sync", []byte(`{}`)))
	require.Error(t, err)
	require.NoError(t, st.DB.WithContext(ctx).Raw("SELECT status,error FROM sync_runs ORDER BY id DESC LIMIT 1").Scan(&run).Error)
	require.Equal(t, "failed", run.Status)
	require.NotNil(t, run.Error)
	require.Contains(t, *run.Error, "unknown")
	require.Contains(t, *run.Error, "*errors.errorString")
	require.NotContains(t, *run.Error, secret)
}
