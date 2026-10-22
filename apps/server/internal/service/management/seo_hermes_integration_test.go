//go:build integration

package management

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/seotasks"
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
func TestSEOTunerIsolationCASAndAudit(t *testing.T) {
	st := testutil.NewSEOStore(t)
	svc := &Service{Store: st}
	ctx := hermesContext("seo:automation:tune")
	view, err := svc.Execute(ctx, "GetSeoAutomation", Input{})
	require.NoError(t, err)
	v := hermesMap(view)
	request := map[string]any{"automation": v["automation"], "expectedRevision": v["revision"], "idempotencyKey": "fixture-tune-first", "reason": "Fixture quota"}
	_, err = svc.Execute(hermesContext("seo:task:handle", "seo:keyword:edit"), "UpdateSeoAutomation", Input{Body: request})
	require.Error(t, err)
	_, err = svc.Execute(hermesContext("seo:automation:tune", "translation:review"), "UpdateSeoAutomation", Input{Body: request})
	require.Error(t, err)
	// All concurrent retries return one result and one immutable audit receipt.
	errs := make(chan error, 4)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := svc.Execute(hermesContext("seo:automation:tune"), "UpdateSeoAutomation", Input{Body: request})
			errs <- e
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		require.NoError(t, e)
	}
	var count int64
	require.NoError(t, st.DB.Table("seo_control_receipts").Count(&count).Error)
	require.EqualValues(t, 1, count)
	request["reason"] = "Changed payload"
	_, err = svc.Execute(ctx, "UpdateSeoAutomation", Input{Body: request})
	require.Error(t, err)
	request["idempotencyKey"] = "fixture-stale-config"
	_, err = svc.Execute(ctx, "UpdateSeoAutomation", Input{Body: request})
	require.Error(t, err)
	request["expectedRevision"] = float64(2)
	domain.CurrentOperation(ctx).DryRun = true
	_, err = svc.Execute(ctx, "UpdateSeoAutomation", Input{Body: request})
	require.NoError(t, err)
	require.NoError(t, st.DB.Table("seo_control_receipts").Count(&count).Error)
	require.EqualValues(t, 1, count)
	var rev int64
	require.NoError(t, st.DB.Raw("SELECT automation_revision FROM seo_settings").Scan(&rev).Error)
	require.EqualValues(t, 2, rev)
	metrics, err := svc.SEOTuningMetrics(ctx)
	require.NoError(t, err)
	raw, _ := json.Marshal(metrics)
	require.Contains(t, string(raw), "inspectionCoverage")
	require.Contains(t, string(raw), "aggregate_observation_only")
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
	tasks := &seotasks.Service{DB: st.DB}
	leased, err := tasks.Claim(ctx, seotasks.Owner(1, 2), 1, []string{"translate"})
	require.NoError(t, err)
	require.Len(t, leased, 1)
	empty := seotasks.Hash([]byte("null"))
	items := []translationItem{
		{Locale: "zh-CN", SourceHash: source.Hash, ExpectedRevision: empty, IdempotencyKey: "fixture-batch-zh", Fields: map[string]any{"name": "文件管理工具", "description": "将 {count} 份文档保存在分类整理的文件夹中。"}},
		{Locale: "ja-JP", SourceHash: source.Hash, ExpectedRevision: empty, IdempotencyKey: "fixture-batch-ja", Fields: map[string]any{"name": "ファイル管理ツール", "description": "フォルダー内のドキュメントを整理します。"}},
	}
	request := translationBatch{Ref: ref, Model: "fixture-model", TaskID: leased[0].ID, Fencing: leased[0].Fencing, Translations: items}
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
	task, err := tasks.Get(ctx, leased[0].ID)
	require.NoError(t, err)
	require.Equal(t, "leased", task.State)
	require.Equal(t, first, call(), "successful language replay is stable")
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
	request.Translations[0].IdempotencyKey = "fixture-stale-fence"
	request.Fencing++
	results = call()["results"].([]any)
	require.Equal(t, "written", results[0].(map[string]any)["status"])
	request.Fencing--
	request.Translations[0].IdempotencyKey = "fixture-stale-source"
	request.Translations[0].SourceHash = strings.Repeat("f", 64)
	results = call()["results"].([]any)
	require.Equal(t, "revision_or_idempotency_conflict", results[0].(map[string]any)["reason"])
	trace, err := svc.SEOTaskTrace(ctx, leased[0].ID)
	require.NoError(t, err)
	require.Empty(t, hermesMap(trace)["actions"])
	// Proposal configuration does not gate writes; explicit preview still rolls back.
	request.Translations = []translationItem{{Locale: "de-DE", SourceHash: source.Hash, ExpectedRevision: empty, IdempotencyKey: "fixture-proposal-de", Fields: map[string]any{"name": "Dateiverwaltung", "description": "Organisieren Sie {count} Dokumente in Ordnern."}}}
	require.NoError(t, st.DB.Exec(`DELETE FROM job_outbox WHERE job_type='cache:invalidate'`).Error)
	require.NoError(t, st.DB.Exec(`UPDATE seo_settings SET automation=jsonb_set(automation,'{actions,translation,mode}','"proposal"')`).Error)
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
	require.Zero(t, n, "proposal and preview must not invalidate live caches")
	require.NoError(t, st.DB.Table("seo_actions").Where("idempotency_key='fixture-preview-de'").Count(&n).Error)
	require.Zero(t, n)
	require.NoError(t, st.DB.Table("seo_actions").Where("mode='proposal' AND after_revision IS NOT NULL").Count(&n).Error)
	require.Zero(t, n, "proposals must not point to rolled-back content revisions")
	// Cache outbox failure rolls back the language write, revision and live action.
	domain.CurrentOperation(ctx).DryRun = false
	request.Translations[0].IdempotencyKey = "fixture-cache-failure"
	request.Translations[0].Fields["name"] = "Changed before cache failure"
	require.NoError(t, st.DB.Exec(`UPDATE seo_settings SET automation=jsonb_set(automation,'{actions,translation,mode}','"live"');
 CREATE FUNCTION reject_translation_cache() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.unique_key LIKE 'cache:invalidate:translation:%' THEN RAISE EXCEPTION 'fixture cache failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER reject_translation_cache BEFORE INSERT ON job_outbox FOR EACH ROW EXECUTE FUNCTION reject_translation_cache()`).Error)
	results = call()["results"].([]any)
	require.Equal(t, "failed", results[0].(map[string]any)["status"])
	value, err = st.TranslationValue(ctx, ref, "de-DE")
	require.NoError(t, err)
	require.Equal(t, "machine", value.Status)
	require.Equal(t, "Dateiverwaltung", value.Fields["name"])
}
func TestHermesImportReconcilesProcessedAndDeferred(t *testing.T) {
	st := testutil.NewSEOStore(t)
	svc := &Service{Store: st}
	ref := hermesSource(t, st, 811)
	ctx := hermesContext("seo:task:handle", "catalog:category:edit")
	source, err := st.ReadContentSource(ctx, ref)
	require.NoError(t, err)
	raw, _ := json.Marshal("File manager")
	entry := importedEntry{Ref: ref, Field: "name", Locale: "en-US", SourceVersion: source.Hash, State: "processed", Reason: "Imported fixture", Evidence: map[string]any{"sourceUrl": "https://example.com"}, LastAttemptAt: time.Now().UTC().Add(-time.Hour), ValueHash: seotasks.Hash(raw)}
	missing := entry
	missing.Field = "description"
	missing.Locale = "ja-JP"
	missing.State = "deferred"
	mismatch := entry
	mismatch.Locale = "zh-CN"
	request := importRequest{IdempotencyKey: "fixture-import-001", Profile: "fixture", Cursor: map[string]any{"sequence": 3}, Entries: []importedEntry{entry, missing, mismatch}}
	call := func() map[string]any {
		v, e := svc.Execute(ctx, "ImportSeoTasks", Input{Body: hermesMap(request)})
		require.NoError(t, e)
		return hermesMap(v)
	}
	first := call()
	require.Equal(t, float64(2), first["processed"])
	require.Equal(t, float64(1), first["deferred"])
	results := first["results"].([]any)
	require.Equal(t, "done", results[0].(map[string]any)["state"])
	require.Equal(t, "deferred", results[1].(map[string]any)["state"])
	require.Equal(t, "open", results[2].(map[string]any)["state"])
	require.Equal(t, first, call())
	var count int64
	require.NoError(t, st.DB.Table("seo_tasks").Where("kind='hermes_import'").Count(&count).Error)
	require.EqualValues(t, 3, count)
	// The next incremental import detects changed evidence without waiting for a new version.
	request.IdempotencyKey = "fixture-import-002"
	request.Entries[1].Evidence = map[string]any{"sourceUrl": "https://example.com/new"}
	results = call()["results"].([]any)
	require.Equal(t, "open", results[1].(map[string]any)["state"])
	// Malformed entry rolls the receipt, cursor and preceding inserts back together.
	request.IdempotencyKey = "fixture-import-003"
	request.Entries[2].Field = "private_internal_column"
	_, err = svc.Execute(ctx, "ImportSeoTasks", Input{Body: hermesMap(request)})
	require.Error(t, err)
	require.NoError(t, st.DB.Table("seo_control_receipts").Count(&count).Error)
	require.EqualValues(t, 2, count)
}

