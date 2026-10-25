package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"time"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/i18n"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ContentSource struct {
	Ref                content.Ref
	SourceLocale, Hash string
	Fields             map[string]any
	Targets            map[string]time.Time
	Protected          []string
}

func textWhere(ref content.Ref) (string, []any) {
	spec := content.Registry[ref.Entity]
	if ref.Entity == "announcement" {
		return spec.Key + "=?", []any{ref.Key()}
	}
	if ref.Entity == "collection_item" {
		return spec.Key + "=? AND package_id=?", []any{ref.ID, ref.SecondaryID}
	}
	return spec.Key + "=?", []any{ref.ID}
}
func (s *Store) ReadContentSource(ctx context.Context, ref content.Ref) (ContentSource, error) {
	out := ContentSource{Ref: ref, Fields: map[string]any{}, Targets: map[string]time.Time{}}
	if !ref.Valid() {
		return out, errors.New("invalid content reference")
	}
	if err := s.CheckContentApp(ctx, ref); err != nil {
		return out, err
	}
	if ref.Entity == "article" {
		var published bool
		if err := s.DB.WithContext(ctx).Raw("SELECT EXISTS(SELECT 1 FROM articles WHERE published_revision_id=? AND status='published')", ref.ID).Scan(&published).Error; err != nil {
			return out, err
		}
		if !published {
			return out, gorm.ErrRecordNotFound
		}
	}
	spec := content.Registry[ref.Entity]
	parentWhere, parentArgs := "id=?", []any{ref.ID}
	if ref.Entity == "collection_item" {
		parentWhere = "collection_id=? AND package_id=?"
		parentArgs = append(parentArgs, ref.SecondaryID)
	}
	if ref.Entity == "announcement" {
		var raw json.RawMessage
		if err := s.contentJSON(ctx, &raw, "SELECT COALESCE(value,'null'::jsonb) FROM app_config WHERE key=?", ref.Key()); err != nil {
			return out, err
		}
		var value map[string]any
		if json.Unmarshal(raw, &value) != nil || value == nil {
			return out, gorm.ErrRecordNotFound
		}
		out.SourceLocale, _ = value["sourceLocale"].(string)
		if out.SourceLocale == "" {
			out.SourceLocale = string(i18n.AuthoringLocale)
		}
	} else {
		if err := s.DB.WithContext(ctx).Table(spec.Parent).Select("source_locale").Where(parentWhere, parentArgs...).Scan(&out.SourceLocale).Error; err != nil {
			return out, err
		}
	}
	if !i18n.Valid(out.SourceLocale) {
		return out, gorm.ErrRecordNotFound
	}
	where, args := textWhere(ref)
	args = append(args, out.SourceLocale)
	var raw json.RawMessage
	if err := s.contentJSON(ctx, &raw, "SELECT to_jsonb(i) FROM "+spec.Table+" i WHERE "+where+" AND locale=?", args...); err != nil {
		return out, err
	}
	if len(raw) == 0 {
		return out, gorm.ErrRecordNotFound
	}
	var row map[string]any
	if err := json.Unmarshal(raw, &row); err != nil {
		return out, err
	}
	if ref.Entity == "package" && row["source"] == "homebrew" {
		return out, gorm.ErrRecordNotFound
	}
	for field := range spec.Fields {
		out.Fields[field] = row[field]
	}
	if ref.Entity == "release" {
		var kind string
		if err := s.DB.WithContext(ctx).Raw("SELECT source FROM releases WHERE id=?", ref.ID).Scan(&kind).Error; err != nil {
			return out, err
		}
		// Full upstream text remains reference source data; default summary backfills neither translate nor clear it.
		if kind != "editorial" {
			delete(out.Fields, "body_markdown")
		}
	}
	hashFields := out.Fields
	if ref.Entity == "category" || ref.Entity == "collection" {
		// Adding optional SEO columns must not stale every existing translation.
		// Keep nil fields in the snapshot so clearing authored metadata still
		// clears it in translations, but preserve the pre-migration fingerprint.
		hashFields = maps.Clone(out.Fields)
		for _, field := range []string{"seo_title", "seo_description"} {
			if hashFields[field] == nil {
				delete(hashFields, field)
			}
		}
	}
	out.Hash = content.Hash(out.SourceLocale, hashFields)
	var targets []struct {
		Locale    string
		UpdatedAt time.Time
	}
	where, args = textWhere(ref)
	locales := []string{}
	for _, code := range i18n.SiteLocales {
		if ref.Entity != "desktop_release" || i18n.ValidDesktop(string(code)) {
			locales = append(locales, string(code))
		}
	}
	languageJSON, _ := json.Marshal(locales)
	if err := s.DB.WithContext(ctx).Raw("SELECT lang.locale,coalesce(i.updated_at,now()) updated_at FROM jsonb_array_elements_text(?::jsonb) lang(locale) LEFT JOIN (SELECT * FROM "+spec.Table+" WHERE "+where+") i ON i.locale=lang.locale WHERE lang.locale<>? AND (i.locale IS NULL OR i.source_hash IS DISTINCT FROM ? OR i.status NOT IN ('manual','machine'))", append([]any{string(languageJSON)}, append(args, out.SourceLocale, out.Hash)...)...).Scan(&targets).Error; err != nil {
		return out, err
	}
	for _, target := range targets {
		if target.Locale != "zh-Hant" {
			out.Targets[target.Locale] = target.UpdatedAt
		}
	}
	if ref.Entity == "package" || ref.Entity == "release" || ref.Entity == "screenshot" || ref.Entity == "collection_item" {
		query := "SELECT name,token FROM packages WHERE id=?"
		id := ref.ID
		switch ref.Entity {
		case "release":
			query = "SELECT p.name,p.token FROM packages p JOIN releases r ON r.package_id=p.id WHERE r.id=?"
		case "screenshot":
			query = "SELECT p.name,p.token FROM packages p JOIN package_screenshots r ON r.package_id=p.id WHERE r.id=?"
		case "collection_item":
			id = ref.SecondaryID
		}
		var names struct{ Name, Token string }
		if err := s.DB.WithContext(ctx).Raw(query, id).Scan(&names).Error; err != nil {
			return out, err
		}
		if names.Name != "" {
			out.Protected = append(out.Protected, names.Name)
		}
		if names.Token != "" && names.Token != names.Name {
			out.Protected = append(out.Protected, names.Token)
		}
	}
	return out, nil
}

