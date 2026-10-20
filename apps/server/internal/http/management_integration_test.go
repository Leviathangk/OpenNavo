//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/opennavo/opennavo/server/gen/adminapi"
	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	httpserver "github.com/opennavo/opennavo/server/internal/http"
	"github.com/opennavo/opennavo/server/internal/http/admin"
	"github.com/opennavo/opennavo/server/internal/jobs"
	"github.com/opennavo/opennavo/server/internal/service/auth"
	"github.com/opennavo/opennavo/server/internal/service/management"
	"github.com/opennavo/opennavo/server/internal/service/seed"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/opennavo/opennavo/server/seeds"
	"github.com/stretchr/testify/require"
)

type recordingQueue struct {
	calls    int
	metadata jobs.Metadata
}

func (q *recordingQueue) Enqueue(_ context.Context, _ string, _ jobs.Payload, m jobs.Metadata) (string, error) {
	q.calls++
	q.metadata = m
	return "test-id", nil
}
func (q *recordingQueue) Trigger(context.Context, string, int64) (string, error) {
	return "test-id", nil
}
func TestManagementContractsMutationsAuditAndSafety(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewFactsStore(t)
	hash, err := seed.HashPassword("test-password-strong")
	require.NoError(t, err)
	seedData, err := seeds.Load()
	require.NoError(t, err)
	require.NoError(t, st.Seed(ctx, seedData, "tester", hash))
	p, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"sample","name":["Sample"],"version":"1","desc":"Editor","homepage":"https://example.com"}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p}}))
	require.NoError(t, st.DB.WithContext(ctx).Exec("DELETE FROM job_outbox").Error)
	pkg, err := st.PublicPackage(ctx, "cask", "sample", "zh-CN")
	require.NoError(t, err)
	pid := strconv.FormatInt(pkg.ID, 10)
	cfg := config.Config{JWTSecret: strings.Repeat("a", 32), JWTAccessTTL: 2 * time.Hour, JWTRefreshTTL: 7 * 24 * time.Hour}
	authentication, err := auth.New(st, cfg)
	require.NoError(t, err)
	pair, err := authentication.Login(ctx, "tester", "test-password-strong", store.AuditActor{})
	require.NoError(t, err)
	identity, err := authentication.Authenticate(ctx, pair.Token)
	require.NoError(t, err)
	enqueue := asynq.NewClientFromRedisClient(st.Redis)
	defer func() { _ = enqueue.Close() }()
	_, err = enqueue.EnqueueContext(ctx, asynq.NewTask("example", []byte(`{}`)), asynq.Queue("default"))
	require.NoError(t, err)
	require.NoError(t, st.DB.WithContext(ctx).Exec("INSERT INTO sync_runs(job_type,trigger,status,stats,finished_at) VALUES('catalog:sync','manual','succeeded','{}',now())").Error)
	queue := &recordingQueue{}
	svc := &management.Service{Store: st, Config: cfg, Queue: queue}
	server, err := httpserver.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), st, httpserver.Dependencies{Admin: &admin.Handler{Auth: authentication, Management: svc, CIToken: "local-ci-only"}})
	require.NoError(t, err)
	spec, err := adminapi.GetSpec()
	require.NoError(t, err)
	call := func(method, path, body, code string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "/admin-api"+path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+pair.Token)
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		server.Engine.ServeHTTP(w, r)
		testutil.ValidateResponse(t, spec, "/admin-api", r, w)
		var envelope struct{ Code string }
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
		require.Equal(t, code, envelope.Code, path+" "+w.Body.String())
		require.Equal(t, 200, w.Code)
		return w
	}
	idFrom := func(w *httptest.ResponseRecorder) int64 {
		var r struct{ Data struct{ ID int64 } }
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &r))
		require.Positive(t, r.Data.ID)
		return r.Data.ID
	}
	for _, path := range []string{"/packages?", "/feedback?", "/search/synonyms?", "/search/insights/top?", "/search/insights/zero?", "/system/users?"} {
		var page struct {
			Data struct{ Records []json.RawMessage }
		}
		require.NoError(t, json.Unmarshal(call("GET", path+"current="+strconv.Itoa(1<<53)+"&size=100", "", "0000").Body.Bytes(), &page))
		require.NotNil(t, page.Data.Records)
		require.Empty(t, page.Data.Records)
	}
	require.NoError(t, st.DB.WithContext(ctx).Exec("INSERT INTO llm_usage(task,ref_id,model,reasoning_effort,prompt_tokens,completion_tokens,reasoning_tokens,cached_tokens) VALUES('enrich_package',1,'gpt-6-luna','high',100,30,20,0)").Error)
	overview := call("GET", "/dashboard/overview", "", "0000")
	require.NotContains(t, overview.Body.String(), `"llm"`)
	require.NotContains(t, overview.Body.String(), `"monthTokens"`)
	for _, path := range []string{"/dashboard/overview", "/packages", "/packages?q=sample&hasIcon=false&translationStatus=none", "/packages/" + pid, "/packages/" + pid + "/screenshots", "/packages/" + pid + "/versions", "/categories/tree", "/assets", "/feedback", "/search/synonyms", "/search/insights/top", "/search/insights/zero", "/jobs/runs", "/jobs/runs/1", "/jobs/queues", "/mirrors", "/app-config", "/system/users", "/system/users?q=test&status=1", "/system/roles", "/system/permissions", "/system/audit-logs", "/packages/" + pid + "/changelog-sources"} {
		call("GET", path, "", "0000")
	}
	assertAudit := func(method, path, body string) {
		var before, after int64
		require.NoError(t, st.DB.WithContext(ctx).Table("audit_logs").Count(&before).Error)
		call(method, path, body, "0000")
		require.NoError(t, st.DB.WithContext(ctx).Table("audit_logs").Count(&after).Error)
		require.Equal(t, before+1, after, path)
	}
	assertAudit("PUT", "/packages/"+pid+"/meta", `{"developer":"Developer","repoUrl":"https://github.com/example/editor","tags":["编辑器"],"notes":"内部备注","editorChoice":true}`)
	assertAudit("PUT", "/packages/"+pid+"/i18n/zh-CN", `{"displayName":"样例","summary":"中文编辑器","description":"中文详细介绍"}`)
	assertAudit("PUT", "/packages/"+pid+"/meta", `{"notes":null}`)
	var changesBefore, changesAfter int64
	require.NoError(t, st.DB.Table("catalog_changes").Where("package_id=?", pkg.ID).Count(&changesBefore).Error)
	stopCache, err := cache.Subscribe(ctx, st.Redis, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	defer stopCache()
	cacheKey := "c:hermes:metadata"
	require.NoError(t, st.Redis.Set(ctx, cacheKey, "old", time.Minute).Err())
	assertAudit("PUT", "/packages/"+pid+"/meta", `{"downloadSize":123456,"accentColor":"#336699"}`)
	require.Eventually(t, func() bool { return st.Redis.Exists(ctx, cacheKey).Val() == 0 }, time.Second, 10*time.Millisecond)
	var metadata struct {
		DownloadSize *int64
		AccentColor  *string
	}
	require.NoError(t, st.DB.Raw("SELECT p.download_size,m.accent_color FROM packages p JOIN package_meta m ON m.package_id=p.id WHERE p.id=?", pkg.ID).Scan(&metadata).Error)
	require.NotNil(t, metadata.DownloadSize)
	require.EqualValues(t, 123456, *metadata.DownloadSize)
	require.NotNil(t, metadata.AccentColor)
	require.Equal(t, "#336699", *metadata.AccentColor)
	require.NoError(t, st.DB.Table("catalog_changes").Where("package_id=? AND reason='content'", pkg.ID).Count(&changesAfter).Error)
	require.GreaterOrEqual(t, changesAfter, int64(1))
	var auditAfter string
	require.NoError(t, st.DB.Raw("SELECT after::text FROM audit_logs WHERE action='UpdatePackageMeta' ORDER BY id DESC LIMIT 1").Scan(&auditAfter).Error)
	require.Contains(t, auditAfter, "downloadSize")
	require.Contains(t, auditAfter, "#336699")
	for _, body := range []string{`{"downloadSize":-1}`, `{"downloadSize":1.5}`, `{"accentColor":"red"}`, `{"accentColor":"#12345g"}`} {
		call("PUT", "/packages/"+pid+"/meta", body, "1001")
	}
	assertAudit("PUT", "/packages/"+pid+"/meta", `{"downloadSize":null,"accentColor":null}`)
	require.NoError(t, st.DB.Raw("SELECT p.download_size,m.accent_color FROM packages p JOIN package_meta m ON m.package_id=p.id WHERE p.id=?", pkg.ID).Scan(&metadata).Error)
	require.Nil(t, metadata.DownloadSize)
	require.Nil(t, metadata.AccentColor)
	require.NoError(t, st.DB.Table("catalog_changes").Where("package_id=?", pkg.ID).Count(&changesAfter).Error)
	require.Equal(t, changesBefore+2, changesAfter)
	var notes *string
	require.NoError(t, st.DB.WithContext(ctx).Raw("SELECT notes FROM package_meta WHERE package_id=?", pkg.ID).Scan(&notes).Error)
	require.Nil(t, notes)
	var categoryID int64
	require.NoError(t, st.DB.WithContext(ctx).Raw("SELECT id FROM categories WHERE slug='developer-tools'").Scan(&categoryID).Error)
	assertAudit("PUT", "/packages/"+pid+"/categories", `{"items":[{"categoryId":`+strconv.FormatInt(categoryID, 10)+`,"isPrimary":true}]}`)
	call("PUT", "/packages/"+pid+"/categories", `{"items":[{"categoryId":`+strconv.FormatInt(categoryID, 10)+`,"isPrimary":false}]}`, "1001")
	asset, err := st.SaveAsset(ctx, store.Asset{Kind: "icon", StorageKey: "icons/test/asset.png", URL: "https://cdn.example.test/icons/test/asset.png", MIME: "image/png", Width: 256, Height: 256, Bytes: 1024, SHA256: strings.Repeat("a", 64)}, nil)
	require.NoError(t, err)
	svc.IconAccent = func(_ context.Context, id int64) (*string, error) {
		require.Equal(t, asset.ID, id)
		color := "#336699"
		return &color, nil
	}
	assertAudit("PUT", "/packages/"+pid+"/icon", `{"assetId":`+strconv.FormatInt(asset.ID, 10)+`}`)
	var iconSource string
	require.NoError(t, st.DB.WithContext(ctx).Raw("SELECT icon_source FROM package_meta WHERE package_id=?", pkg.ID).Scan(&iconSource).Error)
	require.Equal(t, "manual", iconSource)
	var calculatedColor string
	require.NoError(t, st.DB.Raw("SELECT accent_color FROM package_meta WHERE package_id=?", pkg.ID).Scan(&calculatedColor).Error)
	require.Equal(t, "#336699", calculatedColor)
	assertAudit("DELETE", "/packages/"+pid+"/icon", "")
	shot, err := st.SaveAsset(ctx, store.Asset{Kind: "screenshot", StorageKey: "screenshots/test/asset-1280.png", URL: "https://cdn.example.test/screenshots/test/asset-1280.png", MIME: "image/png", Width: 1280, Height: 800, Bytes: 4096, SHA256: strings.Repeat("b", 64)}, nil)
	require.NoError(t, err)
	sid := idFrom(call("POST", "/packages/"+pid+"/screenshots", `{"assetId":`+strconv.FormatInt(shot.ID, 10)+`,"sourceLocale":"zh-CN","i18n":{"zh-CN":{"caption":"屏幕截图"}}}`, "0000"))
	assertAudit("PUT", "/packages/"+pid+"/screenshots/"+strconv.FormatInt(sid, 10), `{"sourceLocale":"zh-CN","i18n":{"zh-CN":{"caption":"说明"}},"theme":"light"}`)
	assertAudit("PUT", "/packages/"+pid+"/screenshots/order", `{"ids":[`+strconv.FormatInt(sid, 10)+`]}`)
	call("PUT", "/packages/"+pid+"/screenshots/order", `{"ids":[]}`, "1001")
	assertAudit("DELETE", "/packages/"+pid+"/screenshots/"+strconv.FormatInt(sid, 10), "")
	categoryBody := `{"slug":"test-category","icon":"lucide:code-xml","appliesTo":"both","visible":true,"hiddenByDefault":false,"sourceLocale":"zh-CN","i18n":{"zh-CN":{"name":"测试分类"},"en-US":{"name":"Test category"}}}`
	cid := idFrom(call("POST", "/categories", categoryBody, "0000"))
	cs := strconv.FormatInt(cid, 10)
	assertAudit("PUT", "/categories/"+cs, categoryBody)
	call("PUT", "/categories/order", `{"items":[{"id":`+cs+`,"parentId":`+cs+`,"sort":1}]}`, "1006")
	assertAudit("PUT", "/categories/order", `{"items":[{"id":`+cs+`,"parentId":null,"sort":3}]}`)
	call("DELETE", "/categories/"+strconv.FormatInt(categoryID, 10), "", "1006")
	assertAudit("DELETE", "/categories/"+cs, "")
	mirror := `{"key":"test-mirror","sourceLocale":"zh-CN","i18n":{"zh-CN":{"name":"测试镜像"},"en-US":{"name":"Test mirror"}},"probeUrl":"https://example.com/probe","recommended":false,"enabled":true,"sort":80}`
	mid := idFrom(call("POST", "/mirrors", mirror, "0000"))
	ms := strconv.FormatInt(mid, 10)
	assertAudit("PUT", "/mirrors/"+ms, mirror)
	var mirrors struct{ Data []map[string]any }
	require.NoError(t, json.Unmarshal(call("GET", "/mirrors", "", "0000").Body.Bytes(), &mirrors))
	for _, row := range mirrors.Data {
		if row["key"] == "test-mirror" {
			require.Equal(t, "tester", row["updateBy"])
			require.NotEmpty(t, row["updateTime"])
			require.NotEmpty(t, row["createTime"])
		}
	}
	assertAudit("DELETE", "/mirrors/"+ms, "")
	assertAudit("PUT", "/app-config/test.value", `{"value":{"enabled":true},"description":"测试值"}`)
	assertAudit("PUT", "/app-config/test.value", `{"value":null}`)
	call("GET", "/app-config", "", "0000")
	call("PUT", "/app-config/catalog.retentionCursor", `{"value":0}`, "1004")
	synonym := `{"terms":["测试同义词","sample"],"enabled":true}`
	syn := idFrom(call("POST", "/search/synonyms", synonym, "0000"))
	syns := strconv.FormatInt(syn, 10)
	assertAudit("PUT", "/search/synonyms/"+syns, synonym)
	assertAudit("DELETE", "/search/synonyms/"+syns, "")
	var fid int64
	require.NoError(t, st.DB.WithContext(ctx).Raw("INSERT INTO feedback(type,content,platform) VALUES('other','测试反馈','web') RETURNING id").Scan(&fid).Error)
	fs := strconv.FormatInt(fid, 10)
	call("GET", "/feedback/"+fs, "", "0000")
	assertAudit("PUT", "/feedback/"+fs, `{"status":"resolved","handlerNote":"已处理"}`)
	user := `{"userName":"second","password":"another-test-password","nickName":"第二用户","status":"1","roles":["R_EDITOR"]}`
	uid := idFrom(call("POST", "/system/users", user, "0000"))
	us := strconv.FormatInt(uid, 10)
	assertAudit("PUT", "/system/users/"+us, `{"nickName":"已改名","roles":["R_REVIEWER"]}`)
	assertAudit("PUT", "/system/users/"+us+"/password", `{"newPassword":"reset-test-password"}`)
	assertAudit("POST", "/system/users/"+us+"/force-logout", "")
	assertAudit("PUT", "/system/users/"+us, `{"roles":["R_SUPER"]}`)
	assertAudit("DELETE", "/system/users/"+us, "")
	call("DELETE", "/system/users/"+strconv.FormatInt(identity.ID, 10), "", "1006")
	role := `{"roleCode":"R_TEST","roleName":"测试角色","status":"1"}`
	rid := idFrom(call("POST", "/system/roles", role, "0000"))
	rs := strconv.FormatInt(rid, 10)
	assertAudit("PUT", "/system/roles/"+rs, role)
	assertAudit("PUT", "/system/roles/"+rs+"/permissions", `{"permissionCodes":["catalog:package:view"]}`)
	call("PUT", "/system/roles/"+rs+"/permissions", `{"permissionCodes":["unknown"]}`, "1006")
	assertAudit("DELETE", "/system/roles/"+rs, "")
	var builtin int64
	require.NoError(t, st.DB.WithContext(ctx).Raw("SELECT id FROM admin_roles WHERE role_code='R_EDITOR'").Scan(&builtin).Error)
	call("DELETE", "/system/roles/"+strconv.FormatInt(builtin, 10), "", "1006")
	var automatic int64
	require.NoError(t, st.DB.WithContext(ctx).Raw("INSERT INTO changelog_sources(package_id,type,config,priority,resolved_by) VALUES(?,'homebrew_commits','{}',90,'auto') RETURNING id", pkg.ID).Scan(&automatic).Error)
	call("GET", "/packages/"+pid+"/changelog-sources", "", "0000")
	assertAudit("POST", "/packages/"+pid+"/resync", "")
	assertAudit("POST", "/jobs/trigger/snapshot_build", "")
	for _, retired := range []string{"enrich_schedule", "enrich_package", "translate_schedule", "translate_release", "assets_icons"} {
		call("POST", "/jobs/trigger/"+retired, "", "1001")
	}
	require.Equal(t, 2, queue.calls)
	require.Equal(t, "manual", queue.metadata.Trigger)
	require.Equal(t, identity.ID, *queue.metadata.TriggeredBy)
	for _, path := range []string{"/packages/" + pid, "/categories/tree", "/assets", "/jobs/runs", "/system/audit-logs", "/system/users", "/system/roles"} {
		call("GET", path, "", "0000")
	}
	var logs string
	require.NoError(t, st.DB.WithContext(ctx).Raw("SELECT string_agg(COALESCE(before::text,'')||COALESCE(after::text,''),'') FROM audit_logs").Scan(&logs).Error)
	require.NotContains(t, logs, "test-password")
	require.NotContains(t, logs, "password_hash")
	require.NotContains(t, logs, pair.RefreshToken)
	var changes int64
	require.NoError(t, st.DB.WithContext(ctx).Table("catalog_changes").Where("reason='content'").Count(&changes).Error)
	require.Greater(t, changes, int64(10))
	stored, err := st.PublicPackage(ctx, "cask", "sample", "zh-CN")
	require.NoError(t, err)
	require.Equal(t, "manual", stored.I18n.Status)
	require.Equal(t, "human", stored.I18n.Source)
	require.Equal(t, "中文编辑器", *stored.I18n.Summary)
	// Coverage still counts editorial notes for the current version; the retired LLM panel is absent, including when translation tasks are pending.
	var releaseID int64
	require.NoError(t, st.DB.Raw("INSERT INTO releases(package_id,source,source_key,version,title,source_locale) VALUES(?,'manual','dashboard','1','test','en-US') RETURNING id", pkg.ID).Scan(&releaseID).Error)
	checkDashboard := func(done int) {
		var response struct {
			Data struct {
				Coverage struct{ LatestVersionNotes struct{ Done int } }
			}
		}
		require.NoError(t, json.Unmarshal(call("GET", "/dashboard/overview", "", "0000").Body.Bytes(), &response))
		require.Equal(t, done, response.Data.Coverage.LatestVersionNotes.Done)
	}
	checkDashboard(0)
	require.NoError(t, st.DB.Exec("UPDATE releases SET source='editorial' WHERE id=?", releaseID).Error)
	require.NoError(t, st.DB.Exec("INSERT INTO release_i18n(release_id,locale,status) VALUES(?,'en-US','pending')", releaseID).Error)
	checkDashboard(1)
	require.NoError(t, st.DB.Exec("INSERT INTO release_i18n(release_id,locale,status) VALUES(?,'ja-JP','pending'),(?,'ru-RU','pending')", releaseID, releaseID).Error)
	checkDashboard(1)
	require.NoError(t, st.DB.Exec("UPDATE releases SET version='0.9' WHERE id=?", releaseID).Error)
	checkDashboard(0)
	require.NoError(t, st.DB.Exec("UPDATE releases SET hidden=true WHERE id=?", releaseID).Error)
	checkDashboard(0)

}
