package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/i18n"
	"github.com/opennavo/opennavo/server/internal/llm"
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
type TranslationSettings struct {
	Enabled                      bool
	EnabledLocales, ContentTypes json.RawMessage
	Model, ReasoningEffort       string
	BaseURL, APIKeyCiphertext    string
	MonthlyTokenBudget           int64
}

func (s *Store) TranslationSettings(ctx context.Context) (TranslationSettings, error) {
	var out TranslationSettings
	err := s.DB.WithContext(ctx).Table("i18n_settings").Where("id=true").Take(&out).Error
	return out, err
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
	out.Hash = content.Hash(out.SourceLocale, out.Fields)
	var targets []struct {
		Locale    string
		UpdatedAt time.Time
	}
	where, args = textWhere(ref)
	if err := s.DB.WithContext(ctx).Raw("SELECT locale,updated_at FROM "+spec.Table+" WHERE "+where+" AND locale<>? AND status IN ('pending','failed') AND source_hash=?", append(args, out.SourceLocale, out.Hash)...).Scan(&targets).Error; err != nil {
		return out, err
	}
	for _, target := range targets {
		out.Targets[target.Locale] = target.UpdatedAt
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

// Callers must use a business transaction; identical source with all target rows present changes neither timestamps nor jobs.
func (s *Store) ScheduleContentTranslation(ctx context.Context, ref content.Ref) error {
	return s.scheduleContentTranslation(ctx, ref, false)
}

// Explicit retranslation bypasses identical-hash deduplication and preservation of existing manual translations, while protecting corrections made during the request.
func (s *Store) ForceContentTranslation(ctx context.Context, ref content.Ref) error {
	return s.scheduleContentTranslation(ctx, ref, true)
}

type translationLocalesKey struct{}

func (s *Store) ForceContentTranslationLocales(ctx context.Context, ref content.Ref, locales []string) error {
	return s.scheduleContentTranslation(context.WithValue(ctx, translationLocalesKey{}, locales), ref, true)
}

// Deleting an object also removes deduplication records and pending deliveries, so old hashes cannot block recreation with the same ID.
func (s *Store) ForgetContentTranslation(ctx context.Context, ref content.Ref) error {
	if !ref.Valid() {
		return errors.New("invalid content reference")
	}
	if err := s.DB.WithContext(ctx).Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "content:"+ref.Entity+":"+ref.Key()).Error; err != nil {
		return err
	}
	if err := s.DB.WithContext(ctx).Exec("DELETE FROM content_translation_sources WHERE entity=? AND object_key=?", ref.Entity, ref.Key()).Error; err != nil {
		return err
	}
	return s.DB.WithContext(ctx).Exec("DELETE FROM job_outbox WHERE unique_key=?", "translate:content:"+ref.Entity+":"+ref.Key()).Error
}

func (s *Store) scheduleContentTranslation(ctx context.Context, ref content.Ref, force bool) error {
	if !ref.Valid() {
		return errors.New("invalid content reference")
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
	spec := content.Registry[ref.Entity]
	where, args := textWhere(ref)
	var old string
	if err := s.DB.WithContext(ctx).Raw("SELECT source_hash FROM content_translation_sources WHERE entity=? AND object_key=?", ref.Entity, ref.Key()).Scan(&old).Error; err != nil {
		return err
	}
	if err := s.DB.WithContext(ctx).Table(spec.Table).Where(where+" AND locale=?", append(args, source.SourceLocale)...).Updates(map[string]any{"status": "source", "source_hash": source.Hash, "source_locale": source.SourceLocale}).Error; err != nil {
		return err
	}
	if len(content.Flatten(source.Fields)) == 0 {
		// Empty source text must also update deduplication, remove old translations and cancel queued jobs to prevent stale content reappearing.
		fields, _ := json.Marshal(source.Fields)
		encodedRef, _ := json.Marshal(ref)
		if err := s.DB.WithContext(ctx).Exec(`INSERT INTO content_translation_sources(entity,object_key,ref,source_locale,source_hash,fields) VALUES(?,?,?::jsonb,?,?,?::jsonb) ON CONFLICT(entity,object_key) DO UPDATE SET ref=EXCLUDED.ref,source_locale=EXCLUDED.source_locale,source_hash=EXCLUDED.source_hash,fields=EXCLUDED.fields,updated_at=clock_timestamp()`, ref.Entity, ref.Key(), string(encodedRef), source.SourceLocale, source.Hash, string(fields)).Error; err != nil {
			return err
		}
		targets := s.DB.WithContext(ctx).Table(spec.Table).Where(where+" AND locale<>?", append(args, source.SourceLocale)...)
		var clearErr error
		if _, includesBody := source.Fields["body_markdown"]; ref.Entity == "release" && !includesBody {
			// Full upstream text is independent of summary translation and remains as reference source text when the summary is cleared.
			clearErr = targets.Updates(map[string]any{"title": source.Fields["title"], "summary": source.Fields["summary"], "sections": gorm.Expr("'[]'::jsonb"), "source_hash": source.Hash, "source_locale": source.SourceLocale, "status": "manual", "model": nil, "translated_at": nil, "fail_reason": nil, "updated_at": time.Now().UTC()}).Error
		} else {
			clearErr = targets.Delete(map[string]any{}).Error
		}
		if clearErr != nil {
			return clearErr
		}
		return s.DB.WithContext(ctx).Exec("DELETE FROM job_outbox WHERE unique_key=?", "translate:content:"+ref.Entity+":"+ref.Key()).Error
	}
	settings, err := s.TranslationSettings(ctx)
	if err != nil {
		return err
	}
	var enabled, types []string
	if json.Unmarshal(settings.EnabledLocales, &enabled) != nil || json.Unmarshal(settings.ContentTypes, &types) != nil {
		return errors.New("invalid translation settings")
	}
	if !settings.Enabled || !containsText(types, ref.Entity) {
		return nil
	}
	if requested, _ := ctx.Value(translationLocalesKey{}).([]string); len(requested) > 0 {
		for _, locale := range requested {
			if !containsText(enabled, locale) || locale == source.SourceLocale {
				return domain.Validation()
			}
		}
		enabled = requested
	}
	fresh := map[string]bool{}
	if old == source.Hash && !force {
		complete := true
		for _, locale := range enabled {
			if locale == source.SourceLocale || !i18n.Valid(locale) {
				continue
			}
			var present bool
			if err := s.DB.WithContext(ctx).Raw("SELECT EXISTS(SELECT 1 FROM "+spec.Table+" WHERE "+where+" AND locale=? AND source_hash=?)", append(args, locale, source.Hash)...).Scan(&present).Error; err != nil {
				return err
			}
			fresh[locale] = present
			complete = complete && present
		}
		// Support hashes left by older deletions: identical-hash deduplication is allowed only if all target rows still exist.
		if complete {
			return nil
		}
	}
	fields, _ := json.Marshal(source.Fields)
	encodedRef, _ := json.Marshal(ref)
	if err := s.DB.WithContext(ctx).Exec(`INSERT INTO content_translation_sources(entity,object_key,ref,source_locale,source_hash,fields) VALUES(?,?,?::jsonb,?,?,?::jsonb) ON CONFLICT(entity,object_key) DO UPDATE SET ref=EXCLUDED.ref,source_locale=EXCLUDED.source_locale,source_hash=EXCLUDED.source_hash,fields=EXCLUDED.fields,updated_at=clock_timestamp()`, ref.Entity, ref.Key(), string(encodedRef), source.SourceLocale, source.Hash, string(fields)).Error; err != nil {
		return err
	}
	for _, locale := range enabled {
		if locale == source.SourceLocale || !i18n.Valid(locale) || fresh[locale] {
			continue
		}
		// Preserve historical manual translations on first adoption; corrections explicitly submitted in the same transaction must not be overwritten with pending.
		var preserve bool
		if err := s.DB.WithContext(ctx).Raw("SELECT EXISTS(SELECT 1 FROM "+spec.Table+" WHERE "+where+" AND locale=? AND status='manual' AND (? OR updated_at>=transaction_timestamp()))", append(args, locale, old == "")...).Scan(&preserve).Error; err != nil {
			return err
		}
		if preserve && !force {
			if err := s.DB.WithContext(ctx).Table(spec.Table).Where(where+" AND locale=?", append(args, locale)...).Updates(map[string]any{"source_hash": source.Hash, "source_locale": source.SourceLocale}).Error; err != nil {
				return err
			}
			continue
		}
		row := map[string]any{spec.Key: ref.ID, "locale": locale, "source_locale": source.SourceLocale, "source_hash": source.Hash, "status": "pending", "updated_at": time.Now().UTC()}
		if ref.Entity == "announcement" {
			row[spec.Key] = ref.Key()
		}
		if ref.Entity == "collection_item" {
			row["package_id"] = ref.SecondaryID
		}
		if ref.Entity == "package" {
			row["source"] = "llm"
		}
		for field := range spec.Fields {
			if field == "sections" {
				row[field] = gorm.Expr("'[]'::jsonb")
			} else {
				if field == "name" || field == "title" || field == "notes" {
					row[field] = ""
				} else {
					row[field] = nil
				}
			}
		}
		keys := []clause.Column{{Name: spec.Key}, {Name: "locale"}}
		if ref.Entity == "collection_item" {
			keys = append(keys, clause.Column{Name: "package_id"})
		}
		if err := s.DB.WithContext(ctx).Table(spec.Table).Clauses(clause.OnConflict{Columns: keys, DoUpdates: clause.Assignments(map[string]any{"source_locale": source.SourceLocale, "source_hash": source.Hash, "status": "pending", "fail_reason": nil, "updated_at": time.Now().UTC()})}).Create(row).Error; err != nil {
			return err
		}
	}
	payload, _ := json.Marshal(map[string]any{"content": ref, "sourceHash": source.Hash, "origin": domain.CurrentOperation(ctx)})
	delay := 30
	if force {
		delay = 0
	}
	// Debouncing retains only this object's latest submission; delivered jobs recheck hashes at read and writeback time.
	return s.DB.WithContext(ctx).Exec(`INSERT INTO job_outbox(unique_key,job_type,payload,available_at) VALUES(?,'translate:content',?::jsonb,clock_timestamp()+?*interval '1 second') ON CONFLICT(unique_key) DO UPDATE SET payload=EXCLUDED.payload,available_at=EXCLUDED.available_at`, "translate:content:"+ref.Entity+":"+ref.Key(), string(payload), delay).Error
}
func containsText(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
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
	return s.DB.WithContext(ctx).Table(content.Registry[ref.Entity].Table).Where(where+" AND locale=?", append(args, locale)...).Update("source_hash", source.Hash).Error
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
func (s *Store) ApplyContentTranslation(ctx context.Context, source ContentSource, locale string, translated map[string]string, model string) (bool, error) {
	applied := false
	err := s.WithTx(ctx, func(tx *gorm.DB) error {
		if err := LockCatalogWrites(ctx, tx); err != nil {
			return err
		}
		st := &Store{DB: tx, Redis: s.Redis}
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "content:"+source.Ref.Entity+":"+source.Ref.Key()).Error; err != nil {
			return err
		}
		current, err := st.ReadContentSource(ctx, source.Ref)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if current.Hash != source.Hash || current.SourceLocale != source.SourceLocale {
			return nil
		}
		version, ok := current.Targets[locale]
		expected, known := source.Targets[locale]
		if !ok || !known || !version.Equal(expected) {
			return nil
		}
		before, err := st.HistorySnapshot(ctx, source.Ref)
		if err != nil {
			return err
		}
		fields := content.Replace(source.Fields, translated)
		if source.Ref.Entity == "package" && locale == "zh-CN" {
			var official string
			if err := tx.Raw(`SELECT name FROM (SELECT unnest(names) name FROM packages WHERE id=?) n WHERE name ~ '[一-鿿]' LIMIT 1`, source.Ref.ID).Scan(&official).Error; err != nil {
				return err
			}
			if official != "" {
				fields["display_name"] = official
			}
		}
		for key, value := range fields {
			if key == "sections" {
				raw, _ := json.Marshal(value)
				fields[key] = gorm.Expr("?::jsonb", string(raw))
			}
		}
		fields["status"] = "machine"
		fields["model"] = model
		fields["translated_at"] = time.Now().UTC()
		fields["updated_at"] = time.Now().UTC()
		fields["fail_reason"] = nil
		where, args := textWhere(source.Ref)
		spec := content.Registry[source.Ref.Entity]
		result := tx.Table(spec.Table).Where(where+" AND locale=? AND updated_at=? AND source_hash=? AND status IN ('pending','failed')", append(args, locale, expected, source.Hash)...).Updates(fields)
		if result.Error != nil {
			return result.Error
		}
		applied = result.RowsAffected == 1
		if applied {
			op := domain.CurrentOperation(ctx)
			if op == nil {
				op = &domain.Operation{Name: "translateContent", RequiredPermissions: []string{"translation:review"}}
			}
			localizedOperation := *op
			localizedOperation.Locale = locale
			op = &localizedOperation
			after, err := st.HistorySnapshot(ctx, source.Ref)
			if err != nil {
				return err
			}
			if _, err := st.RecordRevision(ctx, source.Ref, before, after, "translate", op); err != nil {
				return err
			}
		}

		if applied && source.Ref.Entity == "package" {
			if err := UpdateSearchIndex(ctx, tx, []int64{source.Ref.ID}); err != nil {
				return err
			}
			return st.AppendContentChange(ctx, source.Ref.ID)
		}
		if applied && (source.Ref.Entity == "article" || source.Ref.Entity == "article_author") {
			return st.ContentChanged(ctx, source.Ref)
		}
		if applied && source.Ref.Entity == "release" {
			var id int64
			if err := tx.Raw("SELECT package_id FROM releases WHERE id=?", source.Ref.ID).Scan(&id).Error; err != nil {
				return err
			}
			return st.AppendContentChange(ctx, id)
		}
		return nil
	})
	return applied, err
}
func (s *Store) FailContentTranslation(ctx context.Context, source ContentSource, locales []string, reason string) error {
	spec := content.Registry[source.Ref.Entity]
	where, args := textWhere(source.Ref)
	for _, locale := range locales {
		expected, ok := source.Targets[locale]
		if !ok {
			continue
		}
		if err := s.DB.WithContext(ctx).Table(spec.Table).Where(where+" AND locale=? AND source_hash=? AND updated_at=? AND status IN ('pending','failed')", append(args, locale, source.Hash, expected)...).Updates(map[string]any{"status": "failed", "fail_reason": reason}).Error; err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) LogContentTranslation(ctx context.Context, source ContentSource, locales []string, status, reason, model string, usage llm.Usage) error {
	raw, _ := json.Marshal(locales)
	var prompt, completion, reasoning, cached, latency int64
	for _, a := range usage.Attempts {
		prompt += a.PromptTokens
		completion += a.CompletionTokens
		reasoning += a.ReasoningTokens
		cached += a.CachedTokens
		latency += a.LatencyMS
	}
	var requestID, actorID, clientID any
	if op := domain.CurrentOperation(ctx); op != nil {
		requestID, actorID = nullable(op.RequestID), op.ActorID
		if op.AgentClientID > 0 {
			clientID = op.AgentClientID
		}
	}
	fieldNames := []string{}
	for name := range source.Fields {
		fieldNames = append(fieldNames, name)
	}
	return s.WithTx(ctx, func(tx *gorm.DB) error {
		if err := tx.Exec(`INSERT INTO translation_logs(entity,object_key,source_locale,target_locales,source_hash,model,status,reason,prompt_tokens,completion_tokens,reasoning_tokens,cached_tokens,latency_ms,request_id,actor_id,agent_client_id,fields) VALUES(?,?,?,?::jsonb,?,?,?,?,?,?,?,?,?,?,?,?,ARRAY(SELECT jsonb_array_elements_text(?::jsonb)))`, source.Ref.Entity, source.Ref.Key(), source.SourceLocale, string(raw), source.Hash, model, status, reason, prompt, completion, reasoning, cached, latency, requestID, actorID, clientID, jsonText(fieldNames)).Error; err != nil {
			return err
		}
		return tx.Exec(`INSERT INTO translation_metrics(entity,status,requests,tokens) VALUES(?,?,?,?) ON CONFLICT(entity,status) DO UPDATE SET requests=translation_metrics.requests+EXCLUDED.requests,tokens=translation_metrics.tokens+EXCLUDED.tokens`, source.Ref.Entity, status, len(usage.Attempts), prompt+completion).Error
	})
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
	if !i18n.Valid(source) {
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

func (s *Store) ContentBudget(ctx context.Context) (bool, int64, error) {
	settings, err := s.TranslationSettings(ctx)
	return settings.Enabled, settings.MonthlyTokenBudget, err
}

func (s *Store) contentJSON(ctx context.Context, out *json.RawMessage, query string, args ...any) error {
	err := s.DB.WithContext(ctx).Raw(query, args...).Row().Scan(out)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}
