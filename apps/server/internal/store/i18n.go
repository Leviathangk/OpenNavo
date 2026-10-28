package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"time"
	"unicode/utf8"

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
	"category":        {"categories", "category_i18n", "category_id", map[string]string{"name": "name", "description": "description", "seoTitle": "seo_title", "seoDescription": "seo_description"}},
	"collection":      {"collections", "collection_i18n", "collection_id", map[string]string{"title": "title", "subtitle": "subtitle", "body": "body", "seoTitle": "seo_title", "seoDescription": "seo_description"}},
	"feature":         {"features", "feature_i18n", "feature_id", map[string]string{"badge": "badge", "title": "title", "subtitle": "subtitle", "body": "body", "ctaLabel": "cta_label"}},
	"screenshot":      {"package_screenshots", "screenshot_i18n", "screenshot_id", map[string]string{"caption": "caption"}},
	"collection_item": {"collection_items", "collection_item_i18n", "collection_id", map[string]string{"note": "note"}},
	"desktop_release": {"desktop_releases", "desktop_release_i18n", "desktop_release_id", map[string]string{"notes": "notes"}},
	"mirror":          {"mirrors", "mirror_i18n", "mirror_id", map[string]string{"name": "name"}},
}

// ValidateLocalizedFields checks only submitted fields, using API character limits.
// Unknown field names and invalid locales are never copied into rejection reasons.
func ValidateLocalizedFields(entity, locale string, fields map[string]any) error {
	_, ok := content.Registry[entity]
	if !ok {
		return domain.Rejected("localized_entity_invalid")
	}
	if !i18n.Writable(locale) || entity == "desktop_release" && !i18n.ValidDesktop(locale) {
		return domain.Rejected("locale_invalid")
	}
	return validateContentFields(entity, "."+locale, fields, true)
}

// External source and translation writers use the registry's character limits.
// Trusted conversion output keeps shape/type checks but may expand the prose.
// Sections are bounded by their shape and text leaves, never serialized JSON.
func validateContentFields(entity, localeSuffix string, fields map[string]any, enforceLengths bool) error {
	spec := content.Registry[entity]
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	check := func(field string, value any, limit int) error {
		if value == nil {
			return nil
		}
		text, ok := value.(string)
		if !ok {
			return domain.Rejected("field_type_invalid:" + entity + "." + field + localeSuffix)
		}
		if enforceLengths && utf8.RuneCountInString(text) > limit {
			return domain.Rejected("field_too_long:" + entity + "." + field + localeSuffix)
		}
		return nil
	}
	for _, key := range keys {
		column := textColumn(key)
		limit, ok := spec.Fields[column]
		if !ok {
			return domain.Rejected("unknown_field")
		}
		field, value := textAPIKey(column), fields[key]
		if column == "sections" {
			groups, ok := value.([]any)
			if !ok || len(groups) > 8 {
				return domain.Rejected("sections_invalid:sections")
			}
			for i, item := range groups {
				group, ok := item.(map[string]any)
				if !ok {
					return domain.Rejected("section_invalid:sections")
				}
				items, ok := group["items"].([]any)
				if !ok || len(items) > 6 {
					return domain.Rejected("section_items_invalid:sections.items")
				}
				for j, item := range items {
					if _, ok := item.(string); !ok {
						return domain.Rejected("section_item_invalid:sections.items")
					}
					if err := check(fmt.Sprintf("sections[%d].items[%d]", i, j), item, 80); err != nil {
						return err
					}
				}
				if _, ok := group["area"].(string); !ok {
					return domain.Rejected("section_area_invalid:sections.area")
				}
				if err := check(fmt.Sprintf("sections[%d].area", i), group["area"], 12); err != nil {
					return err
				}
			}
			continue
		}
		if err := check(field, value, limit); err != nil {
			return err
		}
	}
	if entity == "release" {
		return ValidateReleaseNotes(fields)
	}
	return nil
}

// Callers invoke this within the content transaction; identifiers come only from the fixed registry.
// Omitted locales retain their content; an explicit null deletes a non-source
// collection/feature translation. Supplied fields are upserted within each locale.
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
	if !ok || len(values) == 0 {
		return domain.Rejected("i18n_required")
	}
	if !i18n.Writable(source) || (entity == "desktop_release" && !i18n.ValidDesktop(source)) {
		return domain.Rejected("source_locale_invalid")
	}
	if _, ok := values[source].(map[string]any); !ok {
		return domain.Rejected("source_locale_text_required")
	}
	for locale, value := range values {
		if !i18n.Writable(locale) || (entity == "desktop_release" && !i18n.ValidDesktop(locale)) {
			return domain.Rejected("locale_invalid")
		}
		if value == nil && locale != source && (entity == "collection" || entity == "feature") {
			continue
		}
		fields, ok := value.(map[string]any)
		if !ok {
			return domain.Rejected("localized_fields_invalid")
		}
		if err := ValidateLocalizedFields(entity, locale, fields); err != nil {
			return err
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
	ref := content.Ref{Entity: entity, ID: id, SecondaryID: secondaryID}
	// Stamp supplied translations against the canonical, fully merged source, not
	// the submitted partial JSON. Derived Chinese must never consume a stale row.
	for locale := range values {
		if err := s.StampContentHash(ctx, ref, locale); err != nil {
			return err
		}
	}
	return s.ScheduleContentTranslation(ctx, ref)
}

// Single-language corrections preserve the source language by default; explicit changes must match the write path.
func (s *Store) TextSourceLocale(ctx context.Context, entity string, id int64, locale string, body map[string]any) (string, error) {
	parent, key := "packages", "package_id"
	if entity == "release" {
		parent, key = "releases", "release_id"
	} else if entity != "package" {
		return "", domain.Rejected("localized_entity_invalid")
	}
	if !i18n.Writable(locale) || (entity == "desktop_release" && !i18n.ValidDesktop(locale)) {
		return "", domain.Rejected("locale_invalid")
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
			return "", domain.Rejected("source_locale_mismatch")
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
