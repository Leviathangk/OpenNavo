package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/hibiken/asynq"
	"github.com/opennavo/opennavo/server/internal/changelog"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/redis/go-redis/v9"
	"github.com/robfig/cron/v3"
)

type Ledger interface {
	StartJobRun(context.Context, string, string, *int64) (int64, error)
	FinishJobRun(context.Context, int64, string, map[string]any, *string) error
}
type Handler func(context.Context, Payload) (map[string]any, error)
type Handlers map[string]Handler

func WrapHandler(ledger Ledger, rdb *redis.Client, handler Handler, defaults ...Metadata) asynq.HandlerFunc {
	return func(ctx context.Context, task *asynq.Task) error {
		var payload Payload
		if err := json.Unmarshal(task.Payload(), &payload); err != nil {
			return fmt.Errorf("invalid task payload: %w", asynq.SkipRetry)
		}
		metadata := Metadata{Trigger: "schedule"}
		if len(defaults) > 0 {
			metadata = defaults[0]
		}
		if id, ok := asynq.GetTaskID(ctx); ok && rdb != nil {
			data, err := rdb.Get(ctx, "job:meta:"+id).Bytes()
			if err != nil && !errors.Is(err, redis.Nil) {
				return errors.New("cannot load job metadata")
			}
			if err == nil && json.Unmarshal(data, &metadata) != nil {
				return fmt.Errorf("invalid job metadata: %w", asynq.SkipRetry)
			}
		}
		if metadata.Origin != nil {
			ctx = domain.WithOperation(ctx, metadata.Origin)
		}
		id, err := ledger.StartJobRun(ctx, task.Type(), metadata.Trigger, metadata.TriggeredBy)
		if err != nil {
			return fmt.Errorf("start job ledger: %w", err)
		}
		started := time.Now()
		stats, taskErr := execute(ctx, handler, payload)
		if stats == nil {
			stats = map[string]any{}
		}
		stats["durationMs"] = time.Since(started).Milliseconds()
		status := "succeeded"
		var errorMessage *string
		if taskErr != nil {
			status = "failed"
			if partial, ok := stats["partial"].(bool); ok && partial {
				status = "partial"
			}
			message := "job execution failed"
			category := failureCategory(taskErr)
			if category != "unknown" {
				message += " (" + category + ")"
			}
			_, httpStatus := changelog.Failure(taskErr)
			if httpStatus != 0 {
				message += fmt.Sprintf(" HTTP %d", httpStatus)
				stats["httpStatus"] = httpStatus
			}
			errorMessage = &message
			taskID, _ := asynq.GetTaskID(ctx)
			slog.ErrorContext(ctx, "job execution failed", "jobType", task.Type(), "taskId", taskID, "runId", id, "errorCategory", category, "httpStatus", httpStatus)
		}
		finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := ledger.FinishJobRun(finish, id, status, stats, errorMessage); err != nil {
			return fmt.Errorf("finish job ledger: %w", err)
		}
		if taskErr != nil {
			// Do not pass third-party responses or sensitive configuration to asynq logs or archives.
			if errors.Is(taskErr, asynq.SkipRetry) {
				return fmt.Errorf("%s: %w", *errorMessage, asynq.SkipRetry)
			}
			return errors.New(*errorMessage)
		}
		return nil
	}
}

func execute(ctx context.Context, handler Handler, payload Payload) (stats map[string]any, err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("job handler panic")
		}
	}()
	if handler == nil {
		return nil, fmt.Errorf("job handler not registered: %w", asynq.SkipRetry)
	}
	return handler(ctx, payload)
}

func RegisterSchedules(scheduler *asynq.Scheduler) error {
	for _, definition := range Definitions {
		if definition.Cron == "" {
			continue
		}
		task, options, err := NewTask(definition.Type, Payload{})
		if err != nil {
			return err
		}
		if _, err := scheduler.Register(definition.Cron, task, options...); err != nil {
			return fmt.Errorf("register %s: %w", definition.Type, err)
		}
	}
	return nil
}

func NextSchedule(taskType string, now time.Time, location *time.Location) (time.Time, error) {
	definition, err := DefinitionFor(taskType)
	if err != nil {
		return time.Time{}, err
	}
	if definition.Cron == "" {
		return time.Time{}, errors.New("job has no schedule")
	}
	schedule, err := cron.ParseStandard("CRON_TZ=" + location.String() + " " + definition.Cron)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse schedule: %w", err)
	}
	return schedule.Next(now), nil
}

type logAdapter struct{ logger *slog.Logger }

func (l logAdapter) Debug(args ...any) { l.logger.Debug(fmt.Sprint(args...)) }
func (l logAdapter) Info(args ...any)  { l.logger.Info(fmt.Sprint(args...)) }
func (l logAdapter) Warn(args ...any)  { l.logger.Warn(fmt.Sprint(args...)) }
func (l logAdapter) Error(args ...any) { l.logger.Error(fmt.Sprint(args...)) }
func (l logAdapter) Fatal(args ...any) { l.logger.Error(fmt.Sprint(args...)) }

// Build Run's handler registry before startup and never mutate it at runtime, avoiding races during job reads.
func Run(ctx context.Context, rdb *redis.Client, ledger Ledger, handlers Handlers, location *time.Location, logger *slog.Logger) error {
	rdb.AddHook(newDequeueBackoff(ctx))
	mux := asynq.NewServeMux()
	RegisterRetiredHandlers(mux)
	for _, definition := range Definitions {
		mux.Handle(definition.Type, WrapHandler(ledger, rdb, handlers[definition.Type]))
	}
	mainConfig, fetchConfig := MainConfig(), FetchConfig()
	mainConfig.Logger, fetchConfig.Logger = logAdapter{logger}, logAdapter{logger}
	mainConfig.BaseContext = func() context.Context { return ctx }
	fetchConfig.BaseContext = func() context.Context { return ctx }
	main := asynq.NewServerFromRedisClient(rdb, mainConfig)
	if err := main.Start(mux); err != nil {
		return fmt.Errorf("start main worker: %w", err)
	}
	defer main.Shutdown()
	crawler := asynq.NewServerFromRedisClient(rdb, fetchConfig)
	if err := crawler.Start(mux); err != nil {
		return fmt.Errorf("start changelog worker: %w", err)
	}
	defer crawler.Shutdown()
	scheduler := asynq.NewSchedulerFromRedisClient(rdb, &asynq.SchedulerOpts{Location: location, Logger: logAdapter{logger}, PostEnqueueFunc: func(_ *asynq.TaskInfo, err error) {
		if err != nil && !errors.Is(err, asynq.ErrDuplicateTask) {
			logger.Error("scheduled enqueue failed")
		}
	}})
	if released, err := ReleaseArchivedSchedules(asynq.NewInspectorFromRedisClient(rdb)); err != nil {
		logger.Warn("release archived scheduled runs failed")
	} else if released > 0 {
		logger.Info("released archived scheduled runs", "count", released)
	}
	if err := RegisterSchedules(scheduler); err != nil {
		return err
	}
	if err := scheduler.Start(); err != nil {
		return fmt.Errorf("start scheduler: %w", err)
	}
	defer scheduler.Shutdown()
	logger.Info("workers and scheduler ready", "mainConcurrency", 12, "changelogConcurrency", 4, "timezone", location.String())
	<-ctx.Done()
	return nil
}
