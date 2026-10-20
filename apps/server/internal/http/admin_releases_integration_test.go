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

	"github.com/opennavo/opennavo/server/gen/adminapi"
	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	httpserver "github.com/opennavo/opennavo/server/internal/http"
	"github.com/opennavo/opennavo/server/internal/http/admin"
	"github.com/opennavo/opennavo/server/internal/http/public"
	"github.com/opennavo/opennavo/server/internal/service/auth"
	"github.com/opennavo/opennavo/server/internal/service/catalog"
	"github.com/opennavo/opennavo/server/internal/service/management"
	"github.com/opennavo/opennavo/server/internal/service/seed"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/opennavo/opennavo/server/seeds"
	"github.com/stretchr/testify/require"
)

func TestAdminReleaseContractsApprovalStalenessSourcesAndAudit(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewFactsStore(t)
	hash, err := seed.HashPassword("test-password-strong")
	require.NoError(t, err)
	data, err := seeds.Load()
	require.NoError(t, err)
	require.NoError(t, st.Seed(ctx, data, "tester", hash))
	p, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"sample","version":"1.2.0","name":["Sample"],"desc":"Editor"}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p}}))
	require.NoError(t, st.DB.WithContext(ctx).Exec("DELETE FROM job_outbox").Error)
	var pid, rid int64
	require.NoError(t, st.DB.WithContext(ctx).Raw("SELECT id FROM packages").Scan(&pid).Error)
	require.NoError(t, st.DB.WithContext(ctx).Raw("INSERT INTO releases(package_id,source,source_key,version,title,body_markdown,body_hash) VALUES(?,'manual','v1.2.0','1.2.0','Original','Notes','hash1') RETURNING id", pid).Scan(&rid).Error)
	require.NoError(t, st.DB.WithContext(ctx).Exec(`INSERT INTO release_i18n(release_id,locale,status,summary,body_markdown,source_hash) VALUES(?,'zh-CN','machine','摘要','译文','hash1')`, rid).Error)
	cfg := config.Config{JWTSecret: strings.Repeat("a", 32), JWTAccessTTL: time.Hour, JWTRefreshTTL: time.Hour}
	authentication, err := auth.New(st, cfg)
	require.NoError(t, err)
	pair, err := authentication.Login(ctx, "tester", "test-password-strong", store.AuditActor{})
	require.NoError(t, err)
	q := &recordingQueue{}
	svc := &management.Service{Store: st, Queue: q}
	pub := &catalog.PublicService{Store: st, Cache: cache.Redis{Client: st.Redis}}
	server, err := httpserver.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), st, httpserver.Dependencies{Admin: &admin.Handler{Auth: authentication, Management: svc}, Public: &public.Handler{Catalog: pub}})
	require.NoError(t, err)
	spec, err := adminapi.GetSpec()
	require.NoError(t, err)
	pspec, err := publicapi.GetSpec()
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
		require.Equal(t, 200, w.Code)
		var out struct{ Code string }
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
		require.Equal(t, code, out.Code, path+" "+w.Body.String())
		return w
	}
	stopCache, err := cache.Subscribe(ctx, st.Redis, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	defer stopCache()
	warm, err := pub.ReleasePage(ctx, "cask", "sample", "zh-CN", 1, 100, false)
	require.NoError(t, err)
	require.Equal(t, publicapi.TranslationInfoStatusMachine, warm.Records[0].Translation.Status)
	id := strconv.FormatInt(rid, 10)
	pkg := strconv.FormatInt(pid, 10)
	call("GET", "/releases?packageId="+pkg+"&q=sample&source=manual&translationStatus=machine&hidden=false", "", "0000")
	call("GET", "/releases/"+id, "", "0000")
	require.Contains(t, call("GET", "/translations/queue?type=release", "", "0000").Body.String(), `"total":1`)
	call("GET", "/translations/queue?type=package&status=failed", "", "0000")
	call("POST", "/translations/approve", `{"type":"release","ids":[`+id+`]}`, "0000")
	require.Eventually(t, func() bool { return st.Redis.Exists(ctx, "c:rel:c3:cask:sample:zh-CN").Val() == 0 }, 2*time.Second, 10*time.Millisecond)
	r := httptest.NewRequest("GET", "/api/v1/packages/cask/sample/releases?locale=zh-CN", nil)
	w := httptest.NewRecorder()
	server.Engine.ServeHTTP(w, r)
	testutil.ValidateResponse(t, pspec, "/api/v1", r, w)
	require.Contains(t, w.Body.String(), `"status":"manual"`)
	call("POST", "/translations/approve", `{"type":"release","ids":[`+id+`]}`, "1006")
	require.NoError(t, st.DB.WithContext(ctx).Exec("UPDATE releases SET body_hash='hash2'; UPDATE release_i18n SET status='machine'").Error)
	require.Contains(t, call("GET", "/releases/"+id, "", "0000").Body.String(), `"stale":true`)
	call("POST", "/translations/approve", `{"type":"release","ids":[`+id+`]}`, "1006")
	call("PUT", "/releases/"+id+"/i18n/zh-CN", `{"summary":"已复核","sections":[{"area":"编辑器","items":["修复"]}],"bodyMarkdown":"人工正文"}`, "0000")
	require.Contains(t, call("GET", "/releases/"+id, "", "0000").Body.String(), `"stale":false`)
	call("PUT", "/releases/"+id, `{"hidden":true,"title":null}`, "0000")
	require.Contains(t, call("GET", "/releases?hidden=true", "", "0000").Body.String(), `"total":1`)
	call("PUT", "/releases/"+id, `{"hidden":false}`, "0000")
	// S8: manual review never schedules server-side automatic translation.
	require.Zero(t, q.calls)
	require.NoError(t, st.DB.WithContext(ctx).Exec(`INSERT INTO package_i18n(package_id,locale,summary,status,source,source_hash) VALUES(?,'zh-CN','编辑器','machine','llm',encode(sha256(convert_to('Editor','UTF8')),'hex')) ON CONFLICT(package_id,locale) DO UPDATE SET status='machine',source_hash=EXCLUDED.source_hash`, pid).Error)
	call("POST", "/translations/approve", `{"type":"package","ids":[`+pkg+`]}`, "0000")
	call("GET", "/packages/"+pkg+"/changelog-sources", "", "0000")
	call("GET", "/releases/999", "", "1002")
	var count int64
	require.NoError(t, st.DB.WithContext(ctx).Raw("SELECT count(*) FROM audit_logs WHERE entity_type IN('release','translation','changelog_source') AND after IS NOT NULL").Scan(&count).Error)
	require.Equal(t, int64(5), count)
}
