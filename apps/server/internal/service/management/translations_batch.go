package management

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/i18n"
	"github.com/opennavo/opennavo/server/internal/service/agentgate"
	"github.com/opennavo/opennavo/server/internal/service/seobudget"
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
	if json.Unmarshal(raw, &request) != nil || !request.Ref.Valid() || len(request.Translations) < 1 || len(request.Translations) > 10 || request.TaskID <= 0 || request.Fencing <= 0 || request.Model == "" {
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
			case errors.Is(err, seobudget.ErrLimit), errors.Is(err, seobudget.ErrFrozen):
				reason = "budget_or_breaker"
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
			result = map[string]any{"locale": item.Locale, "status": "failed", "reason": reason, "value": current}
		}
		results = append(results, result)
	}
	return map[string]any{"results": results, "taskId": request.TaskID, "leaseRetained": true}, nil
}
func (s *Service) writeTranslationLocale(ctx context.Context, request translationBatch, item translationItem) (any, error) {
	var result any
	err := s.Store.WithTx(ctx, func(tx *gorm.DB) error {
		if !i18n.Valid(item.Locale) || len(item.SourceHash) != 64 || len(item.ExpectedRevision) != 64 {
			return domain.Validation()
		}
		if err := store.LockCatalogWrites(ctx, tx); err != nil {
			return err
		}
		op := domain.CurrentOperation(ctx)
		b, err := seotasks.Begin(ctx, tx, seotasks.Write{TaskID: request.TaskID, Fencing: request.Fencing, Owner: seoOwner(ctx), IdempotencyKey: item.IdempotencyKey, Operation: "WriteTranslations", Payload: map[string]any{"ref": request.Ref, "model": request.Model, "translation": item}})
		if err != nil {
			return err
		}
		if b.Replayed {
			return json.Unmarshal(b.Replay, &result)
		}
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
		if src.Hash != item.SourceHash || current.Revision != item.ExpectedRevision {
			return seotasks.ErrConflict
		}
		if item.Locale == src.SourceLocale {
			return domain.Validation()
		}
		entity := request.Ref.Entity + ":" + request.Ref.Key()
		budgetEntity, err := seotasks.CanonicalEntity(ctx, tx, entity)
		if err != nil {
			return err
		}
		d, err := agentgate.Check(ctx, tx, agentgate.Write{Operation: "FixTranslation", Entity: entity, Task: b.Task, Body: item.Fields, Site: seobudget.Site(s.Config.WebBaseURL), Source: sourceText(src.Fields)})
		if err != nil {
			return err
		}
		if current.Status == "manual" {
			result = map[string]any{"locale": item.Locale, "status": "preserved_manual", "reason": "manual_translation", "value": current}
			if op.DryRun {
				return nil
			}
			_, err = seotasks.CommitPartial(ctx, tx, b, seotasks.Mutation{Mode: "proposal", Object: "translation", ObjectID: entity, Fields: []string{}, Before: current, After: current, Result: result, RuleVersion: agentgate.RuleVersion, PlaybookVersion: agentgate.PlaybookVersion})
			return err
		}
		before, err := st.HistorySnapshot(ctx, request.Ref)
		if err != nil {
			return err
		}
		if err := tx.SavePoint("batch_language").Error; err != nil {
			return err
		}
		if d.Policy.Mode == "live" {
			if err := seobudget.Reserve(ctx, tx, seobudget.Site(s.Config.WebBaseURL), "translation", budgetEntity, op.RequestID); err != nil {
				return err
			}
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
		if op.DryRun || d.Policy.Mode == "proposal" {
			if err := tx.RollbackTo("batch_language").Error; err != nil {
				return err
			}
			status = "proposal"
			if op.DryRun {
				status = "preview"
			}
		}
		result = map[string]any{"locale": item.Locale, "status": status, "reason": "validated", "value": value}
		if op.DryRun {
			return nil
		}
		var afterRevision *int64
		if d.Policy.Mode == "live" {
			revision, err := actionRevision(ctx, st, []content.Ref{request.Ref})
			if err != nil {
				return err
			}
			afterRevision = &revision
		}
		id, err := seotasks.CommitPartial(ctx, tx, b, seotasks.Mutation{Mode: d.Policy.Mode, Object: "translation", ObjectID: entity, Fields: agentgate.ChangedFields(map[string]store.HistoryState{entity: before}, map[string]store.HistoryState{entity: after}), Before: map[string]store.HistoryState{entity: before}, After: map[string]store.HistoryState{entity: after}, Result: result, RuleVersion: agentgate.RuleVersion, PlaybookVersion: agentgate.PlaybookVersion, AfterRevision: afterRevision})
		if err != nil {
			return err
		}
		if d.Policy.Mode == "live" {
			// This entity lease covers the whole batch. Retire matching queued
			// locale work only after its validated write commits.
			if err := tx.Exec(`UPDATE seo_tasks SET state='done',result=jsonb_build_object('actionId',?::bigint,'reason','batch_translation'),updated_at=clock_timestamp() WHERE id<>? AND entity_key=? AND kind='translate' AND locale=? AND evidence_hash=? AND state IN ('open','deferred')`, id, b.Task.ID, entity, item.Locale, src.Hash).Error; err != nil {
				return err
			}
			if err := st.QueueTranslationInvalidation(ctx, request.Ref); err != nil {
				return err
			}
		}
		return agentgate.Log(ctx, tx, op.RequestID, seoOwner(ctx), "WriteTranslations", d.Policy.Mode, "validated", b.Task.ID, nil)
	})
	return result, err
}
