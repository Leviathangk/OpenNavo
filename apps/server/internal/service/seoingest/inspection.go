package seoingest

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

func (s *Service) Inspect(ctx context.Context) (map[string]any, error) {
	client, err := s.Google(ctx)
	if errors.Is(err, ErrDisabled) {
		return map[string]any{"disabled": true}, nil
	}
	if err != nil {
		return nil, err
	}
	if err := s.SyncRegistry(ctx); err != nil {
		return nil, err
	}
	// Google resets daily quotas at midnight Pacific. Reserve before the call; timeouts consume quota too.
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		return nil, err
	}
	now := s.now()
	quotaDay := day(now.In(loc))
	var urls []URL
	if err := s.DB.WithContext(ctx).Raw(`SELECT u.* FROM seo_urls u LEFT JOIN seo_url_status s ON s.url_id=u.id WHERE s.checked_at IS NULL OR s.checked_at<? ORDER BY (s.checked_at IS NULL) DESC,(u.updated_at>s.checked_at) DESC,s.checked_at ASC NULLS FIRST,u.id LIMIT 1500`, now.Add(-24*time.Hour)).Scan(&urls).Error; err != nil {
		return nil, err
	}
	checked := 0
	for _, u := range urls {
		var used int
		if err := s.DB.WithContext(ctx).Raw(`INSERT INTO seo_inspection_quota(day,used) VALUES(?,1) ON CONFLICT(day) DO UPDATE SET used=seo_inspection_quota.used+1 WHERE seo_inspection_quota.used<1500 RETURNING used`, quotaDay).Scan(&used).Error; err != nil {
			return nil, err
		}
		if used == 0 {
			return map[string]any{"checked": checked, "quotaExhausted": true}, nil
		}
		r, err := client.Inspect(ctx, u.URL)
		if err != nil {
			return nil, err
		}
		if err := s.DB.WithContext(ctx).Exec(`INSERT INTO seo_url_status(url_id,coverage_state,indexing_state,google_canonical,last_crawl_at,checked_at) VALUES(?,?,?,?,?,?) ON CONFLICT(url_id) DO UPDATE SET coverage_state=EXCLUDED.coverage_state,indexing_state=EXCLUDED.indexing_state,google_canonical=EXCLUDED.google_canonical,last_crawl_at=EXCLUDED.last_crawl_at,checked_at=EXCLUDED.checked_at WHERE seo_url_status.checked_at<=EXCLUDED.checked_at`, u.ID, r.CoverageState, r.IndexingState, r.GoogleCanonical, r.LastCrawlTime, now).Error; err != nil {
			return nil, err
		}
		checked++
	}
	err = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockSource(tx, "inspection"); err != nil {
			return err
		}
		// Completeness describes this sample, never all URLs in the property.
		return tx.Exec(`UPDATE seo_data_watermarks SET fresh_through=GREATEST(fresh_through,?::date),updated_at=? WHERE source='inspection'`, day(now), now).Error
	})
	return map[string]any{"checked": checked, "coverage": "sample"}, err
}
