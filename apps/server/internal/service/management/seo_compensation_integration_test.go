//go:build integration

package management

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/agentgate"
	"github.com/opennavo/opennavo/server/internal/service/seobudget"
	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestCompensationAtomicScopeRevisionAndBudget(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	svc := &Service{Store: st}
	ref := content.Ref{Entity: "category", ID: 708}
	require.NoError(t, st.DB.Exec("INSERT INTO categories(id,slug,icon,source_locale) VALUES(708,'compensation-fixture','lucide:box','en-US')").Error)
	tasks := &seotasks.Service{DB: st.DB}
	_, err := tasks.Enqueue(ctx, seotasks.NewTask{Kind: "content", EntityKey: "category:708", DedupeKey: "compensation-seed", RuleVersion: "test", Evidence: map[string]any{}})
	require.NoError(t, err)
	leased, err := tasks.Claim(ctx, seotasks.Owner(1, 2), 1, nil)
	require.NoError(t, err)
	require.Len(t, leased, 1)
	evidence := agentgate.Evidence{EntityKey: "category:708", SourceURL: "https://vendor.example.test", FinalURL: "https://vendor.example.test", BodyText: "Mac utilities", Excerpt: "Mac utilities", BodyHash: seotasks.Hash([]byte("Mac utilities")), FetchedAt: time.Now(), ValidUntil: time.Now().Add(time.Hour), Authority: "reference", FetchStatus: 200, CreatedBy: "fixture", CaptureKey: uuid.NewString(), RequestHash: seotasks.Hash([]byte("fixture"))}
	require.NoError(t, st.DB.Create(&evidence).Error)
	require.NoError(t, st.DB.Exec(`UPDATE seo_settings SET automation=jsonb_set(automation,'{actions,content,mode}','"live"')`).Error)
	op := &domain.Operation{RequestID: uuid.NewString(), Name: "updateCategory", AgentClientID: 1, AgentTokenID: 2, Permissions: []string{"catalog:category:edit", "seo:task:handle"}, RequiredPermissions: []string{"catalog:category:edit"}, Execution: &domain.AgentExecution{TaskID: leased[0].ID, Fencing: leased[0].Fencing, IdempotencyKey: uuid.NewString(), EvidenceIDs: []int64{evidence.ID}}}
	in := Input{ID: 708, Body: map[string]any{"icon": "lucide:folder", "sourceLocale": "en-US", "i18n": map[string]any{"en-US": map[string]any{"name": "Mac utilities"}}}}
	_, err = svc.Execute(domain.WithOperation(ctx, op), "UpdateCategory", in)
	require.NoError(t, err)
	var actionID int64
	require.NoError(t, st.DB.Raw("SELECT id FROM seo_actions WHERE task_id=?", leased[0].ID).Scan(&actionID).Error)
	revision, err := actionRevision(ctx, st, []content.Ref{ref})
	require.NoError(t, err)
	require.Positive(t, revision)
	require.NoError(t, seobudget.Open(ctx, st.DB, "default", "technical", "*", "fixture", map[string]any{}, nil))
	op.RequestID = uuid.NewString()
	op.Name = "compensateSeoAction"
	op.RequiredPermissions = []string{"seo:task:handle"}
	op.Execution.ExpectedRevision = revision
	op.Execution.IdempotencyKey = uuid.NewString()
	_, err = svc.Execute(domain.WithOperation(ctx, op), "CompensateSeoAction", Input{ID: actionID, Body: map[string]any{"reason": "traffic_decline"}})
	require.Error(t, err)
	_, err = svc.Execute(domain.WithOperation(ctx, op), "CompensateSeoAction", Input{ID: actionID, Body: map[string]any{"reason": "implementation_error", "fieldPaths": []any{"/unrelated"}}})
	require.Error(t, err)
	result, err := svc.Execute(domain.WithOperation(ctx, op), "CompensateSeoAction", Input{ID: actionID, Body: map[string]any{"reason": "implementation_error"}})
	require.NoError(t, err)
	var icon string
	require.NoError(t, st.DB.Raw("SELECT icon FROM categories WHERE id=708").Scan(&icon).Error)
	require.Equal(t, "lucide:box", icon)
	replay, err := svc.Execute(domain.WithOperation(ctx, op), "CompensateSeoAction", Input{ID: actionID, Body: map[string]any{"reason": "implementation_error"}})
	require.NoError(t, err)
	a, _ := json.Marshal(result)
	b, _ := json.Marshal(replay)
	require.JSONEq(t, string(a), string(b))
	var count int64
	require.NoError(t, st.DB.Table("seo_actions").Where("compensated_at IS NOT NULL").Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, st.DB.Table("seo_daily_entities").Where("action='compensate'").Count(&count).Error)
	require.EqualValues(t, 1, count)
	// A later revision blocks compensation even when its snapshot is identical.
	var latest struct {
		ID                    int64
		BeforeData, AfterData json.RawMessage
		AfterRevision         int64
	}
	require.NoError(t, st.DB.Raw("SELECT id,before_data,after_data,after_revision FROM seo_actions ORDER BY id DESC LIMIT 1").Scan(&latest).Error)
	state, err := st.HistorySnapshot(ctx, ref)
	require.NoError(t, err)
	altered := store.HistoryState{"categories": []map[string]any{{"id": float64(708), "icon": "fixture"}}}
	_, err = st.RecordRevision(ctx, ref, altered, state, "update", &domain.Operation{RequestID: uuid.NewString(), Name: "humanEdit", RequiredPermissions: []string{"catalog:category:edit"}})
	require.NoError(t, err)
	op.Execution.ExpectedRevision, err = actionRevision(ctx, st, []content.Ref{ref})
	require.NoError(t, err)
	op.Execution.IdempotencyKey = uuid.NewString()
	op.RequestID = uuid.NewString()
	_, err = svc.Execute(domain.WithOperation(ctx, op), "CompensateSeoAction", Input{ID: latest.ID, Body: map[string]any{"reason": "implementation_error"}})
	require.Error(t, err)
}
