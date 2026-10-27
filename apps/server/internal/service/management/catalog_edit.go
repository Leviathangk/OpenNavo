package management

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/agentgate"
	"github.com/opennavo/opennavo/server/internal/store"
	"gorm.io/gorm"
)

// executeCatalogEdit provides atomic writes, optional idempotency and previews
// for every allowlisted business operation.
func (s *Service) executeCatalogEdit(ctx context.Context, name string, in Input) (any, error) {
	op := domain.CurrentOperation(ctx)
	e := op.Execution
	if e.IdempotencyKey == "" {
		e.IdempotencyKey = uuid.NewString()
	}
	// evidenceIds stays null so keys recorded before the write-layer rename still replay.
	payload := map[string]any{"id": in.ID, "key": in.Key, "locale": in.Locale, "version": in.Version, "screenshotId": in.ScreenshotID, "requestId": in.RequestID, "jobType": in.JobType, "body": in.Body, "expectedRevision": e.ExpectedRevision, "expectedRevisionSet": e.ExpectedRevisionSet, "evidenceIds": nil}
	result, err := agentgate.Control(ctx, s.Store.DB, writeOwner(ctx), name, e.IdempotencyKey, payload, op.DryRun, func(tx *gorm.DB) (any, error) {
		// Fetch before catalogEdit takes the global catalog lock. Keeping this
		// inside Control still lets identical retries replay without network I/O.
		if name == "AddTapPackage" {
			var err error
			in.tapCandidate, err = s.readTapCandidate(ctx, in)
			if err != nil {
				return nil, err
			}
		}
		local := *s
		local.Store = &store.Store{DB: tx, Redis: s.Store.Redis}
		return local.catalogEdit(ctx, name, in)
	})
	return result, mapWriteError(err)
}

// CurrentRevision reads, without locking, the revision a write of operation
// would check. Reads return it so a caller can pass it back as the expected
// revision of the next write.
func (s *Service) CurrentRevision(ctx context.Context, operation string, in Input) (int64, error) {
	rule, ok := agentgate.RuleFor(operation)
	if !ok {
		return 0, forbidden("operation_not_exposed")
	}
	current, _, err := writeRevision(ctx, s.Store, operation, rule, in, false)
	return current, err
}

// writeRevision resolves the revision that guards a write and the history
// objects it edits. Writers call it under the catalog lock with lock set, which
// also locks an edited article row.
func writeRevision(ctx context.Context, st *store.Store, name string, rule agentgate.Rule, in Input, lock bool) (int64, []content.Ref, error) {
	refs, err := directWriteRefs(ctx, st, name, rule.Object, in)
	if err != nil {
		return 0, nil, err
	}
	current, err := actionRevision(ctx, st, refs)
	if err != nil {
		return 0, nil, err
	}
	db := st.DB.WithContext(ctx)
	switch {
	case name == "UpdateLinkPackage" || name == "RemoveLinkPackage":
		err = db.Raw("SELECT coalesce((SELECT revision FROM link_apps WHERE package_id=?),0)", in.ID).Scan(&current).Error
	case nonHistoryObject(rule.Object):
		err = db.Raw("SELECT coalesce((SELECT revision FROM entity_revisions WHERE entity_key=?),0)", entityRevisionKey(rule.Object, in)).Scan(&current).Error
	case rule.Object == "article" && in.ID > 0:
		query := "SELECT revision FROM articles WHERE id=?"
		if lock {
			query += " FOR UPDATE"
		}
		err = db.Raw(query, in.ID).Scan(&current).Error
	case rule.Object == "package" && strings.Contains(name, "Screenshot"):
		// Screenshot history remains independently restorable, but all package
		// edits accept the revision returned by the package detail read.
		current, err = actionRevision(ctx, st, []content.Ref{{Entity: "package", ID: in.ID}})
	}
	return current, refs, err
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
		current, refs, err := writeRevision(ctx, st, name, rule, in, true)
		if err != nil {
			return err
		}
		if op.Execution.ExpectedRevisionSet && op.Execution.ExpectedRevision != current {
			return agentgate.RevisionConflict(current)
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
		if err := agentgate.Log(ctx, tx, op.RequestID, writeOwner(ctx), name, "live", "validated"); err != nil {
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
