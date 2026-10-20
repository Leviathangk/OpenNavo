//go:build integration

package management

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestKeywordRevisionDetectsHumanWriteAndTaskTraceExposesRevision(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	svc := &Service{Store: st}
	tasks := &seotasks.Service{DB: st.DB}
	_, err := tasks.Enqueue(ctx, seotasks.NewTask{Kind: "research", EntityKey: "site:keywords", DedupeKey: "keyword-revision-fixture", RuleVersion: "fixture", Evidence: map[string]any{}})
	require.NoError(t, err)
	leased, err := tasks.Claim(ctx, seotasks.Owner(1, 2), 1, nil)
	require.NoError(t, err)
	require.Len(t, leased, 1)
	body := map[string]any{"records": []any{map[string]any{"keyword": "example mac", "source": "gsc", "engine": "google", "country": "US", "language": "en-US", "device": "all", "period": "2026-10", "volumeState": "unknown", "observedAt": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)}}, "targets": []any{}}
	_, err = svc.Execute(domain.WithOperation(ctx, &domain.Operation{Permissions: []string{"*"}}), "UpsertSeoKeywords", Input{Body: body})
	require.NoError(t, err)
	op := &domain.Operation{RequestID: uuid.NewString(), Name: "upsertSeoKeywords", AgentClientID: 1, AgentTokenID: 2, Permissions: []string{"seo:keyword:edit", "seo:task:handle"}, RequiredPermissions: []string{"seo:keyword:edit"}, Execution: &domain.AgentExecution{TaskID: leased[0].ID, Fencing: leased[0].Fencing, IdempotencyKey: uuid.NewString()}}
	_, err = svc.Execute(domain.WithOperation(ctx, op), "UpsertSeoKeywords", Input{Body: body})
	require.Error(t, err)
	trace, err := svc.Execute(domain.WithOperation(ctx, op), "GetSeoTask", Input{ID: leased[0].ID})
	require.NoError(t, err)
	revision := trace.(map[string]any)["currentRevision"].(int64)
	require.Positive(t, revision)
	op.Execution.ExpectedRevision = revision
	_, err = svc.Execute(domain.WithOperation(ctx, op), "UpsertSeoKeywords", Input{Body: body})
	require.NoError(t, err)
}
