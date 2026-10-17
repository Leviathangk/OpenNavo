package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/facts"
	"github.com/opennavo/opennavo/server/internal/homebrew"
)

// PackageFacts is one cask's WP12 facts with the Homebrew requirements they are compared with.
type PackageFacts struct {
	PackageID int64
	Price     *facts.Price
	Macos     *facts.Macos
	UpdatedAt *time.Time
	Homebrew  facts.Homebrew
}

// AlternativeRow is one stored alternative with what admin needs to list it.
type AlternativeRow struct {
	facts.Alternative
	Token, Name, DisplayName string
	IconURL                  *string
	Source                   string
	// Available: visible, not removed and not disabled, so public pages show it.
	Available bool
}

// PackageFacts reads a cask's facts; found is false when the cask does not exist.
func (s *Store) PackageFacts(ctx context.Context, id int64) (PackageFacts, bool, error) {
	var row struct {
		ID            int64
		MinMacos      *string
		SupportsArm64 bool
		SupportsX8664 bool `gorm:"column:supports_x86_64"`
		Dependencies  []byte
		Price, Macos  []byte
		UpdatedAt     *time.Time
	}
	err := s.DB.WithContext(ctx).Raw(`SELECT p.id,p.min_macos,p.supports_arm64,p.supports_x86_64,p.dependencies,f.price,f.macos,f.updated_at
 FROM packages p LEFT JOIN package_facts f ON f.package_id=p.id WHERE p.kind IN ('cask','link') AND p.id=?`, id).Scan(&row).Error
	if err != nil || row.ID == 0 {
		return PackageFacts{}, false, err
	}
	out := PackageFacts{PackageID: row.ID, UpdatedAt: row.UpdatedAt}
	if len(row.Price) > 0 {
		if err := json.Unmarshal(row.Price, &out.Price); err != nil {
			return out, false, fmt.Errorf("decode package price: %w", err)
		}
	}
	if len(row.Macos) > 0 {
		if err := json.Unmarshal(row.Macos, &out.Macos); err != nil {
			return out, false, fmt.Errorf("decode package macos: %w", err)
		}
	}
	var metadata homebrew.CaskPlatformMetadata
	if len(row.Dependencies) > 0 {
		if err := json.Unmarshal(row.Dependencies, &metadata); err != nil {
			return out, false, fmt.Errorf("decode cask platform metadata: %w", err)
		}
	}
	out.Homebrew = facts.HomebrewFrom(row.MinMacos, row.SupportsArm64, row.SupportsX8664, metadata)
	return out, true, nil
}

// SavePackageFact replaces the price or macos document; the row goes away once both are empty.
func (s *Store) SavePackageFact(ctx context.Context, id int64, column string, value any, actor *int64) error {
	if column != "price" && column != "macos" {
		return fmt.Errorf("unknown package fact %q", column)
	}
	if value == nil {
		other := map[string]string{"price": "macos", "macos": "price"}[column]
		if err := s.DB.WithContext(ctx).Exec("DELETE FROM package_facts WHERE package_id=? AND "+other+" IS NULL", id).Error; err != nil {
			return err
		}
		return s.DB.WithContext(ctx).Exec("UPDATE package_facts SET "+column+"=NULL,updated_at=now(),updated_by=? WHERE package_id=? AND "+column+" IS NOT NULL", actor, id).Error
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return s.DB.WithContext(ctx).Exec(`INSERT INTO package_facts(package_id,`+column+`,updated_by) VALUES(?,?::jsonb,?)
 ON CONFLICT (package_id) DO UPDATE SET `+column+`=EXCLUDED.`+column+`,updated_at=now(),updated_by=EXCLUDED.updated_by`, id, string(data), actor).Error
}

// ReplaceAlternatives stores the ordered list; rows carry no timestamps, so saving the same list
// again leaves the revision history unchanged.
func (s *Store) ReplaceAlternatives(ctx context.Context, id int64, items []facts.Alternative, source string) error {
	if err := s.DB.WithContext(ctx).Exec("DELETE FROM package_alternatives WHERE package_id=?", id).Error; err != nil {
		return err
	}
	for index, item := range items {
		reasons, _ := json.Marshal(item.Reasons)
		limits, _ := json.Marshal(item.Limits)
		if err := s.DB.WithContext(ctx).Exec(`INSERT INTO package_alternatives(package_id,alternative_id,sort,reasons,limits,best_for,note,source)
 VALUES(?,?,?,ARRAY(SELECT jsonb_array_elements_text(?::jsonb)),ARRAY(SELECT jsonb_array_elements_text(?::jsonb)),?,?,?)`,
			id, item.PackageID, index, string(reasons), string(limits), item.BestFor, item.Note, source).Error; err != nil {
			return MapAdminError(err)
		}
	}
	return nil
}

// AlternativeTargets returns which of the IDs are casks still in the catalog.
func (s *Store) AlternativeTargets(ctx context.Context, ids []int64) (map[int64]bool, error) {
	out := map[int64]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	var found []int64
	if err := s.DB.WithContext(ctx).Raw("SELECT id FROM packages WHERE kind IN ('cask','link') AND removed_at IS NULL AND id IN ?", ids).Scan(&found).Error; err != nil {
		return nil, err
	}
	for _, id := range found {
		out[id] = true
	}
	return out, nil
}

// Alternatives lists a cask's alternatives in display order; publicOnly keeps those pages show.
func (s *Store) Alternatives(ctx context.Context, id int64, publicOnly bool) ([]AlternativeRow, error) {
	var rows []struct {
		AlternativeID            int64
		Reasons, Limits          string
		BestFor, Note            *string
		Source                   string
		Token, Name, DisplayName string
		IconURL                  *string
		Available                bool
	}
	visibilityContext := ctx
	if !publicOnly {
		visibilityContext = domain.WithLinks(ctx, true)
	}
	visible := linkSQL(visibilityContext, publicVisible)
	condition := ""
	if publicOnly {
		condition = " AND " + visible + " AND NOT " + PackageDisabledSQL
	}
	err := s.DB.WithContext(ctx).Raw(`SELECT a.alternative_id,to_jsonb(a.reasons)::text AS reasons,to_jsonb(a.limits)::text AS limits,a.best_for,a.note,a.source,
 p.token,p.name,COALESCE(NULLIF(si.display_name,''),p.name) AS display_name,ia.url AS icon_url,
 (`+visible+` AND NOT `+PackageDisabledSQL+`) AS available
 FROM package_alternatives a JOIN packages p ON p.id=a.alternative_id LEFT JOIN package_meta m ON m.package_id=p.id
 LEFT JOIN package_i18n si ON si.package_id=p.id AND si.locale=p.source_locale LEFT JOIN assets ia ON ia.id=m.icon_asset_id
 WHERE a.package_id=?`+condition+` ORDER BY a.sort,a.alternative_id`, id).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]AlternativeRow, 0, len(rows))
	for _, row := range rows {
		item := AlternativeRow{Alternative: facts.Alternative{PackageID: row.AlternativeID, BestFor: row.BestFor, Note: row.Note},
			Token: row.Token, Name: row.Name, DisplayName: row.DisplayName, IconURL: row.IconURL, Source: row.Source, Available: row.Available}
		if err := json.Unmarshal([]byte(row.Reasons), &item.Reasons); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(row.Limits), &item.Limits); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}
