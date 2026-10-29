package jobs

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/robfig/cron/v3"
)

type Definition struct {
	Type, Queue, Cron string
	Retries           int
	Ref               string
}

var Definitions = []Definition{
	{Type: "seo:pages", Queue: "default", Cron: "*/2 * * * *", Retries: 3},
	{Type: "catalog:sync", Queue: "critical", Cron: "*/15 * * * *", Retries: 3},
	{Type: "analytics:sync", Queue: "default", Cron: "0 3 * * *", Retries: 5},
	{Type: "snapshot:build", Queue: "critical", Cron: "5 * * * *", Retries: 3},
	{Type: "github-release:snapshot", Queue: "default", Cron: "10 * * * *", Retries: 3},

	{Type: "changelog:schedule", Queue: "default", Cron: "*/10 * * * *", Retries: 3},
	{Type: "changelog:resolve", Queue: "changelog", Retries: 3, Ref: "package"},
	{Type: "changelog:fetch", Queue: "changelog", Retries: 3, Ref: "source"},
	{Type: "assets:download-size", Queue: "default", Cron: "0 5 * * *", Retries: 2, Ref: "package"},
	{Type: "cleanup", Queue: "default", Cron: "0 4 * * *", Retries: 1},
	{Type: "facts:sources", Queue: "default", Cron: "35 * * * *", Retries: 1},
	{Type: "update-feeds:verify", Queue: "default", Cron: "15 2 * * *", Retries: 1},
	{Type: "links:sync", Queue: "default", Cron: "40 * * * *", Retries: 1, Ref: "package"},
	{Type: "taps:sync", Queue: "default", Cron: "20,50 * * * *", Retries: 1, Ref: "package"},
}

type Payload struct {
	Origin     *domain.Operation `json:"origin,omitempty"`
	Content    *content.Ref      `json:"content,omitempty"`
	SourceHash string            `json:"sourceHash,omitempty"`
	PackageID  int64             `json:"packageId,omitempty"`
	ReleaseID  int64             `json:"releaseId,omitempty"`
	SourceID   int64             `json:"sourceId,omitempty"`
}
type Metadata struct {
	Origin      *domain.Operation `json:"origin,omitempty"`
	Trigger     string            `json:"trigger"`
	TriggeredBy *int64            `json:"triggeredBy,omitempty"`
}

func DefinitionFor(taskType string) (Definition, error) {
	for _, definition := range Definitions {
		if definition.Type == taskType {
			return definition, nil
		}
	}
	return Definition{}, errors.New("unknown job type")
}
func (d Definition) Key(payload Payload) string {
	if d.Ref == "content" && payload.Content != nil {
		return d.Type + ":" + payload.Content.Entity + ":" + payload.Content.Key() + ":" + payload.SourceHash
	}
	var id int64
	switch d.Ref {
	case "package":
		id = payload.PackageID
	case "release":
		id = payload.ReleaseID
	case "source":
		id = payload.SourceID
	}
	if id > 0 {
		return fmt.Sprintf("%s:%d", d.Type, id)
	}
	return d.Type
}
func NewTask(taskType string, payload Payload) (*asynq.Task, []asynq.Option, error) {
	definition, err := DefinitionFor(taskType)
	if err != nil {
		return nil, nil, err
	}
	if definition.Ref == "content" {
		if payload.Content == nil || !payload.Content.Valid() || len(payload.SourceHash) != 64 {
			return nil, nil, errors.New("invalid content reference")
		}
	} else if payload.Content != nil || payload.SourceHash != "" {
		return nil, nil, errors.New("unexpected content reference")
	}
	if payload.PackageID < 0 || payload.ReleaseID < 0 || payload.SourceID < 0 {
		return nil, nil, errors.New("invalid job reference")
	}
	if (definition.Ref != "package" && payload.PackageID != 0) || (definition.Ref != "release" && payload.ReleaseID != 0) || (definition.Ref != "source" && payload.SourceID != 0) {
		return nil, nil, errors.New("unexpected job reference")
	}
	if definition.Cron == "" && ((definition.Ref == "package" && payload.PackageID == 0) || (definition.Ref == "release" && payload.ReleaseID == 0) || (definition.Ref == "source" && payload.SourceID == 0)) {
		return nil, nil, errors.New("job reference is required")
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, fmt.Errorf("encode job payload: %w", err)
	}
	options := []asynq.Option{asynq.Queue(definition.Queue), asynq.MaxRetry(definition.Retries), asynq.Unique(uniqueTTL(definition)), asynq.Timeout(20 * time.Minute)}
	return asynq.NewTask(taskType, encoded), options, nil
}

// Asynq archives a failed task with its uniqueness lock still held. A day-long
// lock would silence a five-minute schedule for a day after one failure, so a
// scheduled job holds it for about one period: at least the run timeout and
// never a full day.
func uniqueTTL(definition Definition) time.Duration {
	const shortest, longest = 20 * time.Minute, 23 * time.Hour
	if definition.Cron == "" {
		return 24 * time.Hour
	}
	schedule, err := cron.ParseStandard(definition.Cron)
	if err != nil {
		return longest
	}
	first := schedule.Next(time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC))
	return min(max(schedule.Next(first).Sub(first)-time.Minute, shortest), longest)
}

var MainQueues = map[string]int{"critical": 6, "default": 3}

func MainConfig() asynq.Config {
	return asynq.Config{Concurrency: 12, Queues: MainQueues, ShutdownTimeout: 30 * time.Second}
}
func FetchConfig() asynq.Config {
	return asynq.Config{Concurrency: 4, Queues: map[string]int{"changelog": 1}, ShutdownTimeout: 30 * time.Second}
}