// ScheduleContentTranslation records a source snapshot and hands work to Hermes.
// Callers must already be inside the business transaction. Nonempty source updates keep published target rows until a validated replacement
// arrives. An empty source removes translations immediately.
func (s *Store) ScheduleContentTranslation(ctx context.Context, ref content.Ref) error {
	if !ref.Valid() {
		return domain.Validation()
	}
	if err := s.DB.WithContext(ctx).Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "content:"+ref.Entity+":"+ref.Key()).Error; err != nil {
		return err
	}
	source, err := s.ReadContentSource(ctx, ref)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !i18n.Writable(source.SourceLocale) {
		return domain.Rejected("source_locale_read_only")
	}
	spec := content.Registry[ref.Entity]
	where, args := textWhere(ref)
	if err := s.DB.WithContext(ctx).Table(spec.Table).Where(where+" AND locale=?", append(args, source.SourceLocale)...).Updates(map[string]any{"status": "source", "source_hash": source.Hash, "source_locale": source.SourceLocale}).Error; err != nil {
		return err
	}
	fields, _ := json.Marshal(source.Fields)
	encodedRef, _ := json.Marshal(ref)
	if err := s.DB.WithContext(ctx).Exec(`INSERT INTO content_translation_sources(entity,object_key,ref,source_locale,source_hash,fields) VALUES(?,?,?::jsonb,?,?,?::jsonb) ON CONFLICT(entity,object_key) DO UPDATE SET ref=EXCLUDED.ref,source_locale=EXCLUDED.source_locale,source_hash=EXCLUDED.source_hash,fields=EXCLUDED.fields,updated_at=clock_timestamp() WHERE content_translation_sources.source_hash IS DISTINCT FROM EXCLUDED.source_hash`, ref.Entity, ref.Key(), string(encodedRef), source.SourceLocale, source.Hash, string(fields)).Error; err != nil {
		return err
	}
	entity := ref.Entity + ":" + ref.Key()
	if err := s.DB.WithContext(ctx).Exec(`UPDATE seo_tasks SET state='expired',lease_owner=NULL,lease_until=NULL,updated_at=now() WHERE kind='translate' AND entity_key=? AND state IN ('open','leased','deferred') AND evidence_hash<>?`, entity, source.Hash).Error; err != nil {
		return err
	}
	if len(content.Flatten(source.Fields)) == 0 {
		return s.DB.WithContext(ctx).Table(spec.Table).Where(where+" AND locale<>?", append(args, source.SourceLocale)...).Delete(map[string]any{}).Error
	}
	// Translation follows supported content languages, independently of search indexing.
	for _, code := range i18n.SiteLocales {
		locale := string(code)
		if locale == "zh-Hant" || locale == source.SourceLocale || (ref.Entity == "desktop_release" && !i18n.ValidDesktop(locale)) {
			continue
		}
		var fresh bool
		if err := s.DB.WithContext(ctx).Raw("SELECT EXISTS(SELECT 1 FROM "+spec.Table+" WHERE "+where+" AND locale=? AND status IN ('manual','machine') AND source_hash=?)", append(args, locale, source.Hash)...).Scan(&fresh).Error; err != nil {
			return err
		}
		if fresh {
			continue
		}
		evidence, _ := json.Marshal(map[string]any{"ref": ref, "sourceLocale": source.SourceLocale, "sourceHash": source.Hash})
		key := "translate:" + entity + ":" + locale + ":" + source.Hash
		if err := s.DB.WithContext(ctx).Exec(`INSERT INTO seo_tasks(kind,entity_key,locale,dedupe_key,priority,evidence,evidence_hash,rule_version) VALUES('translate',?,?,?,?,?::jsonb,?,'translation-v1') ON CONFLICT(dedupe_key) DO UPDATE SET state='open',priority=greatest(seo_tasks.priority,EXCLUDED.priority),fencing=seo_tasks.fencing+1,lease_owner=NULL,lease_until=NULL,next_retry_at=NULL,updated_at=now() WHERE seo_tasks.state IN ('done','expired','skipped')`, entity, locale, key, translationPriority[ref.Entity], string(evidence), source.Hash).Error; err != nil {
			return err
		}
	}
	if _, err = s.DeriveTraditional(ctx, ref); err != nil {
		return err
	}
	// Restored collection snapshots may predate derived setup criteria.
	if ref.Entity == "collection" {
		_, err = s.deriveAuxiliaryTraditional(ctx, "collection_setup", ref.Key())
	}
	return err
}

