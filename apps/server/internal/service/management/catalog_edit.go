package management

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/agentgate"
	"github.com/opennavo/opennavo/server/internal/service/seobudget"
	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"github.com/opennavo/opennavo/server/internal/store"
	"gorm.io/gorm"
)

// executeCatalogEdit uses the ordinary audited editing path without requiring
// a preallocated SEO task. Existing leased callers still use executeAgent. The
// idempotency key makes a retried request return its first result, and a commit
// names the revision its preview returned, so an unleased edit never overwrites
// a change made in between.
func (s *Service) executeCatalogEdit(ctx context.Context, name string, in Input) (any, error) {
	op := domain.CurrentOperation(ctx)
	e := op.Execution
	payload := map[string]any{"id": in.ID, "key": in.Key, "locale": in.Locale, "version": in.Version, "screenshotId": in.ScreenshotID, "body": in.Body, "expectedRevision": e.ExpectedRevision, "evidenceIds": e.EvidenceIDs}
	result, err := (&seotasks.Service{DB: s.Store.DB}).Control(ctx, seoOwner(ctx), name, e.IdempotencyKey, payload, op.DryRun, func(t *seotasks.Service) (any, error) {
		local := *s
		local.Store = &store.Store{DB: t.DB, Redis: s.Store.Redis}
		return local.catalogEdit(ctx, name, in)
	})
	if errors.Is(err, seobudget.ErrLimit) || errors.Is(err, seobudget.ErrFrozen) {
		return nil, fail(domain.CodeRateLimited)
	}
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
		entity, refs, _, err := agentScope(ctx, st, name, rule.Object, in, seotasks.Task{})
		if err != nil {
			return err
		}
		current, err := actionRevision(ctx, st, refs)
		if err != nil {
			return err
		}
		if !op.Execution.ExpectedRevisionSet && !op.DryRun {
			return &domain.AppError{Code: domain.CodeValidation, HTTPStatus: http.StatusUnprocessableEntity, Msg: "rejected: expected_revision_required"}
		}
		if op.Execution.ExpectedRevisionSet && op.Execution.ExpectedRevision != current {
			return fail(domain.CodeConflict)
		}
		budgetEntity, err := seotasks.CanonicalEntity(ctx, tx, entity)
		if err != nil {
			return err
		}
		if _, err := agentgate.Check(ctx, tx, agentgate.Write{Site: seobudget.Site(s.Config.WebBaseURL), Operation: name, Entity: entity}); err != nil {
			return err
		}
		in.Body = cloneAgentBody(in.Body)
		capAgentProofs(in.Body)
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
			return nil
		}
		afterRefs, err := historyRefs(ctx, st, name, rule.Object, in, result)
		if err != nil {
			return err
		}
		after, err := local.agentSnapshot(ctx, name, rule.Object, in, afterRefs)
		if err != nil {
			return err
		}
		if op.DryRun {
			// Control rolls the whole preview back, including writers that do not
			// honour DryRun themselves; they still answer in the preview shape.
			urls, err := local.agentAffectedURLs(ctx, rule.Object, in, afterRefs, before, after, result)
			if err != nil {
				return err
			}
			result = map[string]any{"dryRun": true, "requestId": op.RequestID, "mode": "preview", "validation": "passed", "expectedRevision": current,
				"changes": []any{map[string]any{"object": map[string]any{"entity": rule.Object, "objectKey": entity}, "action": "update", "before": before, "after": after}}, "affectedUrls": urls}
			return nil
		}
		if !hasContentChanges(before, after) {
			return nil
		}
		if err := seobudget.Reserve(ctx, tx, seobudget.Site(s.Config.WebBaseURL), rule.Action, budgetEntity, op.RequestID); err != nil {
			return err
		}
		if err := agentgate.Log(ctx, tx, op.RequestID, seoOwner(ctx), name, "live", "validated", 0, op.Execution.EvidenceIDs); err != nil {
			return err
		}
		return queueAgentInvalidation(ctx, st, entity, append(refs, afterRefs...))
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

// agentProofValidity matches server-captured evidence. The agent judges whether
// a source supports a fact, but an agent-asserted proof cannot stay current
// forever: it goes stale and is rechecked like any other evidence.
const agentProofValidity = 30 * 24 * time.Hour

func capAgentProofs(value any) {
	switch v := value.(type) {
	case map[string]any:
		if proof, ok := v["evidence"].(map[string]any); ok {
			checked, okChecked := proofTime(proof["checkedAt"])
			until, okUntil := proofTime(proof["validUntil"])
			if limit := checked.Add(agentProofValidity); okChecked && okUntil && until.After(limit) {
				proof["validUntil"] = limit.UTC().Format(time.RFC3339)
			}
		}
		for _, child := range v {
			capAgentProofs(child)
		}
	case []any:
		for _, child := range v {
			capAgentProofs(child)
		}
	}
}

func proofTime(value any) (time.Time, bool) {
	text, ok := value.(string)
	if !ok {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, text)
	return parsed, err == nil
}
