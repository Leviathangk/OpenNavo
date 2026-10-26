//go:build integration

package management

import (
	"context"
	"strconv"
	"testing"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestAgentWritesPreviewCommitAndReplayWithoutTasks(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	require.NoError(t, st.DB.Exec("INSERT INTO categories(id,slug,icon,source_locale) VALUES(703,'gate-category','lucide:box','en-US')").Error)
	op := &domain.Operation{RequestID: uuid.NewString(), Name: "updateCategory", AgentClientID: 1, AgentTokenID: 2, Permissions: []string{"catalog:category:edit"}, RequiredPermissions: []string{"catalog:category:edit"}, DryRun: true, Execution: &domain.AgentExecution{IdempotencyKey: uuid.NewString()}}
	svc := &Service{Store: st, Config: config.Config{WebBaseURL: "https://fixture.example"}}
	in := Input{ID: 703, Body: map[string]any{"icon": "lucide:folder", "sourceLocale": "en-US", "i18n": map[string]any{"en-US": map[string]any{"name": "Mac utilities"}}}}
	preview, err := svc.Execute(domain.WithOperation(ctx, op), "UpdateCategory", in)
	require.NoError(t, err)
	require.NotNil(t, preview)
	require.Equal(t, "passed", preview.(map[string]any)["validation"])
	require.Contains(t, preview.(map[string]any)["affectedUrls"], "https://fixture.example/")
	var count int64
	for _, table := range []string{"agent_write_controls", "agent_write_events", "content_revisions"} {
		require.NoError(t, st.DB.Table(table).Count(&count).Error)
		require.Zero(t, count, "a preview records nothing: "+table)
	}
	op.DryRun = false
	_, err = svc.Execute(domain.WithOperation(ctx, op), "UpdateCategory", in)
	require.NoError(t, err)
	var icon string
	require.NoError(t, st.DB.Raw("SELECT icon FROM categories WHERE id=703").Scan(&icon).Error)
	require.Equal(t, "lucide:folder", icon)
	for _, table := range []string{"content_revisions", "content_translation_sources", "job_outbox", "agent_write_events"} {
		require.NoError(t, st.DB.Table(table).Count(&count).Error)
		require.Positive(t, count, table)
	}
	var owner string
	require.NoError(t, st.DB.Raw("SELECT owner FROM agent_write_events WHERE request_id=? AND operation='UpdateCategory' AND result='live'", op.RequestID).Scan(&owner).Error)
	require.Equal(t, "agent:1:2", owner)
	_, err = svc.Execute(domain.WithOperation(ctx, op), "UpdateCategory", in)
	require.NoError(t, err, "an identical retry replays the first result")
	require.NoError(t, st.DB.Table("agent_write_controls").Count(&count).Error)
	require.EqualValues(t, 1, count)

	other := Input{ID: 703, Body: map[string]any{"icon": "lucide:star", "sourceLocale": "en-US", "i18n": map[string]any{"en-US": map[string]any{"name": "Mac utilities"}}}}
	_, err = svc.Execute(domain.WithOperation(ctx, op), "UpdateCategory", other)
	var appErr *domain.AppError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, domain.CodeConflict, appErr.Code)
	require.Equal(t, "rejected: idempotency_key_reused", appErr.Msg)

	current, err := svc.CurrentRevision(ctx, "UpdateCategory", Input{ID: 703})
	require.NoError(t, err)
	require.Positive(t, current)
	stale := *op
	stale.Execution = &domain.AgentExecution{IdempotencyKey: uuid.NewString(), ExpectedRevision: current - 1, ExpectedRevisionSet: true}
	_, err = svc.Execute(domain.WithOperation(ctx, &stale), "UpdateCategory", other)
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, domain.CodeConflict, appErr.Code)
	require.Equal(t, "rejected: revision_conflict:"+strconv.FormatInt(current, 10), appErr.Msg)
	fresh := *op
	fresh.Execution = &domain.AgentExecution{IdempotencyKey: uuid.NewString(), ExpectedRevision: current, ExpectedRevisionSet: true}
	_, err = svc.Execute(domain.WithOperation(ctx, &fresh), "UpdateCategory", other)
	require.NoError(t, err)
	next, err := svc.CurrentRevision(ctx, "UpdateCategory", Input{ID: 703})
	require.NoError(t, err)
	require.Greater(t, next, current)
}

func TestAgentWritesStillRequirePermissions(t *testing.T) {
	// Authorization still fails before a database is needed.
	s := &Service{}
	ctx := domain.WithOperation(context.Background(), &domain.Operation{AgentClientID: 1, AgentTokenID: 2, RequiredPermissions: []string{"catalog:package:edit"}})
	for _, op := range []string{"FixTranslation", "AddLinkPackage", "PublishArticle", "WithdrawArticle", "RestoreRevision", "RevertRequest"} {
		_, err := s.Execute(ctx, op, Input{})
		require.Error(t, err, op)
	}
	_, err := s.CurrentRevision(ctx, "UpsertSeoKeywords", Input{})
	require.Error(t, err, "unregistered operations have no write revision")
}
