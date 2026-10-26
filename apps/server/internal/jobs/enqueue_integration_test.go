//go:build integration

package jobs_test

import (
	"context"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/jobs"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestTriggerDeduplicatesAcrossOriginsAndRunnerWritesLedger(t *testing.T) {
	st := testutil.NewStore(t)
	ctx := context.Background()
	client := asynq.NewClientFromRedisClient(st.Redis)
	enqueuer := &jobs.Enqueuer{Client: client, Redis: st.Redis}
	id, err := enqueuer.Enqueue(ctx, "catalog:sync", jobs.Payload{}, jobs.Metadata{Trigger: "event"})
	require.NoError(t, err)
	_, err = enqueuer.Trigger(ctx, "catalog_sync", 1)
	var appError *domain.AppError
	require.ErrorAs(t, err, &appError)
	require.Equal(t, domain.CodeInvalidState, appError.Code)
	inspector := asynq.NewInspectorFromRedisClient(st.Redis)
	task, err := inspector.GetTaskInfo("critical", id)
	require.NoError(t, err)
	require.Equal(t, asynq.TaskStatePending, task.State)
	_, err = enqueuer.Enqueue(ctx, "changelog:resolve", jobs.Payload{PackageID: 1}, jobs.Metadata{Trigger: "event"})
	require.NoError(t, err)
	_, err = enqueuer.Enqueue(ctx, "changelog:resolve", jobs.Payload{PackageID: 2}, jobs.Metadata{Trigger: "event"})
	require.NoError(t, err)
	_, err = enqueuer.Enqueue(ctx, "changelog:resolve", jobs.Payload{PackageID: 1}, jobs.Metadata{Trigger: "event"})
	require.Error(t, err)
	_, err = enqueuer.Enqueue(ctx, "catalog:sync", jobs.Payload{}, jobs.Metadata{Trigger: "bad"})
	require.Error(t, err)
	_, err = enqueuer.Trigger(ctx, "bad", 1)
	require.Error(t, err)
	_, err = enqueuer.Trigger(ctx, "cleanup", 0)
	require.Error(t, err)
	mux := asynq.NewServeMux()
	mux.Handle("catalog:sync", jobs.WrapHandler(st, st.Redis, func(context.Context, jobs.Payload) (map[string]any, error) { return map[string]any{"inserted": 2}, nil }))
	worker := asynq.NewServerFromRedisClient(st.Redis, asynq.Config{Concurrency: 1, Queues: map[string]int{"critical": 1}, TaskCheckInterval: 10 * time.Millisecond, ShutdownTimeout: time.Second})
	require.NoError(t, worker.Start(mux))
	t.Cleanup(worker.Shutdown)
	var run struct {
		Status, Trigger string
		Inserted        int
		TriggeredBy     *int64
	}
	require.Eventually(t, func() bool {
		return st.DB.WithContext(ctx).Raw("SELECT status,trigger,triggered_by,(stats->>'inserted')::int AS inserted FROM sync_runs ORDER BY id DESC LIMIT 1").Scan(&run).Error == nil && run.Status == "succeeded"
	}, 10*time.Second, 20*time.Millisecond)
	require.Equal(t, "event", run.Trigger)
	require.Equal(t, 2, run.Inserted)
	require.NoError(t, st.DB.WithContext(ctx).Exec("INSERT INTO admin_users (user_name,password_hash) VALUES ('job-test','isolated-test-only')").Error)
	require.Eventually(t, func() bool { _, err := enqueuer.Trigger(ctx, "catalog_sync", 1); return err == nil }, 5*time.Second, 20*time.Millisecond)
	require.Eventually(t, func() bool {
		return st.DB.WithContext(ctx).Raw("SELECT status,trigger,triggered_by,(stats->>'inserted')::int AS inserted FROM sync_runs ORDER BY id DESC LIMIT 1").Scan(&run).Error == nil && run.Status == "succeeded" && run.Trigger == "manual"
	}, 10*time.Second, 20*time.Millisecond)
	require.NotNil(t, run.TriggeredBy)
	require.Equal(t, int64(1), *run.TriggeredBy)
}

func TestReleaseArchivedSchedulesUnblocksTheNextTick(t *testing.T) {
	st := testutil.NewStore(t)
	ctx := context.Background()
	enqueuer := &jobs.Enqueuer{Client: asynq.NewClientFromRedisClient(st.Redis), Redis: st.Redis}
	inspector := asynq.NewInspectorFromRedisClient(st.Redis)
	scheduled, err := enqueuer.Enqueue(ctx, "facts:sources", jobs.Payload{}, jobs.Metadata{Trigger: "schedule"})
	require.NoError(t, err)
	event, err := enqueuer.Enqueue(ctx, "changelog:resolve", jobs.Payload{PackageID: 9}, jobs.Metadata{Trigger: "event"})
	require.NoError(t, err)
	require.NoError(t, inspector.ArchiveTask("default", scheduled))
	require.NoError(t, inspector.ArchiveTask("changelog", event))
	// An archived run keeps its uniqueness lock, so the next tick is dropped.
	_, err = enqueuer.Enqueue(ctx, "facts:sources", jobs.Payload{}, jobs.Metadata{Trigger: "schedule"})
	var appError *domain.AppError
	require.ErrorAs(t, err, &appError)
	require.Equal(t, domain.CodeInvalidState, appError.Code)
	released, err := jobs.ReleaseArchivedSchedules(inspector)
	require.NoError(t, err)
	require.Equal(t, 1, released)
	_, err = enqueuer.Enqueue(ctx, "facts:sources", jobs.Payload{}, jobs.Metadata{Trigger: "schedule"})
	require.NoError(t, err)
	kept, err := inspector.GetTaskInfo("changelog", event)
	require.NoError(t, err)
	require.Equal(t, asynq.TaskStateArchived, kept.State)
}
