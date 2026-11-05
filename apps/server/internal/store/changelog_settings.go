package store

import (
	"context"
	"strconv"

	"gorm.io/gorm"
)

type ChangelogSettings struct {
	Excluded bool `json:"excluded"`
}

// PackageChangelogSettings reads the exclusion for every package kind that can own a changelog source.
// Scheduled candidates include kind=link apps whose public GitHub repository was verified by the source
// facts job, so restricting this lookup to casks made a link candidate fail every schedule tick with
// gorm.ErrRecordNotFound.
func (s *Store) PackageChangelogSettings(ctx context.Context, id int64) (ChangelogSettings, error) {
	var settings ChangelogSettings
	result := s.DB.WithContext(ctx).Raw(`SELECT COALESCE(m.changelog_excluded,false) excluded FROM packages p LEFT JOIN package_meta m ON m.package_id=p.id WHERE p.id=? AND p.kind IN ('cask','link') AND p.removed_at IS NULL`, id).Scan(&settings)
	if result.Error != nil {
		return settings, result.Error
	}
	if result.RowsAffected == 0 {
		return settings, gorm.ErrRecordNotFound
	}
	return settings, nil
}
func (s *Store) SetPackageChangelogSettings(ctx context.Context, id int64, excluded bool, actor AuditActor) (ChangelogSettings, error) {
	result := ChangelogSettings{Excluded: excluded}
	err := s.WithTx(ctx, func(tx *gorm.DB) error {
		if err := LockCatalogWrites(ctx, tx); err != nil {
			return err
		}
		st := &Store{DB: tx}
		before, err := st.PackageChangelogSettings(ctx, id)
		if err != nil {
			return err
		}
		if err := tx.Exec(`INSERT INTO package_meta(package_id,changelog_excluded) VALUES(?,?) ON CONFLICT(package_id) DO UPDATE SET changelog_excluded=EXCLUDED.changelog_excluded`, id, excluded).Error; err != nil {
			return err
		}
		// Restoration affects only the next scheduled run; do not forcibly remove existing queue deduplication locks or historical failures.
		if before.Excluded && !excluded {
			if err := tx.Exec(`UPDATE changelog_sources SET next_fetch_at=now() WHERE package_id=? AND type IN ('homebrew_commits','github_releases')`, id).Error; err != nil {
				return err
			}
		}
		return Audit(ctx, tx, actor, "UpdatePackageChangelogSettings", "package_changelog_settings", strconv.FormatInt(id, 10), before, result)
	})
	return result, err
}
