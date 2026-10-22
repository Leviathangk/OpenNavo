//go:build integration

package management

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/agentgate"
	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestHermesBatchCompensationRestoresTranslationAndProtectsLaterFix(t *testing.T) {
	st := testutil.NewSEOStore(t)
	svc := &Service{Store: st}
	ref := hermesSource(t, st, 820)
	ctx := hermesContext("translation:review", "catalog:category:edit", "seo:task:handle")
	tasks := &seotasks.Service{DB: st.DB}
	_, err := tasks.Enqueue(ctx, seotasks.NewTask{Kind: "fixture_batch_compensation", EntityKey: "category:820", DedupeKey: "fixture-batch-compensation", RuleVersion: "fixture"})
	require.NoError(t, err)
	leased, err := tasks.Claim(ctx, seotasks.Owner(1, 2), 1, []string{"fixture_batch_compensation"})
	require.NoError(t, err)
	require.Len(t, leased, 1)
	source, err := st.ReadContentSource(ctx, ref)
	require.NoError(t, err)
	evidence := agentgate.Evidence{EntityKey: "category:820", SourceURL: "https://vendor.example.test/files", FinalURL: "https://vendor.example.test/files", BodyText: "File manager for Mac", Excerpt: "File manager for Mac", BodyHash: seotasks.Hash([]byte("File manager for Mac")), FetchedAt: time.Now(), ValidUntil: time.Now().Add(time.Hour), Authority: "reference", FetchStatus: 200, CreatedBy: "fixture", CaptureKey: uuid.NewString(), RequestHash: seotasks.Hash([]byte("fixture"))}
	require.NoError(t, st.DB.Create(&evidence).Error)
	type savedAction struct {
		ID            int64
		AfterRevision *int64
	}
	write := func(name string) savedAction {
		t.Helper()
		current, err := st.TranslationValue(ctx, ref, "zh-CN")
		require.NoError(t, err)
		key := uuid.NewString()
		before, err := st.HistorySnapshot(ctx, ref)
		require.NoError(t, err)
		request := translationBatch{Ref: ref, Model: "fixture-model", TaskID: leased[0].ID, Fencing: leased[0].Fencing, Translations: []translationItem{{Locale: "zh-CN", SourceHash: source.Hash, ExpectedRevision: current.Revision, IdempotencyKey: key, Fields: map[string]any{"name": name, "description": "将 {count} 份文档整理到文件夹中。"}}}}
		value, err := svc.Execute(ctx, "WriteTranslations", Input{Body: hermesMap(request)})
		require.NoError(t, err)
		require.Equal(t, "written", hermesMap(value)["results"].([]any)[0].(map[string]any)["status"])
		currentRevision, err := actionRevision(ctx, st, []content.Ref{ref})
		require.NoError(t, err)
		after, err := st.HistorySnapshot(ctx, ref)
		require.NoError(t, err)
		entity := "category:820"
		prior := map[string]store.HistoryState{entity: before}
		next := map[string]store.HistoryState{entity: after}
		var action savedAction
		require.NoError(t, st.WithTx(ctx, func(tx *gorm.DB) error {
			begun, err := seotasks.Begin(ctx, tx, seotasks.Write{TaskID: leased[0].ID, Fencing: leased[0].Fencing, Owner: seotasks.Owner(1, 2), IdempotencyKey: key, Operation: "WriteTranslations", Payload: request})
			if err != nil {
				return err
			}
			action.ID, err = seotasks.CommitPartial(ctx, tx, begun, seotasks.Mutation{Mode: "live", RuleVersion: "fixture", PlaybookVersion: "fixture", Object: "translation", ObjectID: entity, Fields: agentgate.ChangedFields(prior, next), Before: prior, After: next, Result: value, AfterRevision: &currentRevision})
			return err
		}))
		action.AfterRevision = &currentRevision
		return action
	}
	compensate := func(actionID int64) (any, error) {
		t.Helper()
		revision, err := actionRevision(ctx, st, []content.Ref{ref})
		require.NoError(t, err)
		op := &domain.Operation{RequestID: uuid.NewString(), Name: "CompensateSeoAction", AgentClientID: 1, AgentTokenID: 2, Permissions: []string{"seo:task:handle", "catalog:category:edit", "translation:review"}, RequiredPermissions: []string{"seo:task:handle"}, Execution: &domain.AgentExecution{TaskID: leased[0].ID, Fencing: leased[0].Fencing, IdempotencyKey: uuid.NewString(), ExpectedRevision: revision, EvidenceIDs: []int64{evidence.ID}}}
		return svc.Execute(domain.WithOperation(context.Background(), op), "CompensateSeoAction", Input{ID: actionID, Body: map[string]any{"reason": "implementation_error"}})
	}
	write("原来的文件管理工具")
	prior, err := st.HistorySnapshot(ctx, ref)
	require.NoError(t, err)
	action := write("本次有误的文件管理说明")
	result, err := compensate(action.ID)
	require.NoError(t, err)
	require.Equal(t, true, hermesMap(result)["compensated"])
	restored, err := st.HistorySnapshot(ctx, ref)
	require.NoError(t, err)
	require.Equal(t, store.HistoryHash(prior), store.HistoryHash(restored), "compensation restores the whole saved content state and provenance")
	var n int64
	require.NoError(t, st.DB.Table("seo_actions").Where("id=? AND compensated_at IS NOT NULL", action.ID).Count(&n).Error)
	require.EqualValues(t, 1, n)

	older := write("另一条有误的文件管理说明")
	require.NoError(t, st.WithTx(ctx, func(tx *gorm.DB) error {
		local := &store.Store{DB: tx}
		before, err := local.HistorySnapshot(ctx, ref)
		if err != nil {
			return err
		}
		if err := local.WriteContentText(context.Background(), ref, "zh-CN", map[string]any{"name": "后来的人工修复", "description": "将 {count} 份文档整理到文件夹中。"}, false); err != nil {
			return err
		}
		after, err := local.HistorySnapshot(ctx, ref)
		if err != nil {
			return err
		}
		_, err = local.RecordRevision(ctx, ref, before, after, "update", &domain.Operation{RequestID: uuid.NewString(), Name: "FixTranslation", RequiredPermissions: []string{"translation:review"}})
		return err
	}))
	later, err := st.HistorySnapshot(ctx, ref)
	require.NoError(t, err)
	latestRevision, err := actionRevision(ctx, st, []content.Ref{ref})
	require.NoError(t, err)
	require.Greater(t, latestRevision, *older.AfterRevision)
	// Supply the latest execution revision: rejection must come from the older
	// action's saved AfterRevision, not a stale request precondition.
	_, err = compensate(older.ID)
	require.Error(t, err)
	var conflict *domain.AppError
	require.ErrorAs(t, err, &conflict)
	require.Equal(t, domain.CodeConflict, conflict.Code)
	afterRejected, err := st.HistorySnapshot(ctx, ref)
	require.NoError(t, err)
	require.Equal(t, store.HistoryHash(later), store.HistoryHash(afterRejected))
	require.NoError(t, st.DB.Table("seo_actions").Where("id=? AND compensated_at IS NULL", older.ID).Count(&n).Error)
	require.EqualValues(t, 1, n)
}
