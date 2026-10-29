package store

import (
	"context"
	"time"

	"gorm.io/gorm/clause"
)

// IndexNowContentState reads the page date and the last accepted announcement in
// one snapshot. Nil dates distinguish absent rows from the hash-upgrade sentinel.
type IndexNowContentState struct {
	PackageID                    int64
	ChangedAt, ContentModifiedAt *time.Time
	AnnouncedAt                  *time.Time
	Robots, CanonicalPath        string
}

func (s *Store) IndexNowPageState(ctx context.Context, token, locale, kind, url string) (IndexNowContentState, error) {
	var state IndexNowContentState
	// Keep package eligibility identical to IndexRowByToken, including hidden apps.
	err := s.DB.WithContext(ctx).Raw(`SELECT p.id AS package_id,d.changed_at,ps.content_modified_at,
 u.announced_at,COALESCE(u.robots,'') AS robots,COALESCE(u.canonical_path,'') AS canonical_path
 FROM packages p
 LEFT JOIN package_page_dirty d ON d.package_id=p.id
 LEFT JOIN package_page_state ps ON ps.package_id=p.id AND ps.locale=? AND ps.page_kind=? AND ps.page_number=1
 LEFT JOIN indexnow_url_state u ON u.url=?
 WHERE p.token=? AND p.kind IN ('cask','link') AND p.removed_at IS NULL`, locale, kind, url, token).Scan(&state).Error
	return state, err
}

// SkipUnchangedURLAnnouncement preserves the decision while completing a job
// that needs no submission. A newer revision or lease must remain untouched.
func (s *Store) SkipUnchangedURLAnnouncement(ctx context.Context, row URLNotificationRow, now time.Time) (bool, error) {
	result := s.DB.WithContext(ctx).Exec(`UPDATE url_notify_outbox SET state='announced',announce=false,
 skip_reason='unchanged',last_error='',lease_token=NULL,lease_until=NULL,updated_at=?
 WHERE id=? AND revision=? AND lease_token=? AND lease_until>?`, now, row.ID, row.Revision, row.LeaseToken, now)
	return result.RowsAffected == 1, result.Error
}

// RecordIndexNowURLs records accepted requests, independent of outbox ownership:
// a business edit during delivery cannot undo a request IndexNow already accepted.
// announced_at stores the verified pre-send content date, not the HTTP receipt time.
// An unknown snapshot uses the zero date so it cannot hide an in-flight edit.
func (s *Store) RecordIndexNowURLs(ctx context.Context, rows []URLNotificationRow) error {
	if len(rows) == 0 {
		return nil
	}
	values := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		values = append(values, map[string]any{"url": row.URL, "announced_at": row.ContentBaseline, "robots": row.Robots, "canonical_path": row.CanonicalPath})
	}
	return s.DB.WithContext(ctx).Table("indexnow_url_state").Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "url"}},
		DoUpdates: clause.AssignmentColumns([]string{"announced_at", "robots", "canonical_path"}),
	}).Create(&values).Error
}
