package management

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/i18n"
	"github.com/opennavo/opennavo/server/internal/llm"
	"github.com/opennavo/opennavo/server/internal/service/agentgate"
	"github.com/opennavo/opennavo/server/internal/store"
	"gorm.io/gorm"
)

// errTranslationRevision: the source or the translation changed since the caller read it.
var errTranslationRevision = errors.New("translation revision conflict")

type translationItem struct {
	Locale           string         `json:"locale"`
	SourceHash       string         `json:"sourceHash"`
	ExpectedRevision string         `json:"expectedRevision"`
	IdempotencyKey   string         `json:"idempotencyKey"`
	Fields           map[string]any `json:"fields"`
}
type translationBatch struct {
	Ref          content.Ref       `json:"ref"`
	Model        string            `json:"model"`
	Translations []translationItem `json:"translations"`
}

func (s *Service) translationsSource(ctx context.Context, in Input) (any, error) {
	if err := requirePermission(ctx, "translation:review"); err != nil {
		return nil, err
	}
	ref := content.Ref{Entity: text(in.Params, "entity"), ID: int64(number(in.Params, "id", 0)), SecondaryID: int64(number(in.Params, "secondaryid", 0))}
	if !ref.Valid() {
		return nil, domain.Rejected("content_ref_invalid")
	}
	if err := assertContentAccess(ctx, ref); err != nil {
		return nil, err
	}
	source, err := s.Store.ReadContentSource(ctx, ref)
	if err != nil {
		return nil, store.MapAdminError(err)
	}
	values := []store.TranslationValue{}
	for _, code := range i18n.SiteLocales {
		locale := string(code)
		if ref.Entity == "desktop_release" && !i18n.ValidDesktop(locale) {
			continue
		}
		value, err := s.Store.TranslationValue(ctx, ref, locale)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return map[string]any{"ref": ref, "sourceLocale": source.SourceLocale, "sourceHash": source.Hash, "sourceVersion": source.Hash, "fields": store.ContentAPIFields(source.Fields), "translations": values}, nil
}
func (s *Service) writeTranslations(ctx context.Context, in Input) (any, error) {
	if err := requirePermission(ctx, "translation:review"); err != nil {
		return nil, err
	}
	var request translationBatch
	raw, _ := json.Marshal(in.Body)
	if json.Unmarshal(raw, &request) != nil {
		return nil, domain.Rejected("body_invalid")
	}
	if !request.Ref.Valid() {
		return nil, domain.Rejected("content_ref_invalid")
	}
	if len(request.Translations) < 1 {
		return nil, domain.Rejected("field_required:translations")
	}
	if len(request.Translations) > 10 {
		return nil, domain.Rejected("translations_too_many:10")
	}
	if err := assertContentAccess(ctx, request.Ref); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, item := range request.Translations {
		if seen[item.Locale] {
			return nil, domain.RejectedDetail("locale_duplicate", item.Locale)
		}
		seen[item.Locale] = true
	}
	results := []any{}
	for _, item := range request.Translations {
		result, err := s.writeTranslationLocale(ctx, request, item)
		if err != nil {
			reason := "write_failed"
			var app *domain.AppError
			if errors.As(err, &app) && app.Code == domain.CodeValidation {
				reason = "validation_failed"
				if app.Msg != "" {
					reason += ":" + app.Msg
				}
			}
			switch {
			case errors.Is(err, errTranslationRevision):
				reason = "revision_conflict"
			case errors.Is(err, agentgate.ErrIdempotencyKeyReused):
				reason = "idempotency_key_reused"
			}
			var violation *agentgate.Violation
			if errors.As(err, &violation) {
				reason = violation.Reason
			}
			logctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			logerr := agentgate.Log(logctx, s.Store.DB, domain.CurrentOperation(ctx).RequestID, writeOwner(ctx), "WriteTranslations", "rejected", item.Locale+":"+reason)
			cancel()
			if logerr != nil {
				return nil, logerr
			}
			var current any
			if i18n.Valid(item.Locale) {
				if v, e := s.Store.TranslationValue(ctx, request.Ref, item.Locale); e == nil {
					current = v
				}
			}
			failed := map[string]any{"locale": item.Locale, "status": "failed", "reason": reason, "value": current}
			if failure := (*llm.Failure)(nil); errors.As(err, &failure) && len(failure.Violations) > 0 {
				failed["violations"] = failure.Violations
			}
			result = failed
		}
		results = append(results, result)
	}
	return map[string]any{"results": results}, nil
}
func (s *Service) writeTranslationLocale(ctx context.Context, request translationBatch, item translationItem) (any, error) {
	key := item.IdempotencyKey
	if key == "" {
		key = uuid.NewString()
	}
	return agentgate.Control(ctx, s.Store.DB, writeOwner(ctx), "WriteTranslations", key,
		map[string]any{"ref": request.Ref, "model": request.Model, "translation": item}, domain.CurrentOperation(ctx).DryRun,
		func(tx *gorm.DB) (any, error) {
			local := *s
			local.Store = &store.Store{DB: tx, Redis: s.Store.Redis}
			return local.writeTranslationLocaleOnce(ctx, request, item)
		})
}

func (s *Service) writeTranslationLocaleOnce(ctx context.Context, request translationBatch, item translationItem) (any, error) {
	if err := store.ValidateLocalizedFields(request.Ref.Entity, item.Locale, item.Fields); err != nil {
		return nil, err
	}
	var result any
	err := s.Store.WithTx(ctx, func(tx *gorm.DB) error {
		if !i18n.Valid(item.Locale) {
			return domain.RejectedDetail("locale_invalid", item.Locale)
		}
		if item.SourceHash != "" && len(item.SourceHash) != 64 {
			return domain.Rejected("field_invalid:sourceHash")
		}
		if item.ExpectedRevision != "" && len(item.ExpectedRevision) != 64 {
			return domain.Rejected("field_invalid:expectedRevision")
		}
		if err := store.LockCatalogWrites(ctx, tx); err != nil {
			return err
		}
		op := domain.CurrentOperation(ctx)
		st := &store.Store{DB: tx, Redis: s.Store.Redis}
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "content:"+request.Ref.Entity+":"+request.Ref.Key()).Error; err != nil {
			return err
		}
		src, err := st.ReadContentSource(ctx, request.Ref)
		if err != nil {
			return err
		}
		current, err := st.TranslationValue(ctx, request.Ref, item.Locale)
		if err != nil {
			return err
		}
		if item.SourceHash != "" && src.Hash != item.SourceHash || item.ExpectedRevision != "" && current.Revision != item.ExpectedRevision {
			return errTranslationRevision
		}
		if item.Locale == src.SourceLocale {
			return domain.Rejected("translation_locale_is_source")
		}
		before, err := st.HistorySnapshot(ctx, request.Ref)
		if err != nil {
			return err
		}
		if err := tx.SavePoint("batch_language").Error; err != nil {
			return err
		}
		copy := *op
		copy.Locale = item.Locale
		copy.Name = "WriteTranslations"
		copy.RequiredPermissions = []string{"translation:review"}
		local := store.WithTranslationModel(domain.WithOperation(ctx, &copy), request.Model)
		if err := st.WriteContentText(local, request.Ref, item.Locale, item.Fields, false); err != nil {
			return err
		}
		after, err := st.HistorySnapshot(ctx, request.Ref)
		if err != nil {
			return err
		}
		if _, err := st.RecordRevision(local, request.Ref, before, after, "update", &copy); err != nil {
			return err
		}
		value, err := st.TranslationValue(ctx, request.Ref, item.Locale)
		if err != nil {
			return err
		}
		status := "written"
		if op.DryRun {
			if err := tx.RollbackTo("batch_language").Error; err != nil {
				return err
			}
			status = "preview"
		}
		result = map[string]any{"locale": item.Locale, "status": status, "reason": "validated", "value": value}
		if op.DryRun {
			return nil
		}
		if err := st.QueueTranslationInvalidation(ctx, request.Ref); err != nil {
			return err
		}
		if !hasContentChanges(before, after) {
			return nil
		}
		return agentgate.Log(ctx, tx, op.RequestID, writeOwner(ctx), "WriteTranslations", "live", "validated")
	})
	return result, err
}
