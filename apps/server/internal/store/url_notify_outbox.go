package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/i18n"
	"gorm.io/gorm"
)

// URLNotification is written in the same transaction as the business change or feed cursor.
type URLNotification struct {
	URL, Path, Locale, Reason, PurgePrefix string
	Announce                               bool
	Robots, CanonicalPath                  string
}

type URLNotificationRow struct {
	URLNotification
	ID, Revision int64
	State        string
	Attempts     int
	LeaseToken   string
	// ContentBaseline is the page content date checked before delivery, never a receipt time.
	// Zero means no verified content snapshot and cannot suppress later edits.
	ContentBaseline time.Time `gorm:"-"`
}

func QueueURLNotification(ctx context.Context, tx *gorm.DB, n URLNotification) error {
	if n.URL == "" || !strings.HasPrefix(n.Path, "/") || strings.HasPrefix(n.Path, "//") || !i18n.Valid(n.Locale) {
		return errors.New("invalid URL notification")
	}
	if n.Reason == "" {
		n.Reason = "updated"
	}
	if err := QueueCacheInvalidation(ctx, tx, "url-notify",
		"c:pkg:*", "c:home:*", "c:rank:*", "c:cat:*", "c:sug:*", "c:dep:*", "c:related:*",
		"c:art:*", "c:col:*", "c:coll:*", "c:rel:*", "c:site:*", "c:cfg:*", BrowseRefreshPattern); err != nil {
		return err
	}
	return tx.WithContext(ctx).Exec(`INSERT INTO url_notify_outbox(url,path,locale,reason,purge_prefix,announce,robots,canonical_path)
VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(url) WHERE state IN ('pending','invalidated','probed')
DO UPDATE SET path=EXCLUDED.path,locale=EXCLUDED.locale,reason=EXCLUDED.reason,purge_prefix=EXCLUDED.purge_prefix,
announce=EXCLUDED.announce,robots=EXCLUDED.robots,canonical_path=EXCLUDED.canonical_path,
state='pending',revision=url_notify_outbox.revision+1,attempts=0,next_attempt_at=now(),
lease_token=NULL,lease_until=NULL,last_error='',updated_at=now()`, n.URL, n.Path, n.Locale, n.Reason, n.PurgePrefix, n.Announce, n.Robots, n.CanonicalPath).Error
}

// QueueURLFeed atomically persists the entire feed slice before advancing either cursor.
func (s *Store) QueueURLFeed(ctx context.Context, previous URLNotifyState, next URLNotifyState, notifications []URLNotification) error {
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current URLNotifyState
		if err := tx.Raw("SELECT cursor,content_cursor FROM url_notify_state WHERE id=true FOR UPDATE").Scan(&current).Error; err != nil {
			return err
		}
		if current.Cursor != previous.Cursor || current.ContentCursor != previous.ContentCursor {
			return errors.New("URL feed cursor changed")
		}
		for _, n := range notifications {
			if err := QueueURLNotification(ctx, tx, n); err != nil {
				return err
			}
		}
		if previous.IndexBackfill != next.IndexBackfill {
			if err := QueueCacheInvalidation(ctx, tx, "url-index-backfill", cache.PackageDetailPrefix+"*"); err != nil {
				return err
			}
		}
		return tx.Exec(`UPDATE url_notify_state SET cursor=?,content_cursor=?,last_run_at=?,last_urls=?,index_backfill=?,last_error='' WHERE id=true`, next.Cursor, next.ContentCursor, next.LastRunAt, next.LastURLs, next.IndexBackfill).Error
	})
}

func (s *Store) ClaimURLNotifications(ctx context.Context, token string, now time.Time, limit int, maxRetryAfter time.Duration) ([]URLNotificationRow, error) {
	rows := []URLNotificationRow{}
	// Older workers copied unbounded probe/purge deadlines into the outbox. Repair
	// those on first claim, preserving ordinary content waits and next-day budgets.
	capAt := now.Add(config.ProbeRetryAfterCap(maxRetryAfter))
	if err := s.DB.WithContext(ctx).Exec(`UPDATE url_notify_outbox SET next_attempt_at=?
 WHERE state IN ('pending','invalidated','probed') AND next_attempt_at>?
 AND next_attempt_at>updated_at + make_interval(secs => ?)`, capAt, capAt, config.MaxProbeRetryAfter.Seconds()).Error; err != nil {
		return rows, err
	}
	err := s.DB.WithContext(ctx).Raw(`UPDATE url_notify_outbox SET lease_token=?,lease_until=?,updated_at=?
WHERE id IN (SELECT id FROM url_notify_outbox WHERE state IN ('pending','invalidated','probed')
AND (path='/' OR NOT EXISTS (SELECT 1 FROM url_notify_outbox settings WHERE settings.path='/' AND settings.state IN ('pending','dead')
 AND NOT EXISTS (SELECT 1 FROM url_notify_outbox newer WHERE newer.path='/' AND newer.id>settings.id)))
AND next_attempt_at<=? AND (lease_until IS NULL OR lease_until<=?) ORDER BY (reason='index'),next_attempt_at,id
LIMIT ? FOR UPDATE SKIP LOCKED) RETURNING *`, token, now.Add(2*time.Minute), now, now, now, limit).Scan(&rows).Error
	return rows, err
}

