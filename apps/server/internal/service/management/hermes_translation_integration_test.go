//go:build integration

package management

import (
	"context"
	"errors"
	"strconv"
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

func TestHermesTranslationSnapshotsAndProvenance(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	require.NoError(t, st.DB.Exec("INSERT INTO categories(id,slug,icon,source_locale) VALUES(700,'translation-fixture','lucide:box','en-US')").Error)
	ref := content.Ref{Entity: "category", ID: 700}
	writeSource := func(description string) error {
		return st.WithTx(ctx, func(tx *gorm.DB) error {
			return (&store.Store{DB: tx}).WriteContentText(ctx, ref, "en-US", map[string]any{"name": "File organizer", "description": description}, true)
		})
	}
	original := "## File management\n\nKeep {count} documents in organized folders."
	require.NoError(t, writeSource(original))
	require.NoError(t, writeSource(original))
	var count int64
	require.NoError(t, st.DB.Table("seo_tasks").Where("entity_key='category:700' AND state='open'").Count(&count).Error)
	require.EqualValues(t, 9, count, "identical source schedules each locale once")
	require.NoError(t, st.DB.Table("job_outbox").Where("job_type='translate:content'").Count(&count).Error)
	require.Zero(t, count)
	svc := &Service{Store: st}
	leased, err := (&seotasks.Service{DB: st.DB}).Claim(ctx, seotasks.Owner(1, 2), 1, []string{"translate"})
	require.NoError(t, err)
	require.Len(t, leased, 1)
	fix := func(agent bool, model string, fields map[string]any) error {
		op := &domain.Operation{RequestID: uuid.NewString(), Name: "fixTranslation", Permissions: []string{"*"}, RequiredPermissions: []string{"translation:review"}}
		if agent {
			op.AgentClientID = 1
			op.AgentTokenID = 2
			revision, err := actionRevision(ctx, st, []content.Ref{ref})
			if err != nil {
				return err
			}
			op.Execution = &domain.AgentExecution{TaskID: leased[0].ID, Fencing: leased[0].Fencing, IdempotencyKey: uuid.NewString(), ExpectedRevision: revision}
		}
		request := domain.WithOperation(ctx, op)
		_, err := svc.Execute(request, "FixTranslation", Input{Body: map[string]any{"ref": map[string]any{"entity": "category", "id": float64(700)}, "locale": "zh-CN", "model": model, "fields": fields}})
		return err
	}
	translated := map[string]any{"name": "文件管理工具", "description": "## 文件管理\n\n将 {count} 份文档保存在分类整理的文件夹中。"}
	require.NoError(t, fix(true, "", translated))
	require.NoError(t, fix(true, "fixture-model", translated))
	var row struct{ Status, Model, SourceHash, Description string }
	read := func() {
		row.Status, row.Model, row.SourceHash, row.Description = "", "", "", ""
		require.NoError(t, st.DB.Table("category_i18n").Where("category_id=700 AND locale='zh-CN'").Take(&row).Error)
	}
	read()
	require.Equal(t, "machine", row.Status)
	require.Equal(t, "hermes:fixture-model", row.Model)
	oldHash := row.SourceHash
	require.NoError(t, fix(true, "fixture-model", map[string]any{"description": "## 文件管理\n\n错误地删除了占位符的译文。"}))
	read()
	require.Contains(t, row.Description, "删除了占位符")
	require.NoError(t, fix(true, "fixture-model", translated))
	require.NoError(t, fix(false, "", map[string]any{"name": "文档管理工具"}))
	read()
	require.Equal(t, "manual", row.Status)
	require.Empty(t, row.Model)
	require.NoError(t, writeSource("## File management\n\nKeep {count} documents in new shared folders."))
	read()
	require.Equal(t, oldHash, row.SourceHash)
	require.Equal(t, "manual", row.Status)
	require.Equal(t, translated["description"], row.Description)
	source, err := st.ReadContentSource(ctx, ref)
	require.NoError(t, err)
	require.Contains(t, source.Targets, "zh-CN")
	require.NoError(t, fix(true, "fixture-model", map[string]any{"name": "文件管理工具"}), "an explicit edit may replace manual or partial text")
	injected := errors.New("injected failure after scheduling")
	require.ErrorIs(t, st.WithTx(ctx, func(tx *gorm.DB) error {
		if err := (&store.Store{DB: tx}).WriteContentText(ctx, ref, "en-US", map[string]any{"description": "Rollback this source change"}, true); err != nil {
			return err
		}
		return injected
	}), injected)
	after, err := st.ReadContentSource(ctx, ref)
	require.NoError(t, err)
	require.Equal(t, source.Hash, after.Hash)
	require.NoError(t, st.DB.Table("seo_tasks").Where("entity_key='category:700' AND state IN ('open','leased')").Count(&count).Error)
	require.EqualValues(t, 9, count)
	require.NoError(t, st.DB.Table("translation_logs").Count(&count).Error)
	require.Zero(t, count)
}

func TestBatchTranslationAcceptsChangedFormatting(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	require.NoError(t, st.DB.Exec("INSERT INTO categories(id,slug,icon,source_locale) VALUES(701,'translation-rule-fixture','lucide:box','en-US')").Error)
	ref := content.Ref{Entity: "category", ID: 701}
	require.NoError(t, st.WithTx(ctx, func(tx *gorm.DB) error {
		return (&store.Store{DB: tx}).WriteContentText(ctx, ref, "en-US", map[string]any{"name": "File organizer", "description": "Run `brew install organizer` once."}, true)
	}))
	leased, err := (&seotasks.Service{DB: st.DB}).Claim(ctx, seotasks.Owner(1, 2), 1, []string{"translate"})
	require.NoError(t, err)
	require.Len(t, leased, 1)
	source, err := st.ReadContentSource(ctx, ref)
	require.NoError(t, err)
	current, err := st.TranslationValue(ctx, ref, "de-DE")
	require.NoError(t, err)
	op := &domain.Operation{RequestID: uuid.NewString(), Name: "writeTranslations", AgentClientID: 1, AgentTokenID: 2, Permissions: []string{"*"}, RequiredPermissions: []string{"translation:review"}}
	svc := &Service{Store: st}
	// The translation drops the code span around the command.
	result, err := svc.Execute(domain.WithOperation(ctx, op), "WriteTranslations", Input{Body: map[string]any{
		"ref": map[string]any{"entity": "category", "id": 701}, "model": "fixture-model", "taskId": leased[0].ID, "fencing": leased[0].Fencing,
		"translations": []any{map[string]any{"locale": "de-DE", "sourceHash": source.Hash, "expectedRevision": current.Revision, "idempotencyKey": uuid.NewString(),
			"fields": map[string]any{"name": "Dateiorganisator", "description": "Führe brew install organizer einmal aus."}}},
	}})
	require.NoError(t, err)
	item := result.(map[string]any)["results"].([]any)[0].(map[string]any)
	require.Equal(t, "written", item["status"])

}

// A translation renders the source's own claims: "faster" in the source may
// become "更快", but a translation may not add testing wording on its own.
func TestTranslationsLeaveWordingToTheCaller(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSEOStore(t)
	svc := &Service{Store: st}
	write := func(id int64, description, translated string) map[string]any {
		t.Helper()
		require.NoError(t, st.DB.Exec("INSERT INTO categories(id,slug,icon,source_locale) VALUES(?,?,'lucide:box','en-US')", id, "testing-wording-"+uuid.NewString()[:8]).Error)
		ref := content.Ref{Entity: "category", ID: id}
		require.NoError(t, st.WithTx(ctx, func(tx *gorm.DB) error {
			return (&store.Store{DB: tx}).WriteContentText(ctx, ref, "en-US", map[string]any{"name": "File organizer", "description": description}, true)
		}))
		leased, err := (&seotasks.Service{DB: st.DB}).Claim(ctx, seotasks.Owner(1, 2), 1, []string{"translate"})
		require.NoError(t, err)
		require.Len(t, leased, 1)
		source, err := st.ReadContentSource(ctx, ref)
		require.NoError(t, err)
		current, err := st.TranslationValue(ctx, ref, "zh-Hant")
		require.NoError(t, err)
		op := &domain.Operation{RequestID: uuid.NewString(), Name: "writeTranslations", AgentClientID: 1, AgentTokenID: 2, Permissions: []string{"*"}, RequiredPermissions: []string{"translation:review"}}
		result, err := svc.Execute(domain.WithOperation(ctx, op), "WriteTranslations", Input{Body: map[string]any{
			"ref": map[string]any{"entity": "category", "id": id}, "model": "fixture-model", "taskId": leased[0].ID, "fencing": leased[0].Fencing,
			"translations": []any{map[string]any{"locale": "zh-Hant", "sourceHash": source.Hash, "expectedRevision": current.Revision, "idempotencyKey": uuid.NewString(),
				"fields": map[string]any{"name": "檔案整理工具", "description": translated}}},
		}})
		require.NoError(t, err)
		// Retire this category's other languages so the next claim gets the next fixture.
		require.NoError(t, st.DB.Exec("UPDATE seo_tasks SET state='done',lease_owner=NULL,lease_until=NULL WHERE entity_key=?", "category:"+strconv.FormatInt(id, 10)).Error)
		require.NoError(t, st.DB.Exec("UPDATE seo_entity_leases SET owner=NULL,task_id=NULL,lease_until=NULL WHERE entity_key=?", "category:"+strconv.FormatInt(id, 10)).Error)
		return result.(map[string]any)["results"].([]any)[0].(map[string]any)
	}
	stated := write(702, "Opens large folders faster than the Finder list view.", "開啟大型資料夾的速度比 Finder 列表檢視更快。")
	require.Equal(t, "validated", stated["reason"], stated)
	added := write(703, "Opens large folders from the menu bar.", "從選單列開啟大型資料夾，速度更快。")
	require.Equal(t, "written", added["status"])
	require.Equal(t, "validated", added["reason"])
}
