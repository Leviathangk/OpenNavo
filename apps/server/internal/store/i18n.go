package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/i18n"
	"gorm.io/gorm/clause"
)

type textSpec struct {
	parent, table, key string
	fields             map[string]string
}

var textSpecs = map[string]textSpec{
	"category":        {"categories", "category_i18n", "category_id", map[string]string{"name": "name", "description": "description"}},
	"collection":      {"collections", "collection_i18n", "collection_id", map[string]string{"title": "title", "subtitle": "subtitle", "body": "body"}},
	"feature":         {"features", "feature_i18n", "feature_id", map[string]string{"badge": "badge", "title": "title", "subtitle": "subtitle", "body": "body", "ctaLabel": "cta_label"}},
	"screenshot":      {"package_screenshots", "screenshot_i18n", "screenshot_id", map[string]string{"caption": "caption"}},
	"collection_item": {"collection_items", "collection_item_i18n", "collection_id", map[string]string{"note": "note"}},
	"desktop_release": {"desktop_releases", "desktop_release_i18n", "desktop_release_id", map[string]string{"notes": "notes"}},
	"mirror":          {"mirrors", "mirror_i18n", "mirror_id", map[string]string{"name": "name"}},
}

// Callers invoke this within the content transaction; identifiers come only from the fixed registry.
func (s *Store) WriteLocalized(ctx context.Context, entity string, id, secondaryID int64, body map[string]any, required bool) error {
	spec, ok := textSpecs[entity]
	if !ok {
		return fmt.Errorf("unknown localized entity %q", entity)
	}
	source, _ := body["sourceLocale"].(string)
	raw, present := body["i18n"]
	if !present && source == "" && !required {
		return nil
	}
	values, ok := raw.(map[string]any)
	if !ok || !i18n.Valid(source) || (entity == "desktop_release" && !i18n.ValidApp(source)) || len(values) == 0 {
		return domain.Validation()
	}
	if _, ok := values[source].(map[string]any); !ok {
		return domain.Validation()
	}
	for locale, value := range values {
		if !i18n.Valid(locale) || (entity == "desktop_release" && !i18n.ValidApp(locale)) {
			return domain.Validation()
		}
		if value == nil && locale != source && (entity == "collection" || entity == "feature") {
			continue
		}
		if _, ok := value.(map[string]any); !ok {
			return domain.Validation()
		}
	}
	parentWhere, parentArgs := "id=?", []any{id}
	keys := []clause.Column{{Name: spec.key}, {Name: "locale"}}
	if entity == "collection_item" {
		parentWhere = "collection_id=? AND package_id=?"
		parentArgs = append(parentArgs, secondaryID)
		keys = append(keys, clause.Column{Name: "package_id"})
	}
	if err := s.DB.WithContext(ctx).Table(spec.parent).Where(parentWhere, parentArgs...).Update("source_locale", source).Error; err != nil {
		return err
	}
	textWhere, textArgs := spec.key+"=?", []any{id}
	if entity == "collection_item" {
		textWhere += " AND package_id=?"
		textArgs = append(textArgs, secondaryID)
	}
	args := append([]any{source, source}, textArgs...)
	if err := s.DB.WithContext(ctx).Exec("UPDATE "+spec.table+" SET source_locale=?,status=CASE WHEN locale=? THEN 'source' WHEN status='source' THEN 'manual' ELSE status END WHERE "+textWhere, args...).Error; err != nil {
		return err
	}
	sourceJSON, err := json.Marshal(values[source])
	if err != nil {
		return err
	}
	sum := sha256.Sum256(sourceJSON)
	hash := hex.EncodeToString(sum[:])
	for locale, value := range values {
		if value == nil {
			if err := s.DB.WithContext(ctx).Table(spec.table).Where(spec.key+"=? AND locale=?", id, locale).Delete(map[string]any{}).Error; err != nil {
				return err
			}
			continue
		}
		row := map[string]any{spec.key: id, "locale": locale, "source_locale": source, "source_hash": hash, "status": "manual", "updated_at": time.Now().UTC()}
		if entity == "collection_item" {
			row["package_id"] = secondaryID
		}
		if locale == source {
			row["status"] = "source"
		}
		fields := value.(map[string]any)
		for key, column := range spec.fields {
			if v, exists := fields[key]; exists {
				row[column] = v
			}
		}
		updates := []string{"source_locale", "source_hash", "status", "updated_at"}
		for _, column := range spec.fields {
			if _, exists := row[column]; exists {
				updates = append(updates, column)
			}
		}
		if err := s.DB.WithContext(ctx).Table(spec.table).Clauses(clause.OnConflict{Columns: keys, DoUpdates: clause.AssignmentColumns(updates)}).Create(row).Error; err != nil {
			return err
		}
	}
	return s.ScheduleContentTranslation(ctx, content.Ref{Entity: entity, ID: id, SecondaryID: secondaryID})
}

// Single-language corrections preserve the source language by default; explicit changes must match the write path.
func (s *Store) TextSourceLocale(ctx context.Context, entity string, id int64, locale string, body map[string]any) (string, error) {
	parent, key := "packages", "package_id"
	if entity == "release" {
		parent, key = "releases", "release_id"
	} else if entity != "package" {
		return "", domain.Validation()
	}
	if !i18n.Valid(locale) || (entity == "desktop_release" && !i18n.ValidApp(locale)) {
		return "", domain.Validation()
	}
	var source string
	if err := s.DB.WithContext(ctx).Table(parent).Select("source_locale").Where("id=?", id).Scan(&source).Error; err != nil {
		return "", err
	}
	if source == "" {
		return "", &domain.AppError{Code: domain.CodeNotFound, HTTPStatus: 404}
	}
	if value, present := body["sourceLocale"]; present {
		requested, ok := value.(string)
		if !ok || requested != locale {
			return "", domain.Validation()
		}
		source = requested
		if err := s.DB.WithContext(ctx).Table(parent).Where("id=?", id).Update("source_locale", source).Error; err != nil {
			return "", err
		}
		if err := s.DB.WithContext(ctx).Exec("UPDATE "+entity+"_i18n SET source_locale=?,status=CASE WHEN locale=? THEN 'source' WHEN status='source' THEN 'manual' ELSE status END WHERE "+key+"=?", source, source, id).Error; err != nil {
			return "", err
		}
	}
	return source, nil
}
