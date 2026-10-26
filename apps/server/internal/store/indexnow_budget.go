package store

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// ReserveIndexNowBudget persists a conservative UTC-day allowance before network delivery.
// Failed/ambiguous requests keep their reservation, so restarts cannot exceed the cap.
func (s *Store) ReserveIndexNowBudget(ctx context.Context, now time.Time, limit, requested int) (int, error) {
	allowed := 0
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		day := now.UTC().Format("2006-01-02")
		if err := tx.Exec(`INSERT INTO indexnow_daily_budget(day) VALUES(?) ON CONFLICT DO NOTHING`, day).Error; err != nil {
			return err
		}
		var used int
		if err := tx.Raw(`SELECT reserved FROM indexnow_daily_budget WHERE day=? FOR UPDATE`, day).Scan(&used).Error; err != nil {
			return err
		}
		allowed = min(max(limit-used, 0), max(requested, 0))
		return tx.Exec(`UPDATE indexnow_daily_budget SET reserved=reserved+? WHERE day=?`, allowed, day).Error
	})
	return allowed, err
}

func (s *Store) RecordIndexNowAccepted(ctx context.Context, now time.Time, count int) (int, error) {
	var total int
	err := s.DB.WithContext(ctx).Raw(`UPDATE indexnow_daily_budget SET accepted=accepted+? WHERE day=? RETURNING accepted`, count, now.UTC().Format("2006-01-02")).Scan(&total).Error
	return total, err
}