// CompleteURLNotification fences acknowledgements against a newer business change or lease.
func (s *Store) CompleteURLNotification(ctx context.Context, row URLNotificationRow, next string, problem error, now time.Time) error {
	attempts, message, due := 0, "", now
	if problem != nil {
		attempts, message, next = row.Attempts+1, problem.Error(), row.State
		if len(message) > 1000 {
			message = message[:1000]
		}
		due = now.Add(time.Duration(1<<min(attempts, 10)) * 30 * time.Second)
		if attempts >= 12 {
			next = "dead"
		}
	}
	return s.DB.WithContext(ctx).Exec(`UPDATE url_notify_outbox SET state=?,attempts=?,next_attempt_at=?,last_error=?,
lease_token=NULL,lease_until=NULL,updated_at=? WHERE id=? AND revision=? AND lease_token=? AND lease_until>?`, next, attempts, due, message, now, row.ID, row.Revision, row.LeaseToken, now).Error
}

// SuppressURLAnnouncement finishes invalidated work whose evidence no longer
// qualifies. Keep the decision in the durable receipt and fence concurrent edits.
func (s *Store) SuppressURLAnnouncement(ctx context.Context, row URLNotificationRow, robots, canonical string, now time.Time) error {
	return s.DB.WithContext(ctx).Exec(`UPDATE url_notify_outbox SET state='announced',announce=false,robots=?,canonical_path=?,lease_token=NULL,lease_until=NULL,last_error='',updated_at=?
 WHERE id=? AND revision=? AND lease_token=? AND lease_until>?`, robots, canonical, now, row.ID, row.Revision, row.LeaseToken, now).Error
}

func (s *Store) PauseURLNotification(ctx context.Context, row URLNotificationRow, now time.Time) error {
	return s.DeferURLNotification(ctx, row, now, now.Add(time.Minute))
}

// Waiting for shared capacity is not a delivery failure and must not exhaust
// the retry budget. Keep the lease/revision fence used by all acknowledgements.
func (s *Store) DeferURLNotification(ctx context.Context, row URLNotificationRow, now, due time.Time) error {
	if capAt := now.Add(config.MaxProbeRetryAfter); due.After(capAt) {
		due = capAt
	}
	return s.DB.WithContext(ctx).Exec(`UPDATE url_notify_outbox SET next_attempt_at=?,lease_token=NULL,lease_until=NULL,last_error='',updated_at=?
WHERE id=? AND revision=? AND lease_token=? AND lease_until>?`, due, now, row.ID, row.Revision, row.LeaseToken, now).Error
}

// RenewURLNotification checks ownership immediately before an external side effect.
// Expired owners must reclaim; they cannot renew a lease someone else may have acquired.
func (s *Store) RenewURLNotification(ctx context.Context, row URLNotificationRow, now time.Time) (bool, error) {
	result := s.DB.WithContext(ctx).Exec(`UPDATE url_notify_outbox SET lease_until=?
 WHERE id=? AND revision=? AND lease_token=? AND lease_until>?`, now.Add(2*time.Minute), row.ID, row.Revision, row.LeaseToken, now)
	return result.RowsAffected == 1, result.Error
}

// HasUnreadyFreshNotifications includes all active processing phases and leases.
// readyIDs are fresh rows this worker is about to submit ahead of backfill.
func (s *Store) HasUnreadyFreshNotifications(ctx context.Context, readyIDs []int64) (bool, error) {
	query := s.DB.WithContext(ctx).Table("url_notify_outbox").Where("announce AND reason <> 'index' AND state IN ('pending','invalidated','probed')")
	if len(readyIDs) > 0 {
		query = query.Where("id NOT IN ?", readyIDs)
	}
	var count int64
	err := query.Count(&count).Error
	return count > 0, err
}
