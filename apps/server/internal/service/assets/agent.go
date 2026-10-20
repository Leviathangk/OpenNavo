package assets

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/agentgate"
	"github.com/opennavo/opennavo/server/internal/service/seobudget"
	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"github.com/opennavo/opennavo/server/internal/store"
	"gorm.io/gorm"
)

// UploadAgent guards both multipart uploads and redeemed tickets. Immutable
// objects may remain unreferenced after a failed transaction; no asset record or
// catalog association is committed until the lease is rechecked.
var errAgentPreview = errors.New("asset preview rollback")

func (s *Service) UploadAgent(ctx context.Context, kind string, data []byte, source *string, actor store.AuditActor) (any, error) {
	op := domain.CurrentOperation(ctx)
	if op == nil || op.AgentClientID == 0 || op.Execution == nil {
		return nil, domain.Validation()
	}
	e := op.Execution
	var result any
	err := s.Store.WithTx(ctx, func(tx *gorm.DB) error {
		b, err := seotasks.Begin(ctx, tx, seotasks.Write{TaskID: e.TaskID, Fencing: e.Fencing, Owner: seotasks.Owner(op.AgentClientID, op.AgentTokenID), IdempotencyKey: e.IdempotencyKey, Operation: "uploadAsset", ExpectedRevision: e.ExpectedRevision, EvidenceIDs: e.EvidenceIDs, Payload: map[string]any{"kind": kind, "sourceUrl": source, "sha256": seotasks.Hash(data)}})
		if err != nil {
			return err
		}
		if b.Replayed {
			return json.Unmarshal(b.Replay, &result)
		}
		if e.ExpectedRevision != 0 {
			return seotasks.ErrConflict
		}
		d, err := agentgate.Check(ctx, tx, agentgate.Write{Site: seobudget.Site(op.WebBaseURL), Operation: "UploadAsset", Entity: b.Task.EntityKey, Body: map[string]any{}, Task: b.Task, EvidenceIDs: e.EvidenceIDs})
		if err != nil {
			return err
		}
		if d.Policy.Mode == "live" {
			budgetEntity, err := seotasks.CanonicalEntity(ctx, tx, b.Task.EntityKey)
			if err != nil {
				return err
			}
			if err := seobudget.Reserve(ctx, tx, seobudget.Site(op.WebBaseURL), d.Rule.Action, budgetEntity, op.RequestID); err != nil {
				return err
			}
		}
		metadata, err := s.Preview(ctx, kind, data)
		if err != nil {
			return err
		}
		if op.DryRun || d.Policy.Mode == "proposal" {
			result = map[string]any{"dryRun": true, "requestId": op.RequestID, "changes": []any{map[string]any{"object": map[string]any{"entity": "asset", "objectKey": "new"}, "action": "create", "before": nil, "after": metadata}}, "affectedUrls": []string{}}
			result.(map[string]any)["mode"] = "proposal"
			if op.DryRun {
				result.(map[string]any)["mode"] = "preview"
			}
		} else {
			local := *s
			local.Store = &store.Store{DB: tx, Redis: s.Store.Redis}
			asset, err := local.Upload(ctx, kind, data, source, actor)
			if err != nil {
				return err
			}
			ref := content.Ref{Entity: "asset", ID: asset.Id}
			after, err := local.Store.HistorySnapshot(ctx, ref)
			if err != nil {
				return err
			}
			if _, err := local.Store.RecordRevision(ctx, ref, nil, after, "create", op); err != nil {
				return err
			}
			result = asset
		}
		if op.DryRun {
			return errAgentPreview
		}
		_, err = seotasks.CommitPartial(ctx, tx, b, seotasks.Mutation{Mode: d.Policy.Mode, Object: "asset", ObjectID: b.Task.EntityKey, Fields: []string{"asset"}, Before: nil, After: result, Result: result, RuleVersion: agentgate.RuleVersion, PlaybookVersion: agentgate.PlaybookVersion})
		if err != nil {
			return err
		}
		return nil
	})
	if errors.Is(err, errAgentPreview) {
		return result, nil
	}
	if errors.Is(err, seobudget.ErrLimit) || errors.Is(err, seobudget.ErrFrozen) {
		return nil, &domain.AppError{Code: domain.CodeRateLimited, HTTPStatus: 429}
	}
	if rejected, _, ok := agentgate.Rejection(err, http.StatusBadRequest); ok {
		return nil, rejected
	}
	if err == seotasks.ErrConflict || err == seotasks.ErrLease {
		return nil, &domain.AppError{Code: domain.CodeConflict, HTTPStatus: 409}
	}
	return result, err
}

// ValidateUploadTicket never consumes a task. Redemption revalidates the same
// task, fencing, settings and evidence, so a ticket cannot extend a lease.
func (s *Service) ValidateUploadTicket(ctx context.Context) error {
	op := domain.CurrentOperation(ctx)
	if op == nil || op.Execution == nil {
		return domain.Validation()
	}
	e := op.Execution
	return s.Store.WithTx(ctx, func(tx *gorm.DB) error {
		task, err := seotasks.Verify(ctx, tx, e.TaskID, e.Fencing, seotasks.Owner(op.AgentClientID, op.AgentTokenID))
		if err != nil {
			return domain.Validation()
		}
		_, err = agentgate.Check(ctx, tx, agentgate.Write{Site: seobudget.Site(op.WebBaseURL), Operation: "CreateAssetUploadTicket", Entity: task.EntityKey, Body: map[string]any{}, Task: task, EvidenceIDs: e.EvidenceIDs})
		if err != nil {
			return domain.Validation()
		}
		return nil
	})
}
