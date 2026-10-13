package store

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/i18n"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var textAPIFields = map[string]string{"displayName": "display_name", "bodyMarkdown": "body_markdown", "ctaLabel": "cta_label", "testNote": "test_note", "seoTitle": "seo_title", "seoDescription": "seo_description"}

func textColumn(key string) string {
	if v, ok := textAPIFields[key]; ok {
		return v
	}
	return key
}
func textAPIKey(col string) string {
	for k, v := range textAPIFields {
		if v == col {
			return k
		}
	}
	return col
}
func ValidateContentFields(entity string, fields map[string]any) error {
	spec, ok := content.Registry[entity]
	if !ok || len(fields) == 0 {
		return domain.Validation()
	}
	for key, value := range fields {
		col := textColumn(key)
		max, ok := spec.Fields[col]
		if !ok {
			return domain.Validation()
		}
		if col == "sections" {
			list, ok := value.([]any)
			if !ok || len(list) > 8 {
				return domain.Validation()
			}
			for _, item := range list {
				group, ok := item.(map[string]any)
				if !ok {
					return domain.Validation()
				}
				items, ok := group["items"].([]any)
				if !ok || len(items) > 6 {
					return domain.Validation()
				}
				for _, v := range items {
					if _, ok := v.(string); !ok {
						return domain.Validation()
					}
				}
			}
			if utf8.RuneCountInString(jsonText(value)) > max {
				return domain.Validation()
			}
			continue
		}
		if value == nil {
			continue
		}
		v, ok := value.(string)
		if !ok || utf8.RuneCountInString(v) > max {
			return domain.Validation()
		}
	}
	return nil
}
func (s *Store) WriteContentText(ctx context.Context, ref content.Ref, locale string, fields map[string]any, source bool) error {
	if !ref.Valid() || !i18n.Valid(locale) {
		return domain.Validation()
	}
	if err := ValidateContentFields(ref.Entity, fields); err != nil {
		return err
	}
	if err := s.CheckContentCask(ctx, ref); err != nil {
		return err
	}
	spec := content.Registry[ref.Entity]
	if err := s.DB.WithContext(ctx).Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "content:"+ref.Entity+":"+ref.Key()).Error; err != nil {
		return err
	}
	parentWhere, args := "id=?", []any{ref.ID}
	if ref.Entity == "collection_item" {
		parentWhere, args = "collection_id=? AND package_id=?", []any{ref.ID, ref.SecondaryID}
	}
	if ref.Entity == "announcement" {
		parentWhere, args = "key=?", []any{ref.Key()}
	}
	var count int64
	if err := s.DB.WithContext(ctx).Table(spec.Parent).Where(parentWhere, args...).Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return gorm.ErrRecordNotFound
	}
	if source {
		old, err := s.ReadContentSource(ctx, ref)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil && old.SourceLocale != locale && len(content.Flatten(fields)) == 0 {
			return domain.Validation()
		}
	}
	src := locale
	if source {
		if ref.Entity != "announcement" {
			if err := s.DB.WithContext(ctx).Table(spec.Parent).Where(parentWhere, args...).Update("source_locale", locale).Error; err != nil {
				return err
			}
		}
	} else {
		current, err := s.ReadContentSource(ctx, ref)
		if err != nil {
			return err
		}
		src = current.SourceLocale
		if src == locale {
			return domain.Validation()
		}
	}
	where, keysArgs := textWhere(ref)
	keys := []clause.Column{{Name: spec.Key}, {Name: "locale"}}
	row := map[string]any{spec.Key: ref.ID, "locale": locale, "source_locale": src, "status": "manual", "updated_at": time.Now().UTC(), "fail_reason": nil}
	if source {
		row["status"] = "source"
	}
	if ref.Entity == "announcement" {
		row[spec.Key] = ref.Key()
	}
	if ref.Entity == "collection_item" {
		row["package_id"] = ref.SecondaryID
		keys = append(keys, clause.Column{Name: "package_id"})
	}
	if ref.Entity == "package" {
		row["source"] = "human"
	}
	for key, v := range fields {
		col := textColumn(key)
		if col == "sections" {
			row[col] = gorm.Expr("?::jsonb", jsonText(v))
		} else {
			row[col] = v
		}
	}
	updates := make(map[string]any, len(row))
	for key, value := range row {
		updates[key] = value
	}
	// INSERT checks NOT NULL before ON CONFLICT; placeholders apply only to new rows and do not overwrite omitted existing fields.
	for _, col := range []string{"name", "title", "notes"} {
		if _, present := spec.Fields[col]; present {
			if _, supplied := row[col]; !supplied {
				row[col] = ""
			}
		}
	}
	if err := s.DB.WithContext(ctx).Table(spec.Table).Clauses(clause.OnConflict{Columns: keys, DoUpdates: clause.Assignments(updates)}).Create(row).Error; err != nil {
		return err
	}
	if source {
		if err := s.DB.WithContext(ctx).Exec("UPDATE "+spec.Table+" SET source_locale=?,status=CASE WHEN locale=? THEN 'source' WHEN status='source' THEN 'manual' ELSE status END WHERE "+where, append([]any{src, src}, keysArgs...)...).Error; err != nil {
			return err
		}
	}
	if err := s.StampContentHash(ctx, ref, locale); err != nil {
		return err
	}
	if source {
		if err := s.ScheduleContentTranslation(ctx, ref); err != nil {
			return err
		}
	}
	return s.ContentChanged(ctx, ref)
}
func (s *Store) ContentChanged(ctx context.Context, ref content.Ref) error {
	id := ref.ID
	switch ref.Entity {
	case "article", "article_author":
		var paths []string
		if ref.Entity == "article" {
			// Content refs identify revisions, not article identities. Only live text affects pages.
			var articleID int64
			if err := s.DB.WithContext(ctx).Raw("SELECT id FROM articles WHERE published_revision_id=? AND status='published'", id).Scan(&articleID).Error; err != nil {
				return err
			}
			if articleID == 0 {
				return nil
			}
			var err error
			paths, err = s.ArticlePaths(ctx, articleID)
			if err != nil {
				return err
			}
		} else {
			if err := s.DB.WithContext(ctx).Raw("SELECT '/authors/'||slug FROM article_authors WHERE id=?", id).Scan(&paths).Error; err != nil {
				return err
			}
			var bylines []string
			if err := s.DB.WithContext(ctx).Raw("SELECT '/articles/'||a.slug FROM articles a JOIN article_revisions r ON r.id=a.published_revision_id WHERE a.status='published' AND r.author_id=? ORDER BY a.id", id).Scan(&bylines).Error; err != nil {
				return err
			}
			paths = append(paths, bylines...)
		}
		if err := s.EnqueueArticlePages(ctx, paths); err != nil {
			return err
		}
		return QueueCacheInvalidation(ctx, s.DB, "translation:"+ref.Entity+":"+ref.Key(), ArticleCachePatterns(paths)...)
	case "package":
		if err := UpdateSearchIndex(ctx, s.DB, []int64{id}); err != nil {
			return err
		}
	case "release":
		if err := s.DB.WithContext(ctx).Raw("SELECT package_id FROM releases WHERE id=?", id).Scan(&id).Error; err != nil {
			return err
		}
	case "screenshot":
		if err := s.DB.WithContext(ctx).Raw("SELECT package_id FROM package_screenshots WHERE id=?", id).Scan(&id).Error; err != nil {
			return err
		}
	default:
		return nil
	}
	if id == 0 {
		return nil
	}
	return s.AppendContentChange(ctx, id)
}
func (s *Store) ContentView(ctx context.Context, ref content.Ref) (map[string]any, error) {
	if !ref.Valid() {
		return nil, domain.Validation()
	}
	if err := s.CheckContentCask(ctx, ref); err != nil {
		return nil, err
	}
	state, err := s.HistorySnapshot(ctx, ref)
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, gorm.ErrRecordNotFound
	}
	spec := content.Registry[ref.Entity]
	source := string(i18n.AuthoringLocale)
	parent := state[spec.Parent][0]
	if v, ok := parent["source_locale"].(string); ok {
		source = v
	}
	if ref.Entity == "announcement" {
		if data, ok := parent["value"].(map[string]any); ok {
			if v, ok := data["sourceLocale"].(string); ok {
				source = v
			}
		}
	}
	hash := ""
	src, readErr := s.ReadContentSource(ctx, ref)
	if readErr == nil {
		hash = src.Hash
		source = src.SourceLocale
	} else if !errors.Is(readErr, gorm.ErrRecordNotFound) {
		return nil, readErr
	}
	locales := map[string]any{}
	for _, code := range i18n.Locales {
		fields := map[string]any{}
		for col := range spec.Fields {
			if col == "sections" {
				fields[col] = []any{}
			} else {
				fields[textAPIKey(col)] = nil
			}
		}
		locales[string(code)] = map[string]any{"status": "missing", "stale": false, "sourceHash": nil, "updatedAt": nil, "translatedAt": nil, "machineTranslated": false, "model": nil, "failReason": nil, "fields": fields}
	}
	updated := parent["updated_at"]
	if updated == nil {
		updated = parent["created_at"]
	}
	if updated == nil {
		updated = time.Now().UTC()
	}
	label := ref.Entity + " #" + ref.Key()
	for _, row := range state[spec.Table] {
		locale, _ := row["locale"].(string)
		if _, ok := locales[locale]; !ok {
			continue
		}
		fields := map[string]any{}
		for col := range spec.Fields {
			fields[textAPIKey(col)] = row[col]
		}
		status, _ := row["status"].(string)
		stale := locale != source && hash != "" && row["source_hash"] != hash
		locales[locale] = map[string]any{"status": status, "stale": stale, "sourceHash": row["source_hash"], "updatedAt": row["updated_at"], "translatedAt": row["translated_at"], "machineTranslated": row["machine_translated"] == true, "model": row["model"], "failReason": row["fail_reason"], "fields": fields}
		if locale == source {
			for _, k := range []string{"display_name", "title", "name"} {
				if v, ok := row[k].(string); ok && strings.TrimSpace(v) != "" {
					label = v
					break
				}
			}
		}
	}
	sourceHash := hash
	if utf8.RuneCountInString(label) > 200 {
		label = string([]rune(label)[:200])
	}
	return map[string]any{"ref": ref, "label": label, "sourceLocale": source, "sourceHash": sourceHash, "i18n": locales, "webUrl": nil, "updatedAt": updated}, nil
}

