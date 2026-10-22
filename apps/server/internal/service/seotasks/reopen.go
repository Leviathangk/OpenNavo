package seotasks

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/store"
	"gorm.io/gorm"
)

// StateError reports why a task cannot make the requested transition. Reason is
// a short snake_case rule the caller can act on.
type StateError struct{ Reason string }

func (e *StateError) Error() string { return "SEO task state: " + e.Reason }

// The cooldown prevents retry loops without permanently exhausting an agent's
// ability to recover a corrected task: it starts at an hour and doubles with
// every reopen, up to a day.
const (
	AgentReopenCooldown    = time.Hour
	AgentReopenCooldownMax = 24 * time.Hour
)

// AgentReopenWait is how long after its last reopen an agent waits before
// reopening a task already reopened `reopens` times: 1 h, 2 h, 4 h ... 24 h.
func AgentReopenWait(reopens int) time.Duration {
	wait := AgentReopenCooldown
	for i := 1; i < reopens && wait < AgentReopenCooldownMax; i++ {
		wait *= 2
	}
	return min(wait, AgentReopenCooldownMax)
}

// Reopen returns a quarantined or skipped task to the queue once its cause has
// been fixed. People may always reopen; agents wait AgentReopenWait after the last reopen. A translation whose source changed since then is
// superseded by the task for the current source and is not reopened.
func (s *Service) Reopen(ctx context.Context, id int64, owner, reason string, person bool) (Task, error) {
	var out Task
	if id <= 0 || owner == "" || strings.TrimSpace(reason) == "" || len(reason) > 1000 {
		return out, ErrInvalid
	}
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var task Task
		if err := tx.Raw("SELECT * FROM seo_tasks WHERE id=? FOR UPDATE", id).Scan(&task).Error; err != nil {
			return err
		}
		if task.ID == 0 {
			return gorm.ErrRecordNotFound
		}
		if task.State != "quarantined" && task.State != "skipped" {
			return &StateError{"not_reopenable"}
		}
		var check struct{ Live, Cooling bool }
		if err := tx.Raw("SELECT ?::timestamptz IS NULL OR ?::timestamptz>clock_timestamp() AS live,coalesce(?::timestamptz>clock_timestamp()-make_interval(secs=>?),false) AS cooling", task.ExpiresAt, task.ExpiresAt, task.ReopenedAt, AgentReopenWait(int(task.ReopenCount)).Seconds()).Scan(&check).Error; err != nil {
			return err
		}
		if !check.Live {
			return &StateError{"expired"}
		}
		if task.Kind == "translate" {
			// Backfilled tasks have no source snapshot. Always compare with the
			// actual source, including when a snapshot exists but is stale.
			var evidence struct {
				Ref content.Ref `json:"ref"`
			}
			if json.Unmarshal(task.Evidence, &evidence) != nil || !evidence.Ref.Valid() || evidence.Ref.Entity+":"+evidence.Ref.Key() != task.EntityKey {
				return &StateError{"superseded"}
			}
			source, err := (&store.Store{DB: tx}).ReadContentSource(ctx, evidence.Ref)
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return &StateError{"superseded"}
			}
			if err != nil {
				return err
			}
			if source.Hash != task.EvidenceHash {
				return &StateError{"superseded"}
			}
		}
		if !person && check.Cooling {
			return &StateError{"reopen_cooldown"}
		}
		if err := tx.Exec(`UPDATE seo_tasks SET state='open',next_retry_at=NULL,lease_owner=NULL,lease_until=NULL,reopen_count=reopen_count+1,reopened_at=clock_timestamp(),
  result=jsonb_build_object('reopenedFrom',state,'reason',?::text,'by',?::text,'previous',result),updated_at=clock_timestamp() WHERE id=?`, reason, owner, id).Error; err != nil {
			return err
		}
		var err error
		out, err = (&Service{DB: tx}).Get(ctx, id)
		return err
	})
	return out, err
}
