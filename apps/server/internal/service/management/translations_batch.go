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
	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"github.com/opennavo/opennavo/server/internal/store"
	"gorm.io/gorm"
)

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
	TaskID       int64             `json:"taskId"`
	Fencing      int64             `json:"fencing"`
	Translations []translationItem `json:"translations"`
}

func (s *Service) translationsSource(ctx context.Context, in Input) (any, error) {
	if err := requireSEOAccess(ctx, "translation:review"); err != nil {
		return nil, err
	}
	ref := content.Ref{Entity: text(in.Params, "entity"), ID: int64(number(in.Params, "id", 0)), SecondaryID: int64(number(in.Params, "secondaryid", 0))}
	if !ref.Valid() {
		return nil, domain.Validation()
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
		if ref.Entity == "desktop_release" && !i18n.ValidApp(locale) {
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
	if err := requireSEOAccess(ctx, "translation:review"); err != nil {
		return nil, err
	}
	var request translationBatch
	raw, _ := json.Marshal(in.Body)
	if json.Unmarshal(raw, &request) != nil || !request.Ref.Valid() || len(request.Translations) < 1 || len(request.Translations) > 10 {
		return nil, domain.Validation()
	}
	if err := assertContentAccess(ctx, request.Ref); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, item := range request.Translations {
		if seen[item.Locale] {
			return nil, domain.Validation()
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
			case errors.Is(err, seotasks.ErrLease):
				reason = "lease_lost"
			case errors.Is(err, seotasks.ErrConflict):
				reason = "revision_or_idempotency_conflict"
			}
			var violation *agentgate.Violation
			if errors.As(err, &violation) {
				reason = violation.Reason
			}
			logctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			logerr := agentgate.Log(logctx, s.Store.DB, domain.CurrentOperation(ctx).RequestID, seoOwner(ctx), "WriteTranslations", "rejected", item.Locale+":"+reason, request.TaskID, nil)
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
	return map[string]any{"results": results, "taskId": request.TaskID, "leaseRetained": false}, nil
}
func (s *Service) writeTranslationLocale(ctx context.Context, request translationBatch, item translationItem) (any, error) {
	key := item.IdempotencyKey
	if key == "" {
		key = uuid.NewString()
	}
	return (&seotasks.Service{DB: s.Store.DB}).Control(ctx, seoOwner(ctx), "WriteTranslations", key,
		map[string]any{"ref": request.Ref, "model": request.Model, "translation": item}, domain.CurrentOperation(ctx).DryRun,
		func(control *seotasks.Service) (any, error) {
			local := *s
			local.Store = &store.Store{DB: control.DB, Redis: s.Store.Redis}
			return local.writeTranslationLocaleOnce(ctx, request, item)
		})
}

func (s *Service) writeTranslationLocaleOnce(ctx context.Context, request translationBatch, item translationItem) (any, error) {
	var result any
	err := s.Store.WithTx(ctx, func(tx *gorm.DB) error {
		if !i18n.Valid(item.Locale) || item.SourceHash != "" && len(item.SourceHash) != 64 || item.ExpectedRevision != "" && len(item.ExpectedRevision) != 64 {
			return domain.Validation()
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
			return seotasks.ErrConflict
		}
		if item.Locale == src.SourceLocale {
			return domain.Validation()
		}
		entity := request.Ref.Entity + ":" + request.Ref.Key()
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
		// Retire matching queued work after the direct edit; active leases remain
		// available to their owner, who can explicitly complete them.
		if err := tx.Exec(`UPDATE seo_tasks SET state='done',result=jsonb_build_object('reason','batch_translation'),updated_at=clock_timestamp() WHERE entity_key=? AND kind='translate' AND locale=? AND evidence_hash=? AND state IN ('open','deferred')`, entity, item.Locale, src.Hash).Error; err != nil {
			return err
		}
		if err := st.QueueTranslationInvalidation(ctx, request.Ref); err != nil {
			return err
		}
		if !hasContentChanges(before, after) {
			return nil
		}
		return agentgate.Log(ctx, tx, op.RequestID, seoOwner(ctx), "WriteTranslations", "live", "validated", 0, nil)
	})
	return result, err
}
