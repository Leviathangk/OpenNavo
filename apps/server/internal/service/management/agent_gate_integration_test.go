//go:build integration

package management

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/agentgate"
	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestAgentGateProposalRollsBackBusinessAndOutbox(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	require.NoError(t, st.DB.Exec("INSERT INTO categories(id,slug,icon,source_locale) VALUES(703,'gate-category','lucide:box','en-US')").Error)
	tasks := &seotasks.Service{DB: st.DB}
	_, err := tasks.Enqueue(ctx, seotasks.NewTask{Kind: "content", EntityKey: "category:703", DedupeKey: "gate-proposal", RuleVersion: "test", Evidence: map[string]any{}})
	require.NoError(t, err)
	claimed, err := tasks.Claim(ctx, seotasks.Owner(1, 2), 1, []string{"content"})
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	evidence := agentgate.Evidence{EntityKey: "category:703", SourceURL: "https://vendor.example.test", FinalURL: "https://vendor.example.test", BodyText: "Mac utilities", Excerpt: "Mac utilities", BodyHash: seotasks.Hash([]byte("Mac utilities")), FetchedAt: time.Now(), ValidUntil: time.Now().Add(time.Hour), Authority: "reference", FetchStatus: 200, CreatedBy: "fixture", CaptureKey: uuid.NewString(), RequestHash: seotasks.Hash([]byte("fixture"))}
	require.NoError(t, st.DB.Create(&evidence).Error)
	op := &domain.Operation{RequestID: uuid.NewString(), Name: "updateCategory", AgentClientID: 1, AgentTokenID: 2, Permissions: []string{"catalog:category:edit"}, RequiredPermissions: []string{"catalog:category:edit"}, DryRun: true, Execution: &domain.AgentExecution{TaskID: claimed[0].ID, Fencing: claimed[0].Fencing, IdempotencyKey: uuid.NewString(), EvidenceIDs: []int64{evidence.ID}}}
	svc := &Service{Store: st, Config: config.Config{WebBaseURL: "https://fixture.example"}}
	in := Input{ID: 703, Body: map[string]any{"icon": "lucide:folder", "sourceLocale": "en-US", "i18n": map[string]any{"en-US": map[string]any{"name": "Mac utilities"}}}}
	preview, err := svc.Execute(domain.WithOperation(ctx, op), "UpdateCategory", in)
	require.NoError(t, err)
	require.NotNil(t, preview)
	require.Equal(t, "passed", preview.(map[string]any)["validation"])
	require.Contains(t, preview.(map[string]any)["affectedUrls"], "https://fixture.example/")
	task, err := tasks.Get(ctx, claimed[0].ID)
	require.NoError(t, err)
	require.Equal(t, "leased", task.State)
	var count int64
	require.NoError(t, st.DB.Table("seo_actions").Count(&count).Error)
	require.Zero(t, count)
	op.DryRun = false
	proposal, err := svc.Execute(domain.WithOperation(ctx, op), "UpdateCategory", in)
	require.NoError(t, err)
	require.Equal(t, true, proposal.(map[string]any)["dryRun"])
	var icon string
	require.NoError(t, st.DB.Raw("SELECT icon FROM categories WHERE id=703").Scan(&icon).Error)
	require.Equal(t, "lucide:box", icon)
	for _, table := range []string{"content_revisions", "content_translation_sources", "job_outbox"} {
		require.NoError(t, st.DB.Table(table).Count(&count).Error)
		require.Zero(t, count, table)
	}
	require.NoError(t, st.DB.Table("seo_actions").Where("mode='proposal'").Count(&count).Error)
	require.EqualValues(t, 1, count)
	_, err = svc.Execute(domain.WithOperation(ctx, op), "UpdateCategory", in)
	require.NoError(t, err)
	require.NoError(t, st.DB.Table("seo_actions").Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestAgentGateAllLegacyWritesRequireExecution(t *testing.T) {
	// No database is needed: every legacy write must fail before touching it.
	s := &Service{}
	ctx := domain.WithOperation(context.Background(), &domain.Operation{AgentClientID: 1, AgentTokenID: 2})
	for _, op := range []string{"FixTranslation", "UpdatePackageMeta", "SetPackagePrice", "SetPackageFactRecords", "AddLinkPackage", "PublishArticle", "WithdrawArticle", "RestoreRevision", "RevertRequest", "UpsertSeoKeywords", "UpdatePackageChangelogSettings"} {
		_, err := s.Execute(ctx, op, Input{})
		require.Error(t, err, op)
	}
}
