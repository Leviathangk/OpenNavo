package management

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/i18n"
	"github.com/opennavo/opennavo/server/internal/store"
)

func (s *Service) hermesText(ctx context.Context, op string, in Input) (any, error) {
	ref := content.Ref{Entity: "package", ID: in.ID}
	fields := map[string]any{}
	for k, v := range in.Body {
		if k != "sourceLocale" && k != "enabled" {
			fields[k] = v
		}
	}
	switch op {
	case "UpdateAnnouncement", "GetAnnouncement":
		ref = content.Ref{Entity: "announcement"}
	case "UpdateDesktopReleaseNotes":
		ref.Entity = "desktop_release"
	}
	if op == "GetAnnouncement" {
		view, err := s.translationView(ctx, ref)
		if err != nil {
			return nil, store.MapAdminError(err)
		}
		var enabled bool
		if err := s.Store.DB.WithContext(ctx).Raw(`SELECT COALESCE(jsonb_typeof(value)='object' AND value->'enabled'='true'::jsonb,false) FROM app_config WHERE key='desktop.announcement'`).Scan(&enabled).Error; err != nil {
			return nil, err
		}
		return map[string]any{"enabled": enabled, "sourceLocale": view["sourceLocale"], "i18n": view["i18n"], "updatedAt": view["updatedAt"]}, nil
	}
	if err := assertContentAccess(ctx, ref); err != nil {
		return nil, err
	}
	return s.write(ctx, in, op, ref.Entity, func(st *store.Store) (any, error) {
		if ref.Entity == "package" {
			if _, err := st.AdminPackage(ctx, ref.ID); err != nil {
				return nil, err
			}
		}
		if ref.Entity == "announcement" {
			// jsonb null/arrays cannot be merged directly with ||; the first save also repairs historical invalid values.
			raw, _ := json.Marshal(in.Body)
			if err := st.DB.WithContext(ctx).Exec(`INSERT INTO app_config(key,value) VALUES('desktop.announcement',?::jsonb) ON CONFLICT(key) DO UPDATE SET value=(CASE WHEN jsonb_typeof(app_config.value)='object' THEN app_config.value ELSE '{}'::jsonb END)||EXCLUDED.value,updated_at=now()`, string(raw)).Error; err != nil {
				return nil, err
			}
		}
		if err := st.WriteContentText(ctx, ref, text(in.Body, "sourceLocale"), fields, true); err != nil {
			return nil, err
		}
		return mutationResult(ctx, st, ref)
	})
}

const glossaryJSON = `jsonb_build_object('id',g.id,'term',g.term,'translations',g.translations,'doNotTranslate',g.do_not_translate,'createdAt',g.created_at,'updatedAt',g.updated_at)`

