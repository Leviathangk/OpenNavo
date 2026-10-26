package management

import (
	"context"
	"fmt"
	"net/http"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/agentgate"
)

type agentTransactionKey struct{}

func agentTransaction(ctx context.Context) bool {
	v, _ := ctx.Value(agentTransactionKey{}).(bool)
	return v
}

// Agent writes use the same audited mutations as the admin console.
func (s *Service) executeAgent(ctx context.Context, name string, in Input) (any, error) {
	op := domain.CurrentOperation(ctx)
	for _, permission := range op.RequiredPermissions {
		if !op.Allowed(permission) {
			return nil, forbidden("permission_required:" + permission)
		}
	}
	if _, ok := agentgate.RuleFor(name); !ok {
		return nil, forbidden("operation_not_exposed")
	}
	copy := *op
	copy.Execution = &domain.AgentExecution{}
	if op.Execution != nil {
		*copy.Execution = *op.Execution
	}
	return s.executeCatalogEdit(domain.WithOperation(ctx, &copy), name, in)
}

// writeOwner keys idempotency records and write events by credential.
func writeOwner(ctx context.Context) string {
	op := domain.CurrentOperation(ctx)
	if op.AgentClientID > 0 {
		return agentgate.Owner(op.AgentClientID, op.AgentTokenID)
	}
	if op.ActorID != nil {
		return fmt.Sprintf("admin:%d", *op.ActorID)
	}
	return "system"
}

func requirePermission(ctx context.Context, permission string) error {
	op := domain.CurrentOperation(ctx)
	if op == nil || !op.Allowed(permission) {
		return forbidden("permission_required:" + permission)
	}
	return nil
}

// forbidden names why the caller may not do this, with fail's status.
func forbidden(reason string) error { return domain.Forbidden(http.StatusConflict, reason) }

func (s *Service) agentSnapshot(ctx context.Context, name, object string, in Input, refs []content.Ref) (any, error) {
	if name == "AddLinkPackage" || name == "UpdateLinkPackage" || name == "RemoveLinkPackage" {
		return snapshotEntity(ctx, s.Store, "link_app", in)
	}
	if nonHistoryObject(object) {
		return s.nonHistorySnapshot(ctx, object, in)
	}
	// Release notes are addressed by package and version, so in.ID is the package.
	// A version without editorial notes yet has an empty state, never release #in.ID.
	if name == "UpsertReleaseNotes" {
		return captureHistory(ctx, s.Store, refs)
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
