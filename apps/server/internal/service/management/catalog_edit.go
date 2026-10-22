package management

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/agentgate"
	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"github.com/opennavo/opennavo/server/internal/store"
	"gorm.io/gorm"
)

// executeCatalogEdit provides atomic writes, optional idempotency and previews
// for every allowlisted business operation. It does not consume a task lease.
func (s *Service) executeCatalogEdit(ctx context.Context, name string, in Input) (any, error) {
	op := domain.CurrentOperation(ctx)
	e := op.Execution
	if e.IdempotencyKey == "" {
		e.IdempotencyKey = uuid.NewString()
	}
	payload := map[string]any{"id": in.ID, "key": in.Key, "locale": in.Locale, "version": in.Version, "screenshotId": in.ScreenshotID, "requestId": in.RequestID, "jobType": in.JobType, "body": in.Body, "expectedRevision": e.ExpectedRevision, "expectedRevisionSet": e.ExpectedRevisionSet, "evidenceIds": e.EvidenceIDs}
	result, err := (&seotasks.Service{DB: s.Store.DB}).Control(ctx, seoOwner(ctx), name, e.IdempotencyKey, payload, op.DryRun, func(t *seotasks.Service) (any, error) {
		local := *s
		local.Store = &store.Store{DB: t.DB, Redis: s.Store.Redis}
		return local.catalogEdit(ctx, name, in)
	})
	var violation *agentgate.Violation
	if errors.As(err, &violation) {
		return nil, &domain.AppError{Code: domain.CodeValidation, HTTPStatus: http.StatusUnprocessableEntity, Msg: "rejected: " + violation.Reason}
	}
	return result, mapTaskError(err)
}

func (s *Service) catalogEdit(ctx context.Context, name string, in Input) (any, error) {
	op := domain.CurrentOperation(ctx)
	var result any
	err := s.Store.WithTx(ctx, func(tx *gorm.DB) error {
		if err := store.LockCatalogWrites(ctx, tx); err != nil {
			return err
		}
		st := &store.Store{DB: tx, Redis: s.Store.Redis}
		rule, _ := agentgate.RuleFor(name)
		refs, err := directWriteRefs(ctx, st, name, rule.Object, in)
		if err != nil {
			return err
		}
		current, err := actionRevision(ctx, st, refs)
		if err != nil {
			return err
		}
		switch {
		case name == "UpdateLinkPackage" || name == "RemoveLinkPackage":
			err = tx.Raw("SELECT coalesce((SELECT revision FROM link_apps WHERE package_id=?),0)", in.ID).Scan(&current).Error
		case nonHistorySEOObject(rule.Object):
			entity := "site:keywords"
			switch rule.Object {
			case "background":
				entity = "background:" + in.Key
			case "update_feed":
				entity = "update_feed:" + text(in.Body, "source") + ":" + text(in.Body, "bundleId")
			}
			err = tx.Raw("SELECT coalesce((SELECT revision FROM seo_entity_revisions WHERE entity_key=?),0)", entity).Scan(&current).Error
		case rule.Object == "listing":
			current, err = ListingAgentRevision(ctx, st, in.Key)
		case rule.Object == "article" && in.ID > 0:
			err = tx.Raw("SELECT revision FROM articles WHERE id=? FOR UPDATE", in.ID).Scan(&current).Error
		}
		if err != nil {
			return err
		}
		if op.Execution.ExpectedRevisionSet && op.Execution.ExpectedRevision != current {
			return fail(domain.CodeConflict)
		}
		local := *s
		local.Store = st
		before, err := local.agentSnapshot(ctx, name, rule.Object, in, refs)
		if err != nil {
			return err
		}
		result, err = local.execute(context.WithValue(ctx, agentTransactionKey{}, true), name, in)
		if err != nil {
			return err
		}
		preview, generic := result.(map[string]any)
		generic = generic && preview["dryRun"] == true
		if op.DryRun && generic {
			preview["mode"], preview["validation"], preview["expectedRevision"] = "preview", "passed", current
			urls, err := local.agentAffectedURLs(ctx, rule.Object, in, refs, before, before, result)
			if err != nil {
				return err
			}
			preview["affectedUrls"] = urls
			return nil
		}
		afterInput := in
		if created, ok := result.(map[string]any); ok && afterInput.ID == 0 {
			afterInput.ID = valueID(created["id"])
		}
		afterRefs, err := historyRefs(ctx, st, name, rule.Object, afterInput, result)
		if err != nil {
			return err
		}
		if rule.Object == "history" || rule.Object == "translation" {
			afterRefs = refs
		}
		after, err := local.agentSnapshot(ctx, name, rule.Object, afterInput, afterRefs)
		if err != nil {
			return err
		}
		if op.DryRun {
			// Control rolls the whole preview back, including writers that do not
			// honour DryRun themselves; they still answer in the preview shape.
			urls, err := local.agentAffectedURLs(ctx, rule.Object, afterInput, afterRefs, before, after, result)
			if err != nil {
				return err
			}
			result = map[string]any{"dryRun": true, "requestId": op.RequestID, "mode": "preview", "validation": "passed", "expectedRevision": current,
				"changes": []any{map[string]any{"object": map[string]any{"entity": rule.Object, "objectKey": in.Key}, "action": "update", "before": before, "after": after}}, "affectedUrls": urls}
			return nil
		}
		if !hasContentChanges(before, after) {
			return nil
		}
		if err := agentgate.Log(ctx, tx, op.RequestID, seoOwner(ctx), name, "live", "validated", 0, op.Execution.EvidenceIDs); err != nil {
			return err
		}
		return store.QueueCacheInvalidation(ctx, tx, "agent-write:"+op.RequestID, "c:*")
	})
	return result, err
}

// Storage touch timestamps do not represent an editorial change.
func hasContentChanges(before, after any) bool {
	for _, field := range agentgate.ChangedFields(before, after) {
		if !strings.HasSuffix(field, "/updated_at") && !strings.HasSuffix(field, "/translated_at") {
			return true
		}
	}
	return false
}

// Restores address revision IDs rather than the content IDs in the request.
func directWriteRefs(ctx context.Context, st *store.Store, name, object string, in Input) ([]content.Ref, error) {
	if object != "history" {
		return historyRefs(ctx, st, name, object, in, nil)
	}
	if name == "CompensateSeoAction" {
		row, err := loadCompensation(ctx, st, in.ID)
		if err != nil {
			return nil, err
		}
		_, refs, err := compensationStates(row.AfterData)
		return refs, err
	}
	table, where, arg := "content_revisions", "id=?", any(in.ID)
	if name == "RestoreTrash" {
		table = "content_trash"
	}
	if name == "RevertRequest" {
		where, arg = "request_id=?", in.RequestID
	}
	var rows []struct{ Entity, ObjectKey string }
	if err := st.DB.WithContext(ctx).Table(table).Select("DISTINCT entity,object_key").Where(where, arg).Find(&rows).Error; err != nil {
		return nil, err
	}
	refs := []content.Ref{}
	for _, row := range rows {
		ref, err := store.HistoryRef(row.Entity, row.ObjectKey)
		if err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, nil
}
