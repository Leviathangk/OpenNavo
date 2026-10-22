package assets

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"github.com/opennavo/opennavo/server/internal/store"
)

// UploadAgent accepts an authorized upload without task or evidence prerequisites.
// Image decoding, storage limits and URL safety are enforced by Preview/Upload.
func (s *Service) UploadAgent(ctx context.Context, kind string, data []byte, source *string, actor store.AuditActor) (any, error) {
	op := domain.CurrentOperation(ctx)
	if op == nil || op.AgentClientID == 0 {
		return nil, domain.Validation()
	}
	key := ""
	if op.Execution != nil {
		key = op.Execution.IdempotencyKey
	}
	if key == "" {
		key = uuid.NewString()
	}
	result, err := (&seotasks.Service{DB: s.Store.DB}).Control(ctx, seotasks.Owner(op.AgentClientID, op.AgentTokenID), "UploadAsset", key,
		map[string]any{"kind": kind, "sourceUrl": source, "sha256": seotasks.Hash(data)}, op.DryRun,
		func(control *seotasks.Service) (any, error) {
			if op.DryRun {
				metadata, err := s.Preview(ctx, kind, data)
				if err != nil {
					return nil, err
				}
				return map[string]any{"dryRun": true, "mode": "preview", "requestId": op.RequestID, "changes": []any{map[string]any{"object": map[string]any{"entity": "asset", "objectKey": "new"}, "action": "create", "before": nil, "after": metadata}}, "affectedUrls": []string{}}, nil
			}
			local := *s
			local.Store = &store.Store{DB: control.DB, Redis: s.Store.Redis}
			asset, err := local.Upload(ctx, kind, data, source, actor)
			if err != nil {
				return nil, err
			}
			ref := content.Ref{Entity: "asset", ID: asset.Id}
			after, err := local.Store.HistorySnapshot(ctx, ref)
			if err != nil {
				return nil, err
			}
			_, err = local.Store.RecordRevision(ctx, ref, nil, after, "create", op)
			return asset, err
		})
	if errors.Is(err, seotasks.ErrConflict) {
		return nil, &domain.AppError{Code: domain.CodeConflict, HTTPStatus: http.StatusConflict}
	}
	if errors.Is(err, seotasks.ErrInvalid) {
		return nil, domain.Validation()
	}
	return result, err
}

// The HTTP layer checks upload permission; tickets retain their token binding,
// expiry and single-use semantics without a dependency on SEO task leases.
func (s *Service) ValidateUploadTicket(ctx context.Context) error {
	op := domain.CurrentOperation(ctx)
	if op == nil || op.AgentClientID == 0 {
		return domain.Validation()
	}
	return nil
}
