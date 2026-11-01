package store

import (
	"context"
	"encoding/json"
	"time"

	"gorm.io/gorm"
)

// ChangelogSourceIdentity names a source for job stats regardless of its type or package state.
type ChangelogSourceIdentity struct {
	Type, Token string
	Config      json.RawMessage
	FailCount   int
}

func (s *Store) ChangelogSourceIdentity(ctx context.Context, id int64) (ChangelogSourceIdentity, error) {
	var row ChangelogSourceIdentity
	r := s.DB.WithContext(ctx).Raw("SELECT s.type,p.token,s.config,s.fail_count FROM changelog_sources s JOIN packages p ON p.id=s.package_id WHERE s.id=?", id).Scan(&row)
	if r.Error != nil {
		return row, r.Error
	}
	if r.RowsAffected == 0 {
		return row, gorm.ErrRecordNotFound
	}
	return row, nil
}

// ClaimChangelogDue returns the due sources (same selection as ChangelogDue) and moves their next_fetch_at to
// leaseUntil in the same statement, so concurrent or later schedule ticks do not select them again.
func (s *Store) ClaimChangelogDue(ctx context.Context, now, leaseUntil time.Time) ([]int64, error) {
	ids, err := s.ChangelogDue(ctx, now)
	if err != nil || len(ids) == 0 {
		return ids, err
	}
	claimed := []int64{}
	err = s.DB.WithContext(ctx).Raw("UPDATE changelog_sources SET next_fetch_at=? WHERE id IN ? AND next_fetch_at<=? RETURNING id", leaseUntil, ids, now).Scan(&claimed).Error
	return claimed, err
}

// ReleaseChangelogClaim gives back the lease of sources whose claim never became a task, so they are due on
// the next tick instead of waiting out fetchLease. Only rows still on the lease are touched: a source whose
// task did start owns its own next_fetch_at by then.
func (s *Store) ReleaseChangelogClaim(ctx context.Context, ids []int64, now time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	return s.DB.WithContext(ctx).Exec("UPDATE changelog_sources SET next_fetch_at=? WHERE id IN ? AND next_fetch_at>=?", now, ids, now).Error
}