func (s *Service) glossary(ctx context.Context, op string, in Input) (any, error) {
	if op == "ListGlossary" {
		current, size, err := page(in)
		if err != nil {
			return nil, err
		}
		where, args := "TRUE", []any{}
		if q := text(in.Params, "q"); q != "" {
			where += " AND strpos(lower(g.term),lower(?))>0"
			args = append(args, q)
		}
		if v, ok := in.Params["donottranslate"].(bool); ok {
			where += " AND g.do_not_translate=?"
			args = append(args, v)
		}
		return s.Store.AdminPage(ctx, glossaryJSON, "FROM i18n_glossary g", where, "g.updated_at DESC,g.id DESC", args, current, size)
	}
	return s.write(ctx, in, op, "glossary", func(st *store.Store) (any, error) {
		if op == "DeleteGlossaryTerm" {
			r := st.DB.WithContext(ctx).Exec("DELETE FROM i18n_glossary WHERE id=?", in.ID)
			if r.Error != nil {
				return nil, r.Error
			}
			if r.RowsAffected == 0 {
				return nil, fail(domain.CodeNotFound)
			}
			return nil, nil
		}
		translations, err := json.Marshal(in.Body["translations"])
		if err != nil {
			return nil, err
		}
		var id int64
		if err := st.DB.WithContext(ctx).Raw(`INSERT INTO i18n_glossary(term,translations,do_not_translate,created_by,updated_by) VALUES(?,?::jsonb,?,?,?) ON CONFLICT(term) DO UPDATE SET translations=EXCLUDED.translations,do_not_translate=EXCLUDED.do_not_translate,updated_by=EXCLUDED.updated_by,updated_at=now() RETURNING id`, text(in.Body, "term"), string(translations), boolean(in.Body, "doNotTranslate"), in.Actor.ID, in.Actor.ID).Scan(&id).Error; err != nil {
			return nil, err
		}
		raw, err := st.JSONRow(ctx, "SELECT "+glossaryJSON+" data FROM i18n_glossary g WHERE id=?", id)
		if err != nil {
			return nil, err
		}
		var result map[string]any
		err = json.Unmarshal(raw, &result)
		return result, err
	})
}
func (s *Service) upsertNotes(ctx context.Context, in Input) (any, error) {
	if in.Version == "" {
		return nil, domain.Validation()
	}
	return s.write(ctx, in, "UpsertReleaseNotes", "release", func(st *store.Store) (any, error) {
		var version struct{ ID int64 }
		if err := st.DB.WithContext(ctx).Raw(`SELECT v.id FROM package_versions v JOIN packages p ON p.id=v.package_id WHERE v.package_id=? AND v.version_base=? AND p.kind='cask' ORDER BY v.id DESC LIMIT 1`, in.ID, in.Version).Scan(&version).Error; err != nil {
			return nil, err
		}
		if version.ID == 0 {
			return nil, fail(domain.CodeNotFound)
		}
		var id int64
		if err := st.DB.WithContext(ctx).Raw(`SELECT id FROM releases WHERE package_id=? AND version=? AND source='editorial' ORDER BY hidden,id DESC LIMIT 1`, in.ID, in.Version).Scan(&id).Error; err != nil {
			return nil, err
		}
		if boolean(in.Body, "clear") {
			if id == 0 {
				return nil, fail(domain.CodeNotFound)
			}
			if err := st.DB.WithContext(ctx).Exec("UPDATE releases SET hidden=true,updated_at=now() WHERE package_id=? AND version=? AND source='editorial'", in.ID, in.Version).Error; err != nil {
				return nil, err
			}
			if err := st.DB.WithContext(ctx).Exec("DELETE FROM release_i18n WHERE release_id IN (SELECT id FROM releases WHERE package_id=? AND version=? AND source='editorial')", in.ID, in.Version).Error; err != nil {
				return nil, err
			}
			ref := content.Ref{Entity: "release", ID: id}
			if err := st.ForgetContentTranslation(ctx, ref); err != nil {
				return nil, err
			}
			if err := st.AppendContentChange(ctx, in.ID); err != nil {
				return nil, err
			}
			return mutationResult(ctx, st, ref)
		}
		// Ingestion time is not a release date. Preserve unknown dates as SQL NULL.
		var date *time.Time
		value, dateProvided := in.Body["publishedAt"]
		if dateProvided && value != nil {
			stamp, ok := value.(string)
			if !ok {
				return nil, domain.Validation()
			}
			parsed, err := time.Parse(time.RFC3339, stamp)
			if err != nil || parsed.UTC().Year() <= 1 {
				return nil, domain.Validation()
			}
			date = &parsed
		}
		if id == 0 {
			if err := st.DB.WithContext(ctx).Raw(`INSERT INTO releases(package_id,source,source_key,version,published_at,source_locale) VALUES(?,'editorial',?,?,?,?) RETURNING id`, in.ID, "editorial:"+in.Version, in.Version, date, text(in.Body, "sourceLocale")).Scan(&id).Error; err != nil {
				return nil, err
			}
		}
		if err := st.DB.WithContext(ctx).Exec("UPDATE releases SET hidden=true,updated_at=now() WHERE package_id=? AND version=? AND source='editorial' AND id<>? AND NOT hidden", in.ID, in.Version, id).Error; err != nil {
			return nil, err
		}
		fields := map[string]any{"hidden": false, "updated_at": s.now()}
		if dateProvided {
			fields["published_at"] = date
		}
		for _, key := range []string{"title", "bodyMarkdown"} {
			if v, present := in.Body[key]; present {
				col := key
				if key == "bodyMarkdown" {
					col = "body_markdown"
				}
				fields[col] = v
			}
		}
		if err := st.DB.WithContext(ctx).Table("releases").Where("id=?", id).Updates(fields).Error; err != nil {
			return nil, err
		}
		texts := mapped(in.Body, map[string]string{"title": "title", "summary": "summary", "sections": "sections", "bodyMarkdown": "bodyMarkdown"})
		ref := content.Ref{Entity: "release", ID: id}
		if err := st.WriteContentText(ctx, ref, text(in.Body, "sourceLocale"), texts, true); err != nil {
			return nil, err
		}
		return mutationResult(ctx, st, ref)
	})
}
func (s *Service) translationManage(ctx context.Context, op string, in Input) (any, error) {
	ref, err := contentRef(in.Body["ref"])
	if op == "GetTranslationStatus" {
		ref = content.Ref{Entity: text(in.Params, "entity"), ID: int64(number(in.Params, "id", 0)), SecondaryID: int64(number(in.Params, "secondaryid", 0))}
		if !ref.Valid() {
			return nil, domain.Validation()
		}
		err = nil
	}
	if err != nil {
		return nil, err
	}
	if err := assertContentAccess(ctx, ref); err != nil {
		return nil, err
	}
	if op == "GetTranslationStatus" {
		v, err := s.translationView(ctx, ref)
		return v, store.MapAdminError(err)
	}
	return s.write(ctx, in, op, "translation", func(st *store.Store) (any, error) {
		if op == "FixTranslation" {
			fields, ok := in.Body["fields"].(map[string]any)
			if !ok {
				return nil, domain.Validation()
			}
			if err := st.WriteContentText(ctx, ref, text(in.Body, "locale"), fields, false); err != nil {
				return nil, err
			}
		} else {
			src, err := st.ReadContentSource(ctx, ref)
			if err != nil {
				return nil, store.MapAdminError(err)
			}
			settings, err := st.TranslationSettings(ctx)
			if err != nil {
				return nil, err
			}
			if !settings.Enabled {
				return nil, fail(domain.CodeInvalidState)
			}
			var requested []string
			if value, present := in.Body["locales"]; present {
				raw, _ := json.Marshal(value)
				if json.Unmarshal(raw, &requested) != nil {
					return nil, domain.Validation()
				}
				for _, locale := range requested {
					if locale == src.SourceLocale {
						return nil, domain.Validation()
					}
				}
			}
			if err := st.ForceContentTranslationLocales(ctx, ref, requested); err != nil {
				return nil, err
			}
		}
		return mutationResult(ctx, st, ref)
	})
}

