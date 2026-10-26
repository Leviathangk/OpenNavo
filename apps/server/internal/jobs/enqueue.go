package jobs

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/redis/go-redis/v9"
)

type Enqueuer struct {
	Client *asynq.Client
	Redis  *redis.Client
}

func (e *Enqueuer) Enqueue(ctx context.Context, taskType string, payload Payload, metadata Metadata) (string, error) {
	if metadata.Trigger != "manual" && metadata.Trigger != "event" && metadata.Trigger != "schedule" {
		return "", errors.New("invalid job trigger")
	}
	// Attribution metadata must not change Asynq payload deduplication keys, or identical-source retranslations could run concurrently.
	if payload.Origin != nil {
		metadata.Origin = payload.Origin
		payload.Origin = nil
	}
	task, options, err := NewTask(taskType, payload)
	if err != nil {
		return "", err
	}
	id := rand.Text()
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return "", fmt.Errorf("encode job metadata: %w", err)
	}
	// Store initiator metadata separately and keep payloads stable so manual, event and scheduled jobs share a deduplication key.
	if err := e.Redis.Set(ctx, "job:meta:"+id, encoded, 7*24*time.Hour).Err(); err != nil {
		return "", fmt.Errorf("save job metadata: %w", err)
	}
	_, err = e.Client.EnqueueContext(ctx, task, append(options, asynq.TaskID(id))...)
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = e.Redis.Del(cleanup, "job:meta:"+id).Err()
		if errors.Is(err, asynq.ErrDuplicateTask) {
			return "", &domain.AppError{Code: domain.CodeInvalidState, HTTPStatus: 409}
		}
		return "", fmt.Errorf("enqueue job: %w", err)
	}
	return id, nil
}

func (e *Enqueuer) Trigger(ctx context.Context, externalType string, userID int64) (string, error) {
	if userID <= 0 {
		return "", domain.Rejected("actor_required")
	}
	for _, definition := range Definitions {
		if definition.Cron == "" {
			continue
		}
		name := definition.Type
		for i := range name {
			if name[i] == ':' {
				name = name[:i] + "_" + name[i+1:]
				break
			}
		}
		if name == externalType {
			return e.Enqueue(ctx, definition.Type, Payload{}, Metadata{Trigger: "manual", TriggeredBy: &userID})
		}
	}
	return "", domain.RejectedDetail("job_type_invalid", externalType)
}