// Cancel outstanding work when the source object disappears.
func (s *Store) ForgetContentTranslation(ctx context.Context, ref content.Ref) error {
	if !ref.Valid() {
		return domain.Validation()
	}
	if err := s.DB.WithContext(ctx).Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "content:"+ref.Entity+":"+ref.Key()).Error; err != nil {
		return err
	}
	if err := s.DB.WithContext(ctx).Exec("DELETE FROM content_translation_sources WHERE entity=? AND object_key=?", ref.Entity, ref.Key()).Error; err != nil {
		return err
	}
	return s.DB.WithContext(ctx).Exec("UPDATE seo_tasks SET state='expired',lease_owner=NULL,lease_until=NULL,updated_at=now() WHERE kind='translate' AND entity_key=? AND state IN ('open','leased','deferred')", ref.Entity+":"+ref.Key()).Error
}

// Current content uses the translation engine's source hash; historical records without source rows retain compatible metadata.
func (s *Store) withContentStale(ctx context.Context, raw json.RawMessage, ref content.Ref) (json.RawMessage, error) {
	source, err := s.ReadContentSource(ctx, ref)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return raw, nil
	}
	if err != nil {
		return nil, err
	}
	var detail map[string]json.RawMessage
	if err := json.Unmarshal(raw, &detail); err != nil {
		return nil, err
	}
	var translations []map[string]any
	if err := json.Unmarshal(detail["i18n"], &translations); err != nil {
		return nil, err
	}
	for _, row := range translations {
		row["stale"] = row["sourceHash"] != source.Hash
	}
	detail["i18n"], err = json.Marshal(translations)
	if err != nil {
		return nil, err
	}
	return json.Marshal(detail)
}

