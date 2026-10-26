package store

import (
	"context"
	"encoding/json"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AssetRenditionSpec is the subset of a manifest entry needed to identify a
// rendition. Keep SQL candidate selection and the locked recheck identical.
type AssetRenditionSpec struct {
	Role   string `json:"role"`
	Type   string `json:"type"`
	Width  int    `json:"width"`
	Height int    `json:"height,omitempty"`
}

func RequiredAssetRenditions(kind string) []AssetRenditionSpec {
	if kind != "icon" && kind != "screenshot" {
		return nil
	}
	specs := []AssetRenditionSpec{{Role: "responsive", Type: "image/webp", Width: 64}, {Role: "responsive", Type: "image/webp", Width: 128}, {Role: "responsive", Type: "image/webp", Width: 256}}
	if kind == "screenshot" {
		specs = append(specs, AssetRenditionSpec{Role: "thumbnail", Type: "image/webp", Width: 318, Height: 200}, AssetRenditionSpec{Role: "thumbnail", Type: "image/webp", Width: 636, Height: 400})
	}
	return specs
}

func (spec AssetRenditionSpec) Matches(file AssetFile) bool {
	return spec.Role == file.Role && spec.Type == file.Type && spec.Width == file.Width && (spec.Height == 0 || spec.Height == file.Height)
}

func MissingAssetRenditions(kind string, files []AssetFile) []AssetRenditionSpec {
	missing := []AssetRenditionSpec{}
	for _, spec := range RequiredAssetRenditions(kind) {
		found := false
		for _, file := range files {
			if spec.Matches(file) {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, spec)
		}
	}
	return missing
}

func renditionCandidates(db *gorm.DB, kind string) *gorm.DB {
	icons, _ := json.Marshal(RequiredAssetRenditions("icon"))
	shots, _ := json.Marshal(RequiredAssetRenditions("screenshot"))
	query := db.Table("assets").Where("((kind='icon' AND NOT files @> ?::jsonb) OR (kind='screenshot' AND NOT files @> ?::jsonb))", string(icons), string(shots))
	if kind != "" {
		query = query.Where("kind=?", kind)
	}
	return query
}

// AssetRenditionSnapshot bounds this run: later uploads are left to the next run.
func (s *Store) AssetRenditionSnapshot(ctx context.Context, kind string) (count, throughID int64, err error) {
	var snapshot struct{ Count, ThroughID int64 }
	err = renditionCandidates(s.DB.WithContext(ctx), kind).Select("count(*) AS count, coalesce(max(id),0) AS through_id").Scan(&snapshot).Error
	return snapshot.Count, snapshot.ThroughID, err
}

func (s *Store) AssetRenditionPage(ctx context.Context, kind string, afterID, throughID int64, limit int) ([]int64, error) {
	var ids []int64
	err := renditionCandidates(s.DB.WithContext(ctx), kind).Where("id>? AND id<=?", afterID, throughID).Order("id").Limit(limit).Pluck("id", &ids).Error
	return ids, err
}

// AssetRenditionSource identifies the input used by both preparation and the
// locked recheck. Appending responsive/thumbnail entries cannot change it.
func AssetRenditionSource(asset Asset) (AssetFile, error) {
	var files []AssetFile
	if err := json.Unmarshal(asset.Files, &files); err != nil {
		return AssetFile{}, errors.New("invalid asset manifest")
	}
	for _, file := range files {
		if file.Role == "original" && file.StorageKey != "" {
			return file, nil
		}
	}
	// Pre-manifest assets still have a primary normalized object. Prefer it
	// over smaller secondary variants when no original is recorded.
	if asset.StorageKey != "" {
		return AssetFile{StorageKey: asset.StorageKey, SHA256: asset.SHA256, Width: asset.Width, Height: asset.Height, Type: asset.MIME, Bytes: asset.Bytes, URL: asset.URL, Role: "normalized"}, nil
	}
	for _, file := range files {
		if file.Role == "normalized" && file.StorageKey != "" {
			return file, nil
		}
	}
	return AssetFile{}, errors.New("no source object")
}

type AssetRenditionAppendResult struct {
	Added          int
	DeferredReason string
}

// AppendAssetRenditions accepts files already prepared and uploaded outside any
// transaction. The short row lock only protects the source/manifest recheck and
// JSONB append, preserving existing entries (including unknown fields). It never
// emits catalog changes or invalidations. A changed source or unmatched missing
// rendition is deferred to a later run, not treated as an asset failure.
func (s *Store) AppendAssetRenditions(ctx context.Context, prepared Asset, files []AssetFile) (AssetRenditionAppendResult, error) {
	var result AssetRenditionAppendResult
	source, err := AssetRenditionSource(prepared)
	if err != nil {
		return result, err
	}
	err = s.WithTx(ctx, func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL lock_timeout='5s'").Error; err != nil {
			return err
		}
		var asset Asset
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&asset, prepared.ID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				result.DeferredReason = "asset_deleted"
				return nil
			}
			return err
		}
		var existing []AssetFile
		if err := json.Unmarshal(asset.Files, &existing); err != nil {
			return errors.New("invalid asset manifest")
		}
		missing := MissingAssetRenditions(asset.Kind, existing)
		if len(missing) == 0 {
			return nil
		}
		currentSource, err := AssetRenditionSource(asset)
		if err != nil {
			return err
		}
		// The primary key determines the output directory even when an original
		// is selected. Compare its identity as well as the selected source file.
		if asset.Kind != prepared.Kind || asset.StorageKey != prepared.StorageKey || asset.SHA256 != prepared.SHA256 || currentSource != source {
			result.DeferredReason = "source_changed"
			return nil
		}
		additions := make([]AssetFile, 0, len(missing))
		for _, spec := range missing {
			found := false
			for _, file := range files {
				if spec.Matches(file) && file.StorageKey != "" && file.URL != "" && file.Height > 0 && file.Bytes > 0 && file.SHA256 != "" {
					additions = append(additions, file)
					found = true
					break
				}
			}
			if !found {
				result.DeferredReason = "prepared_renditions_mismatch"
			}
		}
		if len(additions) == 0 {
			return nil
		}
		raw, err := json.Marshal(additions)
		if err != nil {
			return err
		}
		if err := tx.Exec("UPDATE assets SET files=files || ?::jsonb WHERE id=?", string(raw), asset.ID).Error; err != nil {
			return err
		}
		result.Added = len(additions)
		return nil
	})
	if err != nil {
		return AssetRenditionAppendResult{}, err
	}
	return result, nil
}