// Build a UNION with fixed registry identifiers; filter and paginate in the database instead of scanning the entire catalog in MCP.
func (s *Service) translations(ctx context.Context, in Input) (any, error) {
	current, size, err := page(in)
	if err != nil {
		return nil, err
	}
	entities := content.Names()
	if v := text(in.Params, "entity"); v != "" {
		if _, ok := content.Registry[v]; !ok {
			return nil, domain.Validation()
		}
		entities = []string{v}
	}
	clauses := []string{}
	for _, entity := range entities {
		if scope := domain.CurrentOperation(ctx); scope != nil && scope.AgentClientID > 0 {
			if assertContentAccess(ctx, content.Ref{Entity: entity, ID: 1, SecondaryID: 1}) != nil {
				continue
			}
		}
		spec := content.Registry[entity]
		id := "p.id"
		secondary := "0::bigint"
		where := "TRUE"
		if entity == "announcement" {
			id = "0::bigint"
			where = "p.key='desktop.announcement'"
		}
		if entity == "collection_item" {
			id = "p.collection_id"
			secondary = "p.package_id"
		}
		if entity == "package" {
			where = "p.kind='cask'"
		}
		if entity == "category" {
			where += " AND p.applies_to<>'formula'"
		}
		if entity == "feature" {
			where += " AND (p.target_type<>'package' OR EXISTS(SELECT 1 FROM packages pk WHERE pk.id=p.package_id AND pk.kind='cask'))"
		}
		if entity == "release" || entity == "screenshot" || entity == "collection_item" {
			where += " AND EXISTS(SELECT 1 FROM packages pk WHERE pk.id=p.package_id AND pk.kind='cask')"
		}
		if entity == "article" {
			// Only the live revision of a published article is translated.
			where += " AND EXISTS(SELECT 1 FROM articles a WHERE a.published_revision_id=p.id AND a.status='published')"
		}
		clauses = append(clauses, "SELECT '"+entity+"' entity,"+id+" id,"+secondary+" secondary_id FROM "+spec.Parent+" p WHERE "+where)
	}
	result := map[string]any{"records": []any{}, "current": current, "size": size, "total": 0}
	if len(clauses) == 0 {
		return result, nil
	}
	// At most six rows per object; filter using text-table conditions rather than filtering after pagination.
	where, args := "TRUE", []any{}
	filters := []string{}
	for _, entity := range entities {
		spec := content.Registry[entity]
		key := "i." + spec.Key + "=o.id"
		if entity == "announcement" {
			key = "i.config_key='desktop.announcement'"
		}
		if entity == "collection_item" {
			key += " AND i.package_id=o.secondary_id"
		}
		pred := []string{key}
		localArgs := []any{}
		for param, col := range map[string]string{"locale": "i.locale", "status": "i.status"} {
			if v := text(in.Params, param); v != "" && v != "missing" {
				pred = append(pred, col+"=?")
				localArgs = append(localArgs, v)
			}
		}
		if stale, ok := in.Params["stale"].(bool); ok {
			objectKey := "o.id::text"
			if entity == "announcement" {
				objectKey = "'desktop.announcement'"
			}
			if entity == "collection_item" {
				objectKey = "o.id::text||':'||o.secondary_id::text"
			}
			pred = append(pred, "(EXISTS(SELECT 1 FROM content_translation_sources src WHERE src.entity=o.entity AND src.object_key="+objectKey+" AND src.source_hash IS DISTINCT FROM i.source_hash))=?")
			localArgs = append(localArgs, stale)
		}
		if text(in.Params, "status") == "missing" {
			if stale, _ := in.Params["stale"].(bool); stale {
				filters = append(filters, "FALSE")
				continue
			}
			locales := []string{}
			for _, locale := range i18n.Locales {
				locales = append(locales, string(locale))
			}
			if locale := text(in.Params, "locale"); locale != "" {
				locales = []string{locale}
			}
			filters = append(filters, "(o.entity='"+entity+"' AND EXISTS(SELECT 1 FROM unnest(?::text[]) lang WHERE NOT EXISTS(SELECT 1 FROM "+spec.Table+" i WHERE "+key+" AND i.locale=lang)))")
			// Convert the fixed locale list through JSON to a PostgreSQL array, avoiding driver slice expansion.
			raw, _ := json.Marshal(locales)
			filters[len(filters)-1] = strings.Replace(filters[len(filters)-1], "unnest(?::text[])", "jsonb_array_elements_text(?::jsonb)", 1)
			args = append(args, string(raw))
			if q := text(in.Params, "q"); q != "" {
				filters[len(filters)-1] += " AND EXISTS(SELECT 1 FROM " + spec.Table + " i WHERE " + key + " AND strpos(lower(to_jsonb(i)::text),lower(?))>0)"
				args = append(args, q)
			}
			continue
		}
		if q := text(in.Params, "q"); q != "" {
			pred = append(pred, "strpos(lower(to_jsonb(i)::text),lower(?))>0")
			localArgs = append(localArgs, q)
		}
		if len(pred) > 1 {
			filters = append(filters, "(o.entity='"+entity+"' AND EXISTS(SELECT 1 FROM "+spec.Table+" i WHERE "+strings.Join(pred, " AND ")+"))")
			args = append(args, localArgs...)
		}
	}
	if len(filters) > 0 {
		where = strings.Join(filters, " OR ")
	}
	from := "FROM (" + strings.Join(clauses, " UNION ALL ") + ") o"
	p, err := s.Store.AdminPage(ctx, "jsonb_build_object('entity',o.entity,'id',o.id,'secondaryId',o.secondary_id)", from, where, "o.entity,o.id,o.secondary_id", args, current, size)
	if err != nil {
		return nil, err
	}
	records := []any{}
	for _, raw := range p.Records {
		var ref content.Ref
		if err := json.Unmarshal(raw, &ref); err != nil {
			return nil, err
		}
		view, err := s.translationView(ctx, ref)
		if err != nil {
			return nil, err
		}
		records = append(records, view)
	}
	result["records"], result["total"] = records, p.Total
	return result, nil
}

func (s *Service) translationView(ctx context.Context, ref content.Ref) (map[string]any, error) {
	view, err := s.Store.ContentView(ctx, ref)
	if err != nil {
		return nil, err
	}
	state, err := s.Store.HistorySnapshot(ctx, ref)
	if err != nil {
		return nil, err
	}
	urls, err := s.Store.HistoryURLs(ctx, ref, s.Config.WebBaseURL, state)
	if err != nil {
		return nil, err
	}
	if len(urls) > 0 {
		view["webUrl"] = urls[0]
	}
	return view, nil
}