// Generic translation endpoints also enforce the Cask boundary; entity types cannot bypass package restrictions.
func (s *Store) CheckContentCask(ctx context.Context, ref content.Ref) error {
	query := ""
	id := ref.ID
	switch ref.Entity {
	case "package":
		query = "SELECT count(*) FROM packages WHERE id=? AND kind='cask'"
	case "release":
		query = "SELECT count(*) FROM releases r JOIN packages p ON p.id=r.package_id WHERE r.id=? AND p.kind='cask'"
	case "screenshot":
		query = "SELECT count(*) FROM package_screenshots r JOIN packages p ON p.id=r.package_id WHERE r.id=? AND p.kind='cask'"
	case "category":
		query = "SELECT count(*) FROM categories WHERE id=? AND applies_to<>'formula'"
	case "feature":
		query = "SELECT count(*) FROM features f WHERE id=? AND (target_type<>'package' OR EXISTS(SELECT 1 FROM packages p WHERE p.id=f.package_id AND p.kind='cask'))"
	case "collection_item":
		id = ref.SecondaryID
		query = "SELECT count(*) FROM packages WHERE id=? AND kind='cask'"
	default:
		return nil
	}
	var n int64
	if err := s.DB.WithContext(ctx).Raw(query, id).Scan(&n).Error; err != nil {
		return err
	}
	if n == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
