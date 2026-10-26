//go:build integration

package management

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/agentgate"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func hermesContext(permissions ...string) context.Context {
	return domain.WithOperation(context.Background(), &domain.Operation{RequestID: uuid.NewString(), AgentClientID: 1, AgentTokenID: 2, Permissions: permissions, RequiredPermissions: permissions})
}
func hermesMap(v any) map[string]any {
	raw, _ := json.Marshal(v)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}
func hermesSource(t *testing.T, st *store.Store, id int64) content.Ref {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, st.DB.Exec("INSERT INTO categories(id,slug,icon,source_locale) VALUES(?,?,'lucide:box','en-US')", id, uuid.NewString()).Error)
	ref := content.Ref{Entity: "category", ID: id}
	require.NoError(t, st.WithTx(ctx, func(tx *gorm.DB) error {
		return (&store.Store{DB: tx}).WriteContentText(ctx, ref, "en-US", map[string]any{"name": "File manager", "description": "Organize {count} documents in folders."}, true)
	}))
	return ref
}
func TestHermesBatchIndependentLanguagesAndProvenance(t *testing.T) {
	st := testutil.NewSEOStore(t)
	svc := &Service{Store: st}
	ref := hermesSource(t, st, 810)
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES(991,'cask','cache-fixture','cache-fixture','homebrew/cask','Fixture','1','1','{}','fixture');
 INSERT INTO package_categories(package_id,category_id,source) VALUES(991,810,'human');
 DELETE FROM job_outbox WHERE job_type='cache:invalidate'`).Error)
	ctx := hermesContext("translation:review", "catalog:category:edit")
	source, err := st.ReadContentSource(ctx, ref)
	require.NoError(t, err)
	empty := agentgate.Hash([]byte("null"))
	items := []translationItem{
		{Locale: "zh-CN", SourceHash: source.Hash, ExpectedRevision: empty, IdempotencyKey: "fixture-batch-zh", Fields: map[string]any{"name": "文件管理工具", "description": "将 {count} 份文档保存在分类整理的文件夹中。"}},
		{Locale: "ja-JP", SourceHash: source.Hash, ExpectedRevision: empty, IdempotencyKey: "fixture-batch-ja", Fields: map[string]any{"name": "ファイル管理ツール", "description": "フォルダー内のドキュメントを整理します。"}},
	}
	request := translationBatch{Ref: ref, Model: "fixture-model", Translations: items}
	call := func() map[string]any {
		v, e := svc.Execute(ctx, "WriteTranslations", Input{Body: hermesMap(request)})
		require.NoError(t, e)
		return hermesMap(v)
	}
	first := call()
	results := first["results"].([]any)
	require.Equal(t, "written", results[0].(map[string]any)["status"])
	require.Equal(t, "written", results[1].(map[string]any)["status"])
	var patterns []string
	require.NoError(t, st.DB.Raw(`SELECT DISTINCT jsonb_array_elements_text(payload) FROM job_outbox WHERE job_type='cache:invalidate'`).Scan(&patterns).Error)
	require.Contains(t, patterns, "c:cat:*")
	require.Contains(t, patterns, cache.PackageDetailPrefix+"cask:cache-fixture:*")
	for _, global := range []string{"c:*", "c:pkg:*", "c:dep:*", "c:related:*"} {
		require.NotContains(t, patterns, global)
	}
	value, err := st.TranslationValue(ctx, ref, "zh-CN")
	require.NoError(t, err)
	require.Equal(t, "machine", value.Status)
	require.Equal(t, "hermes:fixture-model", *value.Model)
	require.Equal(t, first, call(), "successful language replay is stable")
	reused := request
	reused.Translations = []translationItem{items[0]}
	reused.Translations[0].Fields = map[string]any{"name": "另一个名字", "description": "将 {count} 份文档保存在文件夹中。"}
	v, err := svc.Execute(ctx, "WriteTranslations", Input{Body: hermesMap(reused)})
	require.NoError(t, err)
	require.Equal(t, "idempotency_key_reused", hermesMap(v)["results"].([]any)[0].(map[string]any)["reason"])
	ja, err := st.TranslationValue(ctx, ref, "ja-JP")
	require.NoError(t, err)
	request.Translations[1].ExpectedRevision = ja.Revision
	request.Translations[1].IdempotencyKey = "fixture-ja-edit"
	request.Translations[1].Fields["description"] = "{count} 件のドキュメントをフォルダーに整理します。"
	results = call()["results"].([]any)
	require.Equal(t, "written", results[1].(map[string]any)["status"])
	var invalidations int64
	require.NoError(t, st.DB.Table("job_outbox").Where("unique_key='cache:invalidate:translation:category:810'").Count(&invalidations).Error)
	require.EqualValues(t, 1, invalidations, "successful languages coalesce into one content entity invalidation")
	require.NoError(t, st.DB.Table("job_outbox").Where("job_type='cache:invalidate'").Count(&invalidations).Error)
	require.EqualValues(t, 1, invalidations, "batch writes must not enqueue another generic or global invalidation")
	// A request targeting one language never invalidates another language's CAS.
	view, err := svc.Execute(ctx, "GetTranslationsSource", Input{Params: map[string]any{"entity": "category", "id": float64(810)}})
	require.NoError(t, err)
	require.Len(t, hermesMap(view)["translations"], 10)
	require.NoError(t, st.DB.Exec("UPDATE category_i18n SET status='manual' WHERE category_id=810 AND locale='zh-CN'").Error)
	manual, err := st.TranslationValue(ctx, ref, "zh-CN")
	require.NoError(t, err)
	request.Translations = items[:1]
	request.Translations[0].IdempotencyKey = "fixture-manual-keep"
	request.Translations[0].ExpectedRevision = manual.Revision
	results = call()["results"].([]any)
	require.Equal(t, "written", results[0].(map[string]any)["status"])
	updated, err := st.TranslationValue(ctx, ref, "zh-CN")
	require.NoError(t, err)
	request.Translations[0].ExpectedRevision = updated.Revision
	request.Translations[0].IdempotencyKey = "fixture-current-revision"
	results = call()["results"].([]any)
	require.Equal(t, "written", results[0].(map[string]any)["status"])
	request.Translations[0].IdempotencyKey = "fixture-stale-source"
	request.Translations[0].SourceHash = strings.Repeat("f", 64)
	results = call()["results"].([]any)
	require.Equal(t, "revision_conflict", results[0].(map[string]any)["reason"])
	var rejected string
	require.NoError(t, st.DB.Raw("SELECT reason FROM agent_write_events WHERE operation='WriteTranslations' AND result='rejected' ORDER BY id DESC LIMIT 1").Scan(&rejected).Error)
	require.Equal(t, "zh-CN:revision_conflict", rejected)
	// An explicit preview rolls back.
	request.Translations = []translationItem{{Locale: "de-DE", SourceHash: source.Hash, ExpectedRevision: empty, IdempotencyKey: "fixture-live-de", Fields: map[string]any{"name": "Dateiverwaltung", "description": "Organisieren Sie {count} Dokumente in Ordnern."}}}
	require.NoError(t, st.DB.Exec(`DELETE FROM job_outbox WHERE job_type='cache:invalidate'`).Error)
	results = call()["results"].([]any)
	require.Equal(t, "written", results[0].(map[string]any)["status"])
	value, err = st.TranslationValue(ctx, ref, "de-DE")
	require.NoError(t, err)
	require.Equal(t, "machine", value.Status)
	request.Translations[0].ExpectedRevision = value.Revision
	require.NoError(t, st.DB.Exec(`DELETE FROM job_outbox WHERE job_type='cache:invalidate'`).Error)
	domain.CurrentOperation(ctx).DryRun = true
	request.Translations[0].IdempotencyKey = "fixture-preview-de"
	results = call()["results"].([]any)
	require.Equal(t, "preview", results[0].(map[string]any)["status"])
	var n int64
	require.NoError(t, st.DB.Table("job_outbox").Where("job_type='cache:invalidate'").Count(&n).Error)
	require.Zero(t, n, "a preview must not invalidate live caches")
	require.NoError(t, st.DB.Table("agent_write_controls").Where("idempotency_key='fixture-preview-de'").Count(&n).Error)
	require.Zero(t, n, "a preview records no replay")
	// Cache outbox failure rolls back the language write and its revision.
	domain.CurrentOperation(ctx).DryRun = false
	request.Translations[0].IdempotencyKey = "fixture-cache-failure"
	request.Translations[0].Fields["name"] = "Changed before cache failure"
	require.NoError(t, st.DB.Exec(`CREATE FUNCTION reject_translation_cache() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.unique_key LIKE 'cache:invalidate:translation:%' THEN RAISE EXCEPTION 'fixture cache failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER reject_translation_cache BEFORE INSERT ON job_outbox FOR EACH ROW EXECUTE FUNCTION reject_translation_cache()`).Error)
	results = call()["results"].([]any)
	require.Equal(t, "failed", results[0].(map[string]any)["status"])
	value, err = st.TranslationValue(ctx, ref, "de-DE")
	require.NoError(t, err)
	require.Equal(t, "machine", value.Status)
	require.Equal(t, "Dateiverwaltung", value.Fields["name"])
}
