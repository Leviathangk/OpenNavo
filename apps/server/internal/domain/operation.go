package domain

import "context"

// Operation carries request audit attribution, never credentials.
type Operation struct {
	RequestID, Name, Tool, ActorName, WebBaseURL, Locale string
	ActorID                                              *int64
	AgentClientID, AgentTokenID                          int64
	Permissions                                          []string
	RequiredPermissions                                  []string
	AllowDelete, DryRun                                  bool
	Execution                                            *AgentExecution
}

// AgentExecution is transport-neutral write metadata. Ownership always comes
// from the authenticated token in Operation, never from this envelope. A
// write without ExpectedRevisionSet skips the optimistic concurrency check.
type AgentExecution struct {
	IdempotencyKey      string `json:"idempotencyKey"`
	ExpectedRevision    int64  `json:"expectedRevision"`
	ExpectedRevisionSet bool   `json:"-"`
}
type operationKey struct{}

func WithOperation(ctx context.Context, op *Operation) context.Context {
	return context.WithValue(ctx, operationKey{}, op)
}
func CurrentOperation(ctx context.Context) *Operation {
	op, _ := ctx.Value(operationKey{}).(*Operation)
	return op
}
func (o *Operation) Allowed(permission string) bool {
	if o == nil {
		return true
	}
	for _, p := range o.Permissions {
		if p == permission || p == "*" {
			return true
		}
	}
	return false
}
