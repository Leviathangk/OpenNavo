package management

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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
	ArticleID    *int64            `json:"articleId,omitempty"`
	Model        string            `json:"model"`
	Translations []translationItem `json:"translations"`
}

func (s *Service) translationsSource(ctx context.Context, in Input) (any, error) {
	if err := requirePermission(ctx, "translation:review"); err != nil {
		return nil, err
	}
	ref := content.Ref{Entity: text(in.Params, "entity"), ID: int64(number(in.Params, "id", 0)), SecondaryID: int64(number(in.Params, "secondaryid", 0))}
	if ref.Entity != "article" && !ref.Valid() {
		return nil, domain.Rejected("content_ref_invalid")
	}
	if err := assertContentAccess(ctx, ref); err != nil {
		return nil, err
	}
	var articleID *int64
	if _, ok := in.Params["articleid"]; ok {
		id := int64(number(in.Params, "articleid", 0))
		articleID = &id
		if _, supplied := in.Params["id"]; supplied && ref.ID == 0 && ref.Entity == "article" {
			return nil, domain.Rejected("article_ref_mismatch:articleId_and_ref.id")
		}
	}
	ref, err := s.Store.ResolveArticleTranslationRef(ctx, ref, articleID, false)
	if err != nil {
		return nil, err
	}
	if !ref.Valid() {
		return nil, domain.Rejected("content_ref_invalid")
	}
	source, err := s.Store.ReadContentSource(ctx, ref)
	if err != nil {
		if ref.Entity == "article" && errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, domain.Rejected("article_ref_must_be_published_revision:use_articleId_or_article_get.published.id")
		}
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
	if request.Ref.Entity != "article" && !request.Ref.Valid() {
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
	if request.Ref.Entity == "article" && request.ArticleID != nil && request.Ref.ID == 0 {
		var supplied struct {
			Ref map[string]json.RawMessage `json:"ref"`
		}
		_ = json.Unmarshal(raw, &supplied)
		if _, present := supplied.Ref["id"]; present {
			return nil, domain.Rejected("article_ref_mismatch:articleId_and_ref.id")
		}
	}
	ref, err := s.Store.ResolveArticleTranslationRef(ctx, request.Ref, request.ArticleID, false)
	if err != nil {
		return nil, err
	}
	if !ref.Valid() {
		return nil, domain.Rejected("content_ref_invalid")
	}
	request.Ref = ref
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
			reason := translationFailureReason(err)
			logctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			logerr := agentgate.Log(logctx, s.Store.DB, domain.CurrentOperation(ctx).RequestID, writeOwner(ctx), "WriteTranslations", "rejected", item.Locale+":"+reason)
			cancel()
			if logerr != nil {
				return nil, logerr
			}
			var current any
			if request.Ref.Entity != "article" && i18n.Valid(item.Locale) {
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

// Return actionable business errors, never raw database errors or their values.
func translationFailureReason(err error) string {
	switch {
	case errors.Is(err, errTranslationRevision):
		return "revision_conflict"
	case errors.Is(err, agentgate.ErrIdempotencyKeyReused):
		return "idempotency_key_reused"
	case errors.Is(err, context.Canceled):
		return "request_cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	}
	var violation *agentgate.Violation
	if errors.As(err, &violation) {
		return violation.Reason
	}
	var app *domain.AppError
	if errors.As(store.MapAdminError(err), &app) {
		if app.Code == domain.CodeValidation && strings.HasPrefix(app.Msg, "rejected: ") {
			return "validation_failed:" + app.Msg
		}
		switch app.Code {
		case domain.CodeValidation:
			return "validation_failed"
		case domain.CodeNotFound:
			return "content_not_found"
		case domain.CodeConflict:
			return "state_conflict"
		case domain.CodeForbidden:
			return "permission_denied"
		case domain.CodeInvalidState:
			return "invalid_state"
		}
	}
	return "internal_error"
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
		if _, err := st.ResolveArticleTranslationRef(ctx, request.Ref, request.ArticleID, true); err != nil {
			return err
		}
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
