package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/facts"
	"gorm.io/gorm"
)

type BackgroundOwner struct {
	PackageID              int64
	Kind, Phase, SourceURL string
	CheckedAt              time.Time
	Developer              *string
	Visible                bool
}

func (s *Store) BackgroundLabels(ctx context.Context, q string, current, size int) ([]string, int64, error) {
	labels := []string{}
	var total int64
	where := ` FROM background_items b WHERE octet_length(b.label) BETWEEN 1 AND 255 AND b.label=btrim(b.label) AND b.label !~ $label$[/\\#[:cntrl:]]$label$ AND position(chr(63) in b.label)=0 AND b.label ILIKE '%'||?||'%' AND EXISTS(SELECT 1 FROM background_item_packages x JOIN packages p ON p.id=x.package_id WHERE x.label=b.label AND p.kind='cask' AND p.removed_at IS NULL)`
	if err := s.DB.WithContext(ctx).Raw("SELECT count(*)"+where, q).Scan(&total).Error; err != nil {
		return nil, 0, err
	}
	// Group before pagination, using the same visible, trimmed developer values
	// returned by the detail service. Shared vendors form one separate group.
	groups := `SELECT b.label,(SELECT count(DISTINCT NULLIF(btrim(m.developer),'')) FROM background_item_packages x
 JOIN packages p ON p.id=x.package_id LEFT JOIN package_meta m ON m.package_id=p.id WHERE x.label=b.label AND ` + publicVisible + `) AS developer_count,
 (SELECT min(NULLIF(btrim(m.developer),'') COLLATE "C") FROM background_item_packages x
 JOIN packages p ON p.id=x.package_id LEFT JOIN package_meta m ON m.package_id=p.id WHERE x.label=b.label AND ` + publicVisible + `) AS developer` + where
	err := s.DB.WithContext(ctx).Raw(`SELECT label FROM (`+groups+`) grouped
 ORDER BY CASE WHEN developer_count=0 THEN 2 WHEN developer_count>1 THEN 1 ELSE 0 END,
 CASE WHEN developer_count=1 THEN lower(developer) ELSE '' END COLLATE "C",
 CASE WHEN developer_count=1 THEN developer ELSE '' END COLLATE "C",lower(label) COLLATE "C",label COLLATE "C" LIMIT ? OFFSET ?`, q, size, (current-1)*size).Scan(&labels).Error
	return labels, total, err
}
func (s *Store) BackgroundOwners(ctx context.Context, label string) ([]BackgroundOwner, error) {
	rows := []BackgroundOwner{}
	err := s.DB.WithContext(ctx).Raw(`SELECT x.package_id,x.kind,x.phase,x.source_url,p.homebrew_checked_at AS checked_at,m.developer,NOT COALESCE(m.hidden,false) AS visible FROM background_item_packages x JOIN packages p ON p.id=x.package_id LEFT JOIN package_meta m ON m.package_id=p.id WHERE x.label=? AND p.kind='cask' AND p.removed_at IS NULL ORDER BY x.package_id,x.kind,x.phase`, label).Scan(&rows).Error
	return rows, err
}

func (s *Store) BackgroundReadyLocales(ctx context.Context, label string, now time.Time) ([]string, error) {
	var rows []struct {
		Locale   string
		Evidence json.RawMessage
	}
	if err := s.DB.WithContext(ctx).Raw(`SELECT locale,evidence FROM background_item_content WHERE label=? AND btrim(description)<>'' ORDER BY locale COLLATE "C"`, label).Scan(&rows).Error; err != nil {
		return nil, err
	}
	locales := []string{}
	for _, row := range rows {
		var proof facts.Proof
		if json.Unmarshal(row.Evidence, &proof) == nil && proof.At(now).Status == "verified" {
			locales = append(locales, row.Locale)
		}
	}
	return locales, nil
}
func (s *Store) BackgroundContent(ctx context.Context, label, locale string) (*facts.BackgroundContent, error) {
	var row struct {
		Locale, Description, ItemType string
		CommonPaths, Evidence         []byte
	}
	err := s.DB.WithContext(ctx).Raw(`SELECT locale,description,item_type,common_paths,evidence FROM background_item_content WHERE label=? AND locale IN (?,'en-US') ORDER BY (locale=?) DESC LIMIT 1`, label, locale, locale).Scan(&row).Error
	if err != nil || row.Locale == "" {
		return nil, err
	}
	content := &facts.BackgroundContent{Locale: row.Locale, Description: row.Description, ItemType: row.ItemType}
	if err = json.Unmarshal(row.CommonPaths, &content.CommonPaths); err != nil {
		return nil, err
	}
	err = json.Unmarshal(row.Evidence, &content.Evidence)
	return content, err
}
func (s *Store) SaveBackgroundContent(ctx context.Context, label string, v facts.BackgroundContent) error {
	if v.Locale == "zh-Hant" {
		return domain.Rejected("derived_locale_read_only:zh-Hant:zh-CN")
	}

	var exists bool
	if err := s.DB.WithContext(ctx).Raw("SELECT EXISTS(SELECT 1 FROM background_item_packages WHERE label=?)", label).Scan(&exists).Error; err != nil {
		return err
	}
	if !exists {
		return gorm.ErrRecordNotFound
	}
	paths, err := json.Marshal(v.CommonPaths)
	if err != nil {
		return err
	}
	proof, err := json.Marshal(v.Evidence)
	if err != nil {
		return err
	}
	if err := s.DB.WithContext(ctx).Exec(`INSERT INTO background_item_content(label,locale,description,item_type,common_paths,evidence) VALUES(?,?,?,?,?::jsonb,?::jsonb)
 ON CONFLICT(label,locale) DO UPDATE SET description=EXCLUDED.description,item_type=EXCLUDED.item_type,common_paths=EXCLUDED.common_paths,evidence=EXCLUDED.evidence`, label, v.Locale, v.Description, v.ItemType, string(paths), string(proof)).Error; err != nil {
		return err
	}
	if v.Locale == "zh-CN" {
		return s.deriveAuxiliaryTraditional(ctx, "background_item", label)
	}
	return nil
}
