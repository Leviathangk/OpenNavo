package store

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

const StalePackageAge = 90 * 24 * time.Hour

// PackageDisabledSQL applies to catalog presentation. Homebrew synchronization
// and commit-history fetching must still use the upstream disabled flag alone.
const PackageDisabledSQL = `(p.disabled OR p.stale_disabled)`

type StalePackageStats struct {
	AutoDisabled       int `json:"autoDisabled"`
	Reactivated        int `json:"reactivated"`
	MissingVersionDate int `json:"missingVersionDate"`
}

func (s *Store) RefreshStalePackages(ctx context.Context, now time.Time) (StalePackageStats, error) {
	var stats StalePackageStats
	err := s.WithTx(ctx, func(tx *gorm.DB) error {
		if err := LockCatalogWrites(ctx, tx); err != nil {
			return err
		}
		var err error
		stats, err = refreshStalePackages(ctx, tx, now, nil)
		return err
	})
	if err != nil {
		return StalePackageStats{}, err
	}
	return stats, nil
}

// The caller holds LockCatalogWrites. A nil IDs slice checks the entire catalog;
// a non-nil slice reconciles only the packages changed by the current transaction.
func refreshStalePackages(ctx context.Context, tx *gorm.DB, now time.Time, ids []int64) (StalePackageStats, error) {
	stats := StalePackageStats{}
	if ids != nil && len(ids) == 0 {
		return stats, nil
	}
	now = now.UTC()
	var rows []struct {
		ID               int64
		StaleDisabled    bool
		ShouldDisable    bool
		VersionDateKnown bool
		IsFont           bool
	}
	query := `SELECT p.id,p.stale_disabled,p.is_font,
 COALESCE(p.version<>'latest' AND v.brew_committed_at>'0001-01-01T00:00:00Z'::timestamptz
  AND v.brew_committed_at<=? AND isfinite(v.brew_committed_at),false) version_date_known,
 COALESCE(NOT p.is_font AND p.version<>'latest'
  AND v.brew_committed_at>'0001-01-01T00:00:00Z'::timestamptz
  AND v.brew_committed_at<?,false)
  AND NOT COALESCE(p.disabled_override_at IS NOT NULL AND p.disabled_override_stale AND p.disabled_override_version=p.version,false) should_disable
 FROM packages p LEFT JOIN package_versions v ON v.package_id=p.id AND v.version=p.version
 WHERE p.kind='cask' AND p.removed_at IS NULL`
	args := []any{now, now.Add(-StalePackageAge)}
	if ids != nil {
		query += " AND p.id IN ?"
		args = append(args, ids)
	}
	if err := tx.WithContext(ctx).Raw(query, args...).Scan(&rows).Error; err != nil {
		return stats, fmt.Errorf("read package update dates: %w", err)
	}
	disabledIDs, activeIDs := []int64{}, []int64{}
	for _, row := range rows {
		if !row.IsFont && !row.VersionDateKnown {
			stats.MissingVersionDate++
		}
		if row.StaleDisabled == row.ShouldDisable {
			continue
		}
		if row.ShouldDisable {
			disabledIDs = append(disabledIDs, row.ID)
		} else {
			activeIDs = append(activeIDs, row.ID)
		}
	}
	stats.AutoDisabled, stats.Reactivated = len(disabledIDs), len(activeIDs)
	if stats.AutoDisabled+stats.Reactivated == 0 {
		return stats, nil
	}
	for _, change := range []struct {
		IDs      []int64
		Disabled bool
	}{{disabledIDs, true}, {activeIDs, false}} {
		if len(change.IDs) == 0 {
			continue
		}
		if err := tx.WithContext(ctx).Exec("UPDATE packages SET stale_disabled=?,updated_at=? WHERE id IN ?", change.Disabled, now, change.IDs).Error; err != nil {
			return stats, fmt.Errorf("update stale package status: %w", err)
		}
	}
	if err := refreshPackageRanks(ctx, tx); err != nil {
		return stats, err
	}
	// Only apps whose status flipped are recorded. Every app ranked below them shifts by one, and
	// recording those too meant thousands of rows per transition: past 1000 the URL feed purges
	// the whole /apps section in every language, and every desktop pulled them as deltas. Shifted
	// ranks reach desktops with the next hourly snapshot (this change advances its cursor), as the
	// daily analytics ranks do, and reach pages when their caches expire.
	changed := make([]int64, 0, len(disabledIDs)+len(activeIDs))
	changed = append(changed, disabledIDs...)
	changed = append(changed, activeIDs...)
	if err := tx.WithContext(ctx).Exec(`INSERT INTO catalog_changes(package_id,kind,token,op,reason)
 SELECT id,kind,token,'upsert','content' FROM packages WHERE id IN ?`, changed).Error; err != nil {
		return stats, fmt.Errorf("record stale package changes: %w", err)
	}
	return stats, addCatalogInvalidation(ctx, tx)
}

func refreshPackageRanks(ctx context.Context, tx *gorm.DB) error {
	if err := tx.WithContext(ctx).Exec(`UPDATE packages SET rank_30d=NULL
 WHERE kind='cask' AND (removed_at IS NOT NULL OR is_font OR disabled OR stale_disabled)
 AND rank_30d IS NOT NULL`).Error; err != nil {
		return fmt.Errorf("reset excluded ranks: %w", err)
	}
	if err := tx.WithContext(ctx).Exec(`WITH ranked AS (
 SELECT id,row_number() OVER (ORDER BY installs_30d DESC,token) AS rank FROM packages
 WHERE kind='cask' AND removed_at IS NULL AND NOT is_font AND NOT disabled AND NOT stale_disabled)
 UPDATE packages p SET rank_30d=ranked.rank FROM ranked
 WHERE p.id=ranked.id AND p.rank_30d IS DISTINCT FROM ranked.rank`).Error; err != nil {
		return fmt.Errorf("compute analytics ranks: %w", err)
	}
	return nil
}
