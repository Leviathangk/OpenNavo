package seotasks

import (
	"context"
	"strings"
	"time"

	"gorm.io/gorm"
)

// StateError reports why a task cannot make the requested transition. Reason is
// a short snake_case rule the caller can act on.
type StateError struct{ Reason string }

func (e *StateError) Error() string { return "SEO task state: " + e.Reason }

// An agent may reopen a task it could not finish this many times; after that a
// person decides. The cooldown keeps a failing fix from looping every run.
const (
	AgentReopenLimit    = 3
	AgentReopenCooldown = time.Hour
)

// Reopen returns a quarantined or skipped task to the queue once its cause has
// been fixed. People may always reopen; agents within AgentReopenLimit and
// AgentReopenCooldown. A translation whose source changed since then is
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
		if err := tx.Raw("SELECT ?::timestamptz IS NULL OR ?::timestamptz>clock_timestamp() AS live,coalesce(?::timestamptz>clock_timestamp()-make_interval(secs=>?),false) AS cooling", task.ExpiresAt, task.ExpiresAt, task.ReopenedAt, AgentReopenCooldown.Seconds()).Scan(&check).Error; err != nil {
			return err
		}
		if !check.Live {
			return &StateError{"expired"}
		}
		if task.Kind == "translate" {
			entity, key, _ := strings.Cut(task.EntityKey, ":")
			var current bool
			if err := tx.Raw("SELECT EXISTS(SELECT 1 FROM content_translation_sources WHERE entity=? AND object_key=? AND source_hash=?)", entity, key, task.EvidenceHash).Scan(&current).Error; err != nil {
				return err
			}
			if !current {
				return &StateError{"superseded"}
			}
		}
		if !person && task.ReopenCount >= AgentReopenLimit {
			return &StateError{"reopen_limit"}
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
