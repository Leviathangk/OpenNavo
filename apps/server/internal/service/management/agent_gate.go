package management

import (
	"context"
	"encoding/json"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/agentgate"
)

type agentTransactionKey struct{}

func agentTransaction(ctx context.Context) bool {
	v, _ := ctx.Value(agentTransactionKey{}).(bool)
	return v
}

// Agent writes use the same audited mutations as the admin console. Tasks and
// evidence are optional workflow records, not authorization prerequisites.
func (s *Service) executeAgent(ctx context.Context, name string, in Input) (any, error) {
	op := domain.CurrentOperation(ctx)
	for _, permission := range op.RequiredPermissions {
		if !op.Allowed(permission) {
			return nil, fail(domain.CodeForbidden)
		}
	}
	if _, ok := agentgate.RuleFor(name); !ok {
		return nil, fail(domain.CodeForbidden)
	}
	copy := *op
	copy.Execution = &domain.AgentExecution{}
	if op.Execution != nil {
		*copy.Execution = *op.Execution
	}
	return s.executeCatalogEdit(domain.WithOperation(ctx, &copy), name, in)
}

func (s *Service) agentSnapshot(ctx context.Context, name, object string, in Input, refs []content.Ref) (any, error) {
	if name == "AddLinkPackage" || name == "UpdateLinkPackage" || name == "RemoveLinkPackage" {
		return snapshotEntity(ctx, s.Store, "link_app", in)
	}
	if nonHistorySEOObject(object) {
		return s.nonHistorySnapshot(ctx, object, in)
	}
	// Release notes are addressed by package and version, so in.ID is the package.
	// A version without editorial notes yet has an empty state, never release #in.ID.
	if name == "UpsertReleaseNotes" {
		return captureHistory(ctx, s.Store, refs)
	}
	if object == "experiment" {
		if in.ID == 0 {
			return nil, nil
		}
		var row struct{ Data json.RawMessage }
		if err := s.Store.DB.WithContext(ctx).Raw("SELECT to_jsonb(e)||jsonb_build_object('pages',(SELECT jsonb_agg(to_jsonb(p) ORDER BY url_id) FROM seo_experiment_pages p WHERE p.experiment_id=e.id)) data FROM seo_experiments e WHERE id=?", in.ID).Scan(&row).Error; err != nil {
			return nil, err
		}
		var value any
		err := json.Unmarshal(row.Data, &value)
		return value, err
	}
	if object == "article" {
		if in.ID == 0 {
			return nil, nil
		}
		return s.articleView(ctx, s.Store, in.ID)
	}
	if len(refs) > 0 {
		return captureHistory(ctx, s.Store, refs)
	}
	return snapshotEntity(ctx, s.Store, object, in)
}
