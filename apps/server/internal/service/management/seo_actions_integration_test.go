//go:build integration

package management

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAgentDirectTranslationKeepsHistoryAndOptionalTasks(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	require.NoError(t, st.DB.Exec("INSERT INTO categories(id,slug,icon,source_locale) VALUES(702,'task-write-fixture','lucide:box','en-US')").Error)
	require.NoError(t, st.WithTx(ctx, func(tx *gorm.DB) error {
		return (&store.Store{DB: tx}).WriteContentText(ctx, content.Ref{Entity: "category", ID: 702}, "en-US", map[string]any{"name": "File manager"}, true)
	}))
	tasks := &seotasks.Service{DB: st.DB}
	claimed, err := tasks.Claim(ctx, seotasks.Owner(1, 2), 1, []string{"translate"})
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	require.Equal(t, "zh-CN", *claimed[0].Locale)
	op := &domain.Operation{RequestID: uuid.NewString(), Name: "fixTranslation", AgentClientID: 1, AgentTokenID: 2, Permissions: []string{"translation:review", "catalog:category:edit"}, RequiredPermissions: []string{"translation:review"}, Execution: &domain.AgentExecution{TaskID: claimed[0].ID, Fencing: claimed[0].Fencing, IdempotencyKey: uuid.NewString()}}
	request := domain.WithOperation(ctx, op)
	svc := &Service{Store: st}
	in := Input{Body: map[string]any{"ref": map[string]any{"entity": "category", "id": float64(702)}, "locale": "zh-CN", "model": "fixture", "fields": map[string]any{"name": "文件管理工具"}}}
	first, err := svc.Execute(request, "FixTranslation", in)
	require.NoError(t, err)
	replay, err := svc.Execute(request, "FixTranslation", in)
	require.NoError(t, err)
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(replay)
	require.JSONEq(t, string(a), string(b))
	task, err := tasks.Get(ctx, claimed[0].ID)
	require.NoError(t, err)
	require.Equal(t, "leased", task.State)
	_, err = tasks.Complete(ctx, task.ID, task.Fencing, seotasks.Owner(1, 2), "done", "")
	require.NoError(t, err)
	var count int64
	require.NoError(t, st.DB.Table("content_revisions").Where("entity='category' AND object_key='702'").Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, st.DB.Table("seo_actions").Where("task_id=?", task.ID).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, st.DB.Table("job_outbox").Where("job_type='cache:invalidate'").Count(&count).Error)
	require.Positive(t, count)
	in.Body["fields"] = map[string]any{"name": "其他管理工具"}
	_, err = svc.Execute(request, "FixTranslation", in)
	require.Error(t, err)
	// Legacy lease metadata does not impose a revision prerequisite.
	next, err := tasks.Claim(ctx, seotasks.Owner(1, 2), 1, []string{"translate"})
	require.NoError(t, err)
	require.Len(t, next, 1)
	op.Execution = &domain.AgentExecution{TaskID: next[0].ID, Fencing: next[0].Fencing, IdempotencyKey: uuid.NewString()}
	_, err = svc.Execute(domain.WithOperation(ctx, op), "FixTranslation", in)
	require.NoError(t, err)
	require.NoError(t, st.DB.Table("seo_actions").Count(&count).Error)
	require.Zero(t, count)
}