func TestHermesImportPeriodicFieldsAndServerOwnership(t *testing.T) {
	st := testutil.NewSEOStore(t)
	ctx := hermesContext("seo:task:handle", "catalog:package:edit")
	svc := &Service{Store: st}
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,homepage,raw,raw_hash) VALUES(812,'cask','fixture-import-package','fixture-import-package','homebrew/cask','Fixture','1.0','1.0','https://example.com','{}','fixture')`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO package_meta(package_id,repo_url) VALUES(812,'https://example.com/repository')`).Error)
	raw, _ := json.Marshal("https://example.com/repository")
	entry := importedEntry{Ref: content.Ref{Entity: "package", ID: 812}, Field: "repoUrl", Locale: "en-US", SourceVersion: "1.0", State: "processed", Reason: "fixture", Evidence: map[string]any{}, LastAttemptAt: time.Now().UTC().Add(-31 * 24 * time.Hour), ValueHash: seotasks.Hash(raw)}
	request := importRequest{Profile: "fixture", IdempotencyKey: "periodic-001", Cursor: map[string]any{}, Entries: []importedEntry{entry}}
	first, err := svc.Execute(ctx, "ImportSeoTasks", Input{Body: hermesMap(request)})
	require.NoError(t, err)
	require.Equal(t, "done", hermesMap(first)["results"].([]any)[0].(map[string]any)["state"])
	tasks := &seotasks.Service{DB: st.DB}
	claimed, err := tasks.Claim(ctx, seotasks.Owner(1, 2), 1, []string{"hermes_import"})
	require.NoError(t, err)
	require.Len(t, claimed, 1, "time alone retries without a new version")
	request.IdempotencyKey = "periodic-002"
	request.Entries[0].State = "deferred"
	second, err := svc.Execute(ctx, "ImportSeoTasks", Input{Body: hermesMap(request)})
	require.NoError(t, err)
	require.Equal(t, "leased", hermesMap(second)["results"].([]any)[0].(map[string]any)["state"], "import cannot clobber server lease")
}
