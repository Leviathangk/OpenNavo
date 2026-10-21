package management

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"github.com/opennavo/opennavo/server/internal/store"
	"gorm.io/gorm"
)

func beginAgentAction(ctx context.Context, st *store.Store, in Input) (*seotasks.Begun, error) {
	op := domain.CurrentOperation(ctx)
	if op == nil || op.AgentClientID == 0 || op.Execution == nil || agentTransaction(ctx) {
		return nil, nil
	}
	e := op.Execution
	w := seotasks.Write{TaskID: e.TaskID, Fencing: e.Fencing, Owner: seotasks.Owner(op.AgentClientID, op.AgentTokenID), IdempotencyKey: e.IdempotencyKey, Operation: op.Name, ExpectedRevision: e.ExpectedRevision, EvidenceIDs: e.EvidenceIDs, Payload: map[string]any{"id": in.ID, "key": in.Key, "locale": in.Locale, "version": in.Version, "screenshotId": in.ScreenshotID, "body": in.Body}}
	b, err := seotasks.Begin(ctx, st.DB, w)
	return &b, mapTaskError(err)
}

func checkActionRevision(ctx context.Context, st *store.Store, b *seotasks.Begun, refs []content.Ref) error {
	if b == nil {
		return nil
	}
	var current int64
	for _, ref := range refs {
		var revision int64
		if err := st.DB.WithContext(ctx).Raw("SELECT coalesce(max(id),0) FROM content_revisions WHERE entity=? AND object_key=?", ref.Entity, ref.Key()).Scan(&revision).Error; err != nil {
			return err
		}
		current = max(current, revision)
	}
	if current != b.Write.ExpectedRevision {
		return fail(domain.CodeConflict)
	}
	return nil
}

func finishAgentAction(ctx context.Context, st *store.Store, b *seotasks.Begun, in Input, entity, id string, before, after, result any) error {
	if b == nil {
		return nil
	}
	fields := []string{}
	for key := range in.Body {
		fields = append(fields, key)
	}
	sort.Strings(fields)
	actionID, err := seotasks.Commit(ctx, st.DB, *b, seotasks.Mutation{Mode: "live", Object: entity, ObjectID: id, Fields: fields, Before: before, After: after, Result: result, RuleVersion: "agent-v1", PlaybookVersion: "seo-routine-v1"})
	if err != nil {
		return mapTaskError(err)
	}
	return store.QueueCacheInvalidation(ctx, st.DB, fmt.Sprintf("seo-action:%d", actionID), "c:*")
}

func replayAction(b *seotasks.Begun) (any, error) {
	var result any
	err := json.Unmarshal(b.Replay, &result)
	return result, err
}

func mapTaskError(err error) error {
	if errors.Is(err, seotasks.ErrLease) || errors.Is(err, seotasks.ErrConflict) {
		return fail(domain.CodeConflict)
	}
	if errors.Is(err, seotasks.ErrInvalid) {
		return domain.Validation()
	}
	var state *seotasks.StateError
	if errors.As(err, &state) {
		return &domain.AppError{Code: domain.CodeInvalidState, HTTPStatus: http.StatusConflict, Msg: "rejected: " + state.Reason, Cause: err}
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &domain.AppError{Code: domain.CodeNotFound, HTTPStatus: http.StatusNotFound}
	}
	return err
}

func actionRevision(ctx context.Context, st *store.Store, refs []content.Ref) (int64, error) {
	var current int64
	for _, ref := range refs {
		var n int64
		if err := st.DB.WithContext(ctx).Raw("SELECT coalesce(max(id),0) FROM content_revisions WHERE entity=? AND object_key=?", ref.Entity, ref.Key()).Scan(&n).Error; err != nil {
			return 0, err
		}
		current = max(current, n)
	}
	return current, nil
}
