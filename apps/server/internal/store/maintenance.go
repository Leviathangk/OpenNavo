package store

import (
	"context"
	"time"

	"gorm.io/gorm"
)

func (s *Store) SetDownloadSize(ctx context.Context, p PublicPackage, value *int64) (bool, error) {
	changed := false
	err := s.WithTx(ctx, func(tx *gorm.DB) error {
		if err := LockCatalogWrites(ctx, tx); err != nil {
			return err
		}
		r := tx.WithContext(ctx).Exec("UPDATE packages SET download_size=?,updated_at=now() WHERE id=? AND version=? AND download_url IS NOT DISTINCT FROM ? AND download_size IS DISTINCT FROM ?", value, p.ID, p.Version, p.DownloadURL, value)
		if r.Error != nil || r.RowsAffected == 0 {
			return r.Error
		}
		changed = true
		return tx.WithContext(ctx).Exec("INSERT INTO catalog_changes(package_id,kind,token,op,reason) VALUES(?,?,?,'upsert','asset')", p.ID, p.Kind, p.Token).Error
	})
	return changed, err
}
func (s *Store) PruneLogs(ctx context.Context, before time.Time) (map[string]int64, error) {
	out := map[string]int64{}
	err := s.WithTx(ctx, func(tx *gorm.DB) error {
		for _, table := range []string{"search_queries", "sync_runs", "translation_logs"} {
			column := "created_at"
			extra := ""
			if table == "sync_runs" {
				column = "started_at"
				extra = " AND status<>'running'"
			}
			r := tx.WithContext(ctx).Exec("DELETE FROM "+table+" WHERE "+column+"<?"+extra, before)
			if r.Error != nil {
				return r.Error
			}
			out[table] = r.RowsAffected
		}
		for _, entry := range []struct {
			table, predicate string
			arg              any
		}{
			{"agent_call_logs", "created_at<?", before.AddDate(0, 0, -90)},
			{"usage_daily", "day<?::date", before.In(usageZone).Format("2006-01-02")},
			{"usage_identities", "last_seen_at<?", before},
			{"content_trash", "expires_at<?", before.AddDate(0, 0, 90)},
			// Idempotency records only need to outlive client retries: 30 days.
			{"agent_write_controls", "created_at<?", before.AddDate(0, 0, 60)},
		} {
			r := tx.WithContext(ctx).Exec("DELETE FROM "+entry.table+" WHERE "+entry.predicate, entry.arg)
			if r.Error != nil {
				return r.Error
			}
			out[entry.table] = r.RowsAffected
		}
		return nil
	})
	return out, err
}

// Lock candidates before checking references; concurrent foreign-key references wait for this row lock, preventing deletion of images in use.
func (s *Store) PruneAssets(ctx context.Context, before time.Time, remove func(context.Context, Asset) error) (int64, error) {
	var total int64
	for {
		count := int64(0)
		err := s.WithTx(ctx, func(tx *gorm.DB) error {
			var rows []Asset
			if err := tx.WithContext(ctx).Raw(`SELECT a.* FROM assets a WHERE a.created_at<? AND NOT EXISTS(SELECT 1 FROM package_meta WHERE icon_asset_id=a.id) AND NOT EXISTS(SELECT 1 FROM package_screenshots WHERE asset_id=a.id) AND NOT EXISTS(SELECT 1 FROM collections WHERE cover_asset_id=a.id) AND NOT EXISTS(SELECT 1 FROM article_revisions WHERE cover_asset_id=a.id) AND NOT EXISTS(SELECT 1 FROM article_authors WHERE avatar_asset_id=a.id) AND NOT EXISTS(SELECT 1 FROM content_revisions WHERE asset_ids @> ARRAY[a.id]) AND NOT EXISTS(SELECT 1 FROM content_trash WHERE expires_at>now() AND asset_ids @> ARRAY[a.id]) ORDER BY a.id LIMIT 50 FOR UPDATE OF a SKIP LOCKED`, before).Scan(&rows).Error; err != nil {
				return err
			}
			for _, asset := range rows {
				var referenced bool
				if err := tx.WithContext(ctx).Raw(`SELECT EXISTS(SELECT 1 FROM package_meta WHERE icon_asset_id=? UNION ALL SELECT 1 FROM package_screenshots WHERE asset_id=? UNION ALL SELECT 1 FROM collections WHERE cover_asset_id=? UNION ALL SELECT 1 FROM article_revisions WHERE cover_asset_id=? UNION ALL SELECT 1 FROM article_authors WHERE avatar_asset_id=? UNION ALL SELECT 1 FROM content_revisions WHERE asset_ids @> ARRAY[?::bigint] UNION ALL SELECT 1 FROM content_trash WHERE expires_at>now() AND asset_ids @> ARRAY[?::bigint])`, asset.ID, asset.ID, asset.ID, asset.ID, asset.ID, asset.ID, asset.ID).Scan(&referenced).Error; err != nil {
					return err
				}
				if referenced {
					continue
				}
				if err := remove(ctx, asset); err != nil {
					return err
				}
				r := tx.WithContext(ctx).Exec("DELETE FROM assets WHERE id=?", asset.ID)
				if r.Error != nil {
					return r.Error
				}
				count += r.RowsAffected
			}
			return nil
		})
		if err != nil {
			return total, err
		}
		total += count
		if count == 0 {
			return total, nil
		}
	}
}
