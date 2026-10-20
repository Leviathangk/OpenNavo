package management

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/hibiken/asynq"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/jobs"
	"github.com/opennavo/opennavo/server/internal/store"
)

func (s *Service) queues(ctx context.Context) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	inspector := asynq.NewInspectorFromRedisClient(s.Store.Redis)
	defer func() { _ = inspector.Close() }()
	names, err := inspector.Queues()
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, name := range names {
		q, err := inspector.GetQueueInfo(name)
		if err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"queue": name, "size": q.Size, "active": q.Active, "pending": q.Pending, "scheduled": q.Scheduled, "retry": q.Retry, "archived": q.Archived, "processedToday": q.Processed, "failedToday": q.Failed})
	}
	return out, nil
}
func (s *Service) trigger(ctx context.Context, op string, in Input) (any, error) {
	if s.Queue == nil {
		return nil, domain.Internal(nil)
	}
	taskType := ""
	payload := jobs.Payload{}
	switch op {
	case "ResyncPackage":
		taskType = "catalog:sync"
		// Casks from third-party taps are read by the tap sync, one package at a time.
		if in.ID > 0 {
			link, err := s.Store.LinkApp(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			if link != nil && link.Kind == "link" && !link.Removed {
				taskType, payload.PackageID = "links:sync", in.ID
				break
			}
			cask, err := s.Store.TapCask(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			if cask != nil {
				taskType, payload.PackageID = "taps:sync", in.ID
			}
		}
	case "TriggerJob":
		for _, d := range jobs.Definitions {
			if d.Cron != "" && strings.Replace(d.Type, ":", "_", 1) == in.JobType {
				taskType = d.Type
				break
			}
		}
	}
	if taskType == "" {
		return nil, domain.Validation()
	}
	meta := jobs.Metadata{Trigger: "manual", TriggeredBy: in.Actor.ID}
	encoded, err := json.Marshal(jobs.OutboxPayload{Payload: payload, Metadata: &meta})
	if err != nil {
		return nil, err
	}
	entity := "job"
	if in.ID > 0 {
		entity = "package"
	}
	result, err := s.write(ctx, in, op, entity, func(st *store.Store) (any, error) {
		if in.ID > 0 {
			if _, err := st.AdminPackage(ctx, in.ID); err != nil {
				return nil, err
			}
		}
		key := taskType + ":" + strconv.FormatInt(in.ID, 10)
		result := st.DB.WithContext(ctx).Exec("INSERT INTO job_outbox(unique_key,job_type,payload) VALUES(?,?,?::jsonb) ON CONFLICT DO NOTHING", key, taskType, string(encoded))
		if result.Error != nil {
			return nil, result.Error
		}
		if result.RowsAffected == 0 {
			return nil, fail(domain.CodeInvalidState)
		}
		return nil, nil
	})
	if err != nil {
		return nil, err
	}
	if boolean(in.Params, "dryrun") || agentTransaction(ctx) {
		return result, nil
	}
	if taskType == "catalog:sync" && op == "ResyncPackage" {
		if err := s.Store.Redis.Del(ctx, "etag:homebrew:cask", "etag:homebrew:formula").Err(); err != nil {
			return nil, err
		}
	}
	duplicate := false
	_, err = s.Store.DispatchOutbox(ctx, func(ctx context.Context, typ string, raw json.RawMessage) error {
		p, m, err := jobs.DecodeOutbox(raw)
		if err != nil {
			return err
		}
		_, err = s.Queue.Enqueue(ctx, typ, p, m)
		var app *domain.AppError
		if errors.As(err, &app) && app.Code == domain.CodeInvalidState {
			duplicate = true
			return nil
		}
		return jobs.OutboxDeliveryError(typ, err)
	})
	if err != nil {
		return nil, err
	}
	if duplicate {
		return nil, fail(domain.CodeInvalidState)
	}
	return nil, nil
}
func (s *Service) sources(ctx context.Context, in Input) (any, error) {
	if _, err := s.Store.AdminPackage(ctx, in.ID); err != nil {
		return nil, err
	}
	return s.Store.JSONRows(ctx, `SELECT jsonb_build_object('id',id,'type',type,'config',config,'priority',priority,'enabled',enabled,'resolvedBy',resolved_by,'lastFetchedAt',last_fetched_at,'lastStatus',last_status,'lastError',last_error,'failCount',fail_count,'nextFetchAt',next_fetch_at) AS data FROM changelog_sources WHERE package_id=? AND type='homebrew_commits' ORDER BY priority,id`, in.ID)
}
