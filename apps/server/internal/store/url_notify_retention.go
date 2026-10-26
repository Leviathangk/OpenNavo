package store

import (
	"context"
	"time"

	"gorm.io/gorm"
)

const URLNotificationRetention = 14 * 24 * time.Hour
const URLNotificationPruneBatch = 1000

// PruneURLNotifications removes a bounded batch of completed delivery receipts.
// The latest settings receipt is a barrier/witness used by ClaimURLNotifications,
// even when dead, so it must survive retention. Older dead receipts can go as
// soon as a newer notification supersedes them; retain their newer witness until
// they have actually been deleted (including concurrent cleanup batches).
func (s *Store) PruneURLNotifications(ctx context.Context, before time.Time, limit int) (int64, error) {
	if limit <= 0 {
		return 0, nil
	}
	var count int64
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL statement_timeout = '2s'").Error; err != nil {
			return err
		}
		// Separate predicates let PostgreSQL use the small dead-receipt index and
		// the retention timestamp index, instead of scanning all recent successes.
		for _, expired := range []bool{false, true} {
			predicate := "d.state='dead' AND EXISTS (SELECT 1 FROM url_notify_outbox newer WHERE newer.url=d.url AND newer.id>d.id)"
			args := []any{}
			if expired {
				predicate = "d.state IN ('announced','dead') AND d.updated_at<?"
				args = append(args, before)
			}
			args = append(args, min(limit, URLNotificationPruneBatch)-int(count))
			result := tx.Exec(`DELETE FROM url_notify_outbox WHERE id IN (
 SELECT d.id FROM url_notify_outbox d WHERE `+predicate+`
 AND (d.path<>'/' OR EXISTS (SELECT 1 FROM url_notify_outbox newer WHERE newer.path='/' AND newer.id>d.id))
 AND NOT EXISTS (SELECT 1 FROM url_notify_outbox older WHERE older.url=d.url AND older.id<d.id AND older.state='dead')
 ORDER BY d.id LIMIT ? FOR UPDATE OF d SKIP LOCKED
)`, args...)
			if result.Error != nil {
				return result.Error
			}
			count += result.RowsAffected
			if count == int64(min(limit, URLNotificationPruneBatch)) {
				break
			}
		}
		return nil
	})
	return count, err
}
