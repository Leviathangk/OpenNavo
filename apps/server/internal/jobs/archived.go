package jobs

import (
	"errors"

	"github.com/hibiken/asynq"
)

// ReleaseArchivedSchedules deletes archived runs of scheduled jobs. Asynq keeps
// an archived task's uniqueness lock, which silently drops the following ticks
// of that schedule; the failure is already recorded in the job ledger and the
// next tick reruns the job. Archived event jobs are kept.
func ReleaseArchivedSchedules(inspector *asynq.Inspector) (int, error) {
	scheduled := map[string]bool{}
	queues := map[string]bool{}
	for _, definition := range Definitions {
		if definition.Cron != "" {
			scheduled[definition.Type] = true
			queues[definition.Queue] = true
		}
	}
	released := 0
	for queue := range queues {
		var stale []string
		for page := 1; ; page++ {
			tasks, err := inspector.ListArchivedTasks(queue, asynq.Page(page), asynq.PageSize(100))
			if errors.Is(err, asynq.ErrQueueNotFound) {
				break
			}
			if err != nil {
				return released, err
			}
			for _, task := range tasks {
				if scheduled[task.Type] {
					stale = append(stale, task.ID)
				}
			}
			if len(tasks) < 100 {
				break
			}
		}
		for _, id := range stale {
			err := inspector.DeleteTask(queue, id)
			if err == nil {
				released++
			} else if !errors.Is(err, asynq.ErrTaskNotFound) {
				return released, err
			}
		}
	}
	return released, nil
}
