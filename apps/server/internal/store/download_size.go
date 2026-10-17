package store

import (
	"context"

	"github.com/opennavo/opennavo/server/internal/domain"

	"gorm.io/gorm"
)

// Download probing reads catalog information only and does not depend on retired content enrichment.
func (s *Store) DownloadSizePackage(ctx context.Context, id int64) (PublicPackage, error) {
	rows, err := s.publicRows(domain.WithLinks(ctx, false), "en-US", "p.kind='cask' AND p.id=? AND p.removed_at IS NULL", "", id)
	if err != nil {
		return PublicPackage{}, err
	}
	if len(rows) == 0 {
		return PublicPackage{}, gorm.ErrRecordNotFound
	}
	return rows[0], nil
}
func (s *Store) ScheduleDownloadSizes(ctx context.Context) (int64, error) {
	result := s.DB.WithContext(ctx).Exec(`INSERT INTO job_outbox(unique_key,job_type,payload) SELECT 'assets:download-size:'||p.id,'assets:download-size',jsonb_build_object('packageId',p.id) FROM packages p LEFT JOIN package_meta m ON m.package_id=p.id WHERE p.kind='cask' AND p.removed_at IS NULL AND NOT (p.disabled OR p.stale_disabled) AND NOT p.is_font AND NOT coalesce(m.hidden,false) AND p.download_size IS NULL AND p.download_url IS NOT NULL ORDER BY p.popularity DESC,p.id ON CONFLICT(unique_key) DO NOTHING`)
	return result.RowsAffected, result.Error
}
