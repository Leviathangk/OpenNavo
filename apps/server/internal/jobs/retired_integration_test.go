//go:build integration

package jobs_test

import (
	"context"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/opennavo/opennavo/server/internal/jobs"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestRetiredBacklogAcknowledgedWithoutLedgerOrLLM(t *testing.T) {
	st := testutil.NewStore(t)
	ctx := context.Background()
	client := asynq.NewClientFromRedisClient(st.Redis)
	inspector := asynq.NewInspectorFromRedisClient(st.Redis)
	var taskIDs []string
	for _, kind := range []string{"enrich:package", "enrich:schedule", "enrich:old-subtype", "translate:release", "translate:schedule", "assets:icons", "seo:gsc", "seo:inspect", "seo:bing", "seo:audit", "seo:opportunities", "seo:evaluate", "seo:experiments"} {
		info, err := client.EnqueueContext(ctx, asynq.NewTask(kind, []byte("legacy invalid payload")), asynq.Queue("llm"), asynq.Retention(time.Hour))
		require.NoError(t, err)
		taskIDs = append(taskIDs, info.ID)
	}
	mux := asynq.NewServeMux()
	jobs.RegisterRetiredHandlers(mux)
	worker := asynq.NewServerFromRedisClient(st.Redis, asynq.Config{Concurrency: 2, Queues: map[string]int{"llm": 1}, ShutdownTimeout: time.Second})
	require.NoError(t, worker.Start(mux))
	defer worker.Shutdown()
	require.Eventually(t, func() bool {
		for _, id := range taskIDs {
			info, err := inspector.GetTaskInfo("llm", id)
			if err != nil || info.State != asynq.TaskStateCompleted || info.Retried != 0 {
				return false
			}
		}
		return true
	}, 10*time.Second, 20*time.Millisecond)
	var count int64
	require.NoError(t, st.DB.Table("sync_runs").Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, st.DB.Table("translation_logs").Count(&count).Error)
	require.Zero(t, count)
	// Redis scheduler entries are heartbeat snapshots, not a recovery source; new instances register only current Definitions.
	scheduler := asynq.NewSchedulerFromRedisClient(st.Redis, &asynq.SchedulerOpts{Location: time.UTC, HeartbeatInterval: 50 * time.Millisecond, Logger: quietScheduler{}})
	require.NoError(t, jobs.RegisterSchedules(scheduler))
	require.NoError(t, scheduler.Start())
	defer scheduler.Shutdown()
	require.Eventually(t, func() bool {
		entries, err := inspector.SchedulerEntries()
		if err != nil || len(entries) == 0 {
			return false
		}
		for _, e := range entries {
			_, err := jobs.DefinitionFor(e.Task.Type())
			if err != nil {
				return false
			}
		}
		return true
	}, 10*time.Second, 20*time.Millisecond)
}

type quietScheduler struct{}

func (q quietScheduler) Debug(...any) {}
func (q quietScheduler) Info(...any)  {}
func (q quietScheduler) Warn(...any)  {}
func (q quietScheduler) Error(...any) {}
func (q quietScheduler) Fatal(...any) {}
