package seotasks

import (
	"context"
	"errors"
	"strings"

	"gorm.io/gorm"
)

// StateError reports why a task cannot make the requested transition. Reason is
// a short snake_case rule the caller can act on.
type StateError struct{ Reason string }

func (e *StateError) Error() string { return "SEO task state: " + e.Reason }

// Reopen returns a task to the queue without cooldown, expiry or source-version
// prerequisites. Incrementing fencing invalidates any previous worker lease.
func (s *Service) Reopen(ctx context.Context, id int64, owner, reason string, _ bool) (Task, error) {
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
		if task.LeaseOwner != nil {
			// Release only this task's lease. A takeover or an earlier release is
			// already fenced off and must not prevent returning the task to open.
			if err := release(tx, task, *task.LeaseOwner); err != nil && !errors.Is(err, ErrLease) {
				return err
			}
		}
		if err := tx.Exec(`UPDATE seo_tasks SET state='open',next_retry_at=NULL,expires_at=NULL,lease_owner=NULL,lease_until=NULL,fencing=fencing+1,reopen_count=reopen_count+1,reopened_at=clock_timestamp(),
  result=jsonb_build_object('reopenedFrom',state,'reason',?::text,'by',?::text,'previous',result),updated_at=clock_timestamp() WHERE id=?`, reason, owner, id).Error; err != nil {
			return err
		}
		var err error
		out, err = (&Service{DB: tx}).Get(ctx, id)
		return err
	})
	return out, err
}