// Manual corrections record the current source version so successful corrections are not still marked stale.
func (s *Store) StampContentHash(ctx context.Context, ref content.Ref, locale string) error {
	source, err := s.ReadContentSource(ctx, ref)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	where, args := textWhere(ref)
	if err := s.DB.WithContext(ctx).Table(content.Registry[ref.Entity].Table).Where(where+" AND locale=?", append(args, locale)...).Update("source_hash", source.Hash).Error; err != nil {
		return err
	}
	if locale == "zh-CN" {
		_, err = s.DeriveTraditional(ctx, ref)
	}
	return err
}
func (s *Store) ContentGlossary(ctx context.Context) (map[string]map[string]string, []string, error) {
	var rows []struct {
		Term           string
		Translations   json.RawMessage
		DoNotTranslate bool
	}
	err := s.DB.WithContext(ctx).Table("i18n_glossary").Order("id").Find(&rows).Error
	if err != nil {
		return nil, nil, err
	}
	out := map[string]map[string]string{}
	protected := []string{}
	for _, r := range rows {
		if r.DoNotTranslate {
			protected = append(protected, r.Term)
		} else {
			var translated map[string]string
			if err := json.Unmarshal(r.Translations, &translated); err != nil {
				return nil, nil, err
			}
			out[r.Term] = translated
		}
	}
	return out, protected, nil
}

// Announcements retain their JSON contract; text tables contain only translatable fields, never links, dates or identifiers.
func (s *Store) ScheduleAnnouncement(ctx context.Context) error {
	var raw json.RawMessage
	if err := s.contentJSON(ctx, &raw, "SELECT COALESCE(value,'null'::jsonb) FROM app_config WHERE key='desktop.announcement'"); err != nil {
		return err
	}
	var data map[string]any
	if json.Unmarshal(raw, &data) != nil || data == nil {
		return nil
	}
	source, _ := data["sourceLocale"].(string)
	if source == "" {
		source = string(i18n.AuthoringLocale)
	}
	if !i18n.Writable(source) {
		return fmt.Errorf("invalid announcement locale")
	}
	fields, ok := data[source].(map[string]any)
	if !ok {
		fields = data
	}
	row := map[string]any{"config_key": "desktop.announcement", "locale": source, "source_locale": source, "status": "source"}
	for _, key := range []string{"title", "body"} {
		if v, ok := fields[key].(string); ok {
			row[key] = v
		}
	}
	if len(row) == 4 {
		return nil
	}
	if err := s.DB.WithContext(ctx).Table("announcement_i18n").Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "config_key"}, {Name: "locale"}}, DoUpdates: clause.Assignments(row)}).Create(row).Error; err != nil {
		return err
	}
	return s.ScheduleContentTranslation(ctx, content.Ref{Entity: "announcement"})
}
func (s *Store) LocalizeAnnouncement(ctx context.Context, locale string) (json.RawMessage, error) {
	var raw json.RawMessage
	err := s.contentJSON(ctx, &raw, `SELECT jsonb_build_object('title',i.title,'body',i.body,'sourceLocale',i.source_locale,'machineTranslated',i.machine_translated) FROM announcement_i18n i WHERE config_key='desktop.announcement' AND locale IN (?,source_locale,'en-US') AND coalesce(title,body,'')<>'' ORDER BY `+announcementLocaleOrder+` LIMIT 1`, locale, locale)
	return raw, err
}

func (s *Store) contentJSON(ctx context.Context, out *json.RawMessage, query string, args ...any) error {
	err := s.DB.WithContext(ctx).Raw(query, args...).Row().Scan(out)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}
