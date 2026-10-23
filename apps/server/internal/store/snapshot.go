package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/domain"
	"gorm.io/gorm"
)

type SnapshotRecord struct {
	ModernBase              *publicapi.CatalogSnapshotInfo `gorm:"-"`
	ID                      int64
	FormatVersion           int
	Seq                     int64
	ItemCount               int
	StorageKey, URL, SHA256 string
	Bytes                   int64
	CreatedAt               time.Time
	TextPacks               *publicapi.CatalogTextPacks `gorm:"-"`
}
type SnapshotCategory struct {
	SourceLocale    string            `json:"sourceLocale,omitempty"`
	Slug            string            `json:"slug"`
	Parent          *string           `json:"parent"`
	Icon            string            `json:"icon"`
	Sort            int               `json:"sort"`
	HiddenByDefault bool              `json:"hiddenByDefault"`
	AppliesTo       string            `json:"appliesTo"`
	Name            map[string]string `json:"name"`
}

func (s *Store) CatalogRead(ctx context.Context, fn func(*Store) error) error {
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return fn(&Store{DB: tx, Redis: s.Redis}) }, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
}
func (s *Store) LatestSnapshot(ctx context.Context) (SnapshotRecord, error) {
	var r SnapshotRecord
	err := s.DB.WithContext(ctx).Raw("SELECT * FROM catalog_snapshots WHERE format_version=2 AND storage_key LIKE 'snapshots/base-v2-%' ORDER BY created_at DESC,id DESC LIMIT 1").Scan(&r).Error
	if err == nil && r.ID == 0 {
		err = gorm.ErrRecordNotFound
	}
	if err == nil {
		var row struct{ Value json.RawMessage }
		err = s.DB.WithContext(ctx).Raw("SELECT value FROM app_config WHERE key=?", "catalog.snapshotManifest:"+r.SHA256).Scan(&row).Error
		if err == nil && len(row.Value) > 0 {
			err = json.Unmarshal(row.Value, &r.TextPacks)
		} else if err == nil {
			err = gorm.ErrRecordNotFound
		}
	}
	if err == nil {
		var row struct{ Value json.RawMessage }
		err = s.DB.WithContext(ctx).Raw("SELECT value FROM app_config WHERE key=?", "catalog.snapshotModernBase:"+r.SHA256).Scan(&row).Error
		if err == nil && len(row.Value) > 0 {
			err = json.Unmarshal(row.Value, &r.ModernBase)
		}
	}
	return r, err
}
func (s *Store) CatalogBounds(ctx context.Context) (low, high int64, err error) {
	var r struct{ Low, High int64 }
	err = s.DB.WithContext(ctx).Raw(`SELECT GREATEST(COALESCE(max(seq) FILTER(WHERE created_at<now()-interval '14 days'),0),COALESCE((SELECT value::text::bigint FROM app_config WHERE key='catalog.retentionCursor'),0))+1 AS low,GREATEST(COALESCE(max(seq),0),(SELECT COALESCE(max(seq),0) FROM catalog_snapshots),COALESCE((SELECT value::text::bigint FROM app_config WHERE key='catalog.retentionCursor'),0)) AS high FROM catalog_changes WHERE kind<>'link'`).Scan(&r).Error
	return r.Low, r.High, err
}
func (s *Store) LastAnalytics(ctx context.Context) (time.Time, error) {
	var r struct{ At time.Time }
	err := s.DB.WithContext(ctx).Raw("SELECT COALESCE(max(finished_at),'epoch'::timestamptz) AS at FROM sync_runs WHERE job_type='analytics:sync' AND status='succeeded'").Scan(&r).Error
	return r.At, err
}
func (s *Store) SnapshotPackages(ctx context.Context, locale string) ([]PublicPackage, error) {
	return s.publicRows(domain.WithLinks(ctx, false), locale, "p.removed_at IS NULL", "ORDER BY p.kind,p.token")
}
func (s *Store) SnapshotCategories(ctx context.Context) ([]SnapshotCategory, error) {
	var rows []struct{ Data json.RawMessage }
	err := s.DB.WithContext(ctx).Raw(`SELECT jsonb_build_object('slug',c.slug,'parent',p.slug,'sourceLocale',c.source_locale,'icon',c.icon,'sort',c.sort,'hiddenByDefault',c.hidden_by_default,'appliesTo',c.applies_to,'name',COALESCE((SELECT jsonb_object_agg(t.locale,t.name) FROM category_i18n t WHERE t.category_id=c.id AND NULLIF(t.name,'') IS NOT NULL),'{}'::jsonb)) AS data FROM categories c LEFT JOIN categories p ON p.id=c.parent_id WHERE ` + publicCategoryVisible + ` ORDER BY c.sort,c.id`).Scan(&rows).Error
	out := []SnapshotCategory{}
	for _, row := range rows {
		var c SnapshotCategory
		if e := json.Unmarshal(row.Data, &c); e != nil {
			return nil, e
		}
		out = append(out, c)
	}
	return out, err
}
func (s *Store) SaveSnapshot(ctx context.Context, r SnapshotRecord) error {
	return s.WithTx(ctx, func(tx *gorm.DB) error {
		if r.ModernBase != nil {
			raw, err := json.Marshal(r.ModernBase)
			if err != nil {
				return err
			}
			if err = tx.WithContext(ctx).Exec("INSERT INTO app_config(key,value,description) VALUES(?,?::jsonb,'Internal immutable modern snapshot metadata') ON CONFLICT(key) DO NOTHING", "catalog.snapshotModernBase:"+r.SHA256, string(raw)).Error; err != nil {
				return err
			}
		}
		if r.TextPacks != nil {
			raw, err := json.Marshal(r.TextPacks)
			if err != nil {
				return err
			}
			if err = tx.WithContext(ctx).Exec("INSERT INTO app_config(key,value,description) VALUES(?,?::jsonb,'Internal immutable snapshot manifest') ON CONFLICT(key) DO NOTHING", "catalog.snapshotManifest:"+r.SHA256, string(raw)).Error; err != nil {
				return err
			}
		}
		return tx.WithContext(ctx).Exec("INSERT INTO catalog_snapshots(format_version,seq,item_count,storage_key,url,sha256,bytes,created_at) VALUES(?,?,?,?,?,?,?,?)", r.FormatVersion, r.Seq, r.ItemCount, r.StorageKey, r.URL, r.SHA256, r.Bytes, r.CreatedAt).Error
	})
}

