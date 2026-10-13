package store

import (
	"context"
	"time"
)

// LifecycleRow is where a requested package token leads (WP2).
type LifecycleRow struct {
	ID          int64
	Token       string
	Version     string
	RemovedAt   *time.Time
	Replacement *string
}

// PackageLifecycle resolves a token: a live package holding it, else an alias (rename or a former
// token Homebrew declares), else a removed package that held it. found is false for unknown tokens.
func (s *Store) PackageLifecycle(ctx context.Context, kind, token string) (LifecycleRow, bool, error) {
	var rows []LifecycleRow
	err := s.DB.WithContext(ctx).Raw(`SELECT p.id,p.token,p.version,p.removed_at,COALESCE(p.disable_replacement,p.deprecation_replacement) AS replacement
 FROM (SELECT id,1 AS rank FROM packages WHERE kind=? AND token=? AND removed_at IS NULL
  UNION ALL SELECT package_id,2 FROM package_aliases WHERE kind=? AND token=?
  UNION ALL SELECT id,3 FROM packages WHERE kind=? AND token=? AND removed_at IS NOT NULL) c
 JOIN packages p ON p.id=c.id ORDER BY c.rank LIMIT 1`, kind, token, kind, token, kind, token).Scan(&rows).Error
	if err != nil || len(rows) == 0 {
		return LifecycleRow{}, false, err
	}
	return rows[0], true, nil
}

// LifecycleAlternatives lists popular available casks sharing the removed app's primary category.
func (s *Store) LifecycleAlternatives(ctx context.Context, packageID int64, limit int) ([]int64, error) {
	var ids []int64
	err := s.DB.WithContext(ctx).Raw(`SELECT p.id`+publicJoins+` WHERE `+publicVisible+` AND NOT `+PackageDisabledSQL+` AND p.id<>?
 AND EXISTS (SELECT 1 FROM package_categories pc WHERE pc.package_id=p.id AND pc.category_id IN (SELECT category_id FROM package_categories WHERE package_id=? AND is_primary))
 ORDER BY p.popularity DESC,p.id LIMIT ?`, packageID, packageID, limit).Scan(&ids).Error
	return ids, err
}

// LivePackageID finds an available cask by token, for replacements Homebrew names.
func (s *Store) LivePackageID(ctx context.Context, kind, token string) (int64, bool, error) {
	var ids []int64
	err := s.DB.WithContext(ctx).Raw(`SELECT p.id`+publicJoins+` WHERE `+publicVisible+` AND p.kind=? AND p.token=?`, kind, token).Scan(&ids).Error
	if err != nil || len(ids) == 0 {
		return 0, false, err
	}
	return ids[0], true, nil
}