type Change struct {
	Seq, PackageID  int64
	Kind, Token, Op string
}

func (s *Store) CatalogChanges(ctx context.Context, since int64, limit int) ([]Change, error) {
	var rows []Change
	err := s.DB.WithContext(ctx).Raw(`SELECT * FROM (SELECT DISTINCT ON(kind,token) seq,package_id,kind,token,op FROM catalog_changes WHERE kind='cask' AND seq>? ORDER BY kind,token,seq DESC) latest ORDER BY seq LIMIT ?`, since, limit).Scan(&rows).Error
	return rows, err
}

// Acquire LockCatalogWrites before reading or modifying packages so cursor allocation follows transaction commit order.
func LockCatalogWrites(ctx context.Context, tx *gorm.DB) error {
	return tx.WithContext(ctx).Exec("SELECT pg_advisory_xact_lock(hashtextextended('catalog:changes',0))").Error
}
func (s *Store) PruneCatalogChanges(ctx context.Context, before time.Time) (int64, error) {
	var deleted int64
	err := s.WithTx(ctx, func(tx *gorm.DB) error {
		if err := LockCatalogWrites(ctx, tx); err != nil {
			return err
		}
		if err := tx.WithContext(ctx).Exec(`INSERT INTO app_config(key,value,description) SELECT 'catalog.retentionCursor',to_jsonb(COALESCE(max(seq),0)),'Internal catalog retention watermark' FROM catalog_changes WHERE kind<>'link' AND created_at<? ON CONFLICT(key) DO UPDATE SET value=to_jsonb(GREATEST(app_config.value::text::bigint,EXCLUDED.value::text::bigint)),updated_at=now()`, before).Error; err != nil {
			return err
		}
		r := tx.WithContext(ctx).Exec("DELETE FROM catalog_changes WHERE created_at<?", before)
		deleted = r.RowsAffected
		return r.Error
	})
	return deleted, err
}

type SnapshotText struct {
	PackageID            int64
	Locale               string
	DisplayName, Summary *string
}

func (s *Store) SnapshotTexts(ctx context.Context, ids []int64) ([]SnapshotText, error) {
	rows := []SnapshotText{}
	if len(ids) == 0 {
		return rows, nil
	}
	err := s.DB.WithContext(ctx).Raw("SELECT package_id,locale,NULLIF(display_name,'') AS display_name,NULLIF(summary,'') AS summary FROM package_i18n WHERE package_id IN ? AND (NULLIF(display_name,'') IS NOT NULL OR NULLIF(summary,'') IS NOT NULL) ORDER BY package_id,locale", ids).Scan(&rows).Error
	return rows, err
}
