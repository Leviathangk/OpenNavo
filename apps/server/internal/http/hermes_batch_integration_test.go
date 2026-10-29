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

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/gen/adminapi"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/content"
	httpserver "github.com/opennavo/opennavo/server/internal/http"
	"github.com/opennavo/opennavo/server/internal/http/admin"
	"github.com/opennavo/opennavo/server/internal/service/agentgate"
	"github.com/opennavo/opennavo/server/internal/service/auth"
	"github.com/opennavo/opennavo/server/internal/service/management"
	"github.com/opennavo/opennavo/server/internal/service/seed"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/opennavo/opennavo/server/seeds"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestHermesBatchHTTPContracts(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewFactsStore(t)
	data, err := seeds.Load()
	require.NoError(t, err)
	hash, err := seed.HashPassword("fixture-admin-password")
	require.NoError(t, err)
	require.NoError(t, st.Seed(ctx, data, "fixture-admin", hash))
	cfg := config.Config{JWTSecret: strings.Repeat("h", 32), JWTAccessTTL: time.Hour, JWTRefreshTTL: 24 * time.Hour, AgentGatewaySecret: strings.Repeat("g", 32), WebBaseURL: "https://example.com"}
	authentication, err := auth.New(st, cfg)
	require.NoError(t, err)
	pair, err := authentication.Login(ctx, "fixture-admin", "fixture-admin-password", store.AuditActor{})
	require.NoError(t, err)
	manager := &management.Service{Store: st, Config: cfg}
	server, err := httpserver.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), st, httpserver.Dependencies{Admin: &admin.Handler{Auth: authentication, Management: manager}})
	require.NoError(t, err)
	spec, err := adminapi.GetSpec()
	require.NoError(t, err)
	envelope := func(method, path string, body any, token, want string) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(body)
		base := "/agent-api"
		bearer := token
		if token == "" {
			base = "/admin-api"
			bearer = pair.Token
		}
		req := httptest.NewRequest(method, base+path, strings.NewReader(string(raw)))
		req.Header.Set("Authorization", "Bearer "+bearer)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Agent-Gateway-Key", cfg.AgentGatewaySecret)
		req.Header.Set("X-Agent-Client-IP", "127.0.0.1")
		req.Header.Set("X-Agent-Tool", "fixture_hermes")
		req.Header.Set("X-Request-ID", uuid.NewString())
		out := httptest.NewRecorder()
		server.Engine.ServeHTTP(out, req)
		require.Equal(t, 200, out.Code)
		var result map[string]any
		require.NoError(t, json.Unmarshal(out.Body.Bytes(), &result))
		require.Equal(t, want, result["code"], "%s %s", method, path)
		if want == "0000" {
			testutil.ValidateResponse(t, spec, base, req, out)
		}
		return result
	}
	call := func(method, path string, body any, token, want string) map[string]any {
		t.Helper()
		value, _ := envelope(method, path, body, token, want)["data"].(map[string]any)
		return value
	}
	client := call("POST", "/agent/clients", map[string]any{"name": "Fixture Hermes", "notes": "isolated test"}, "", "0000")
	clientID := int64(client["id"].(float64))
	tokenPath := "/agent/clients/" + strconv.FormatInt(clientID, 10) + "/tokens"
	execution := call("POST", tokenPath, map[string]any{"name": "Fixture execution", "permissions": []string{"translation:review", "catalog:category:edit", "content:article:edit", "site:index:view"}}, "", "0000")
	token := execution["plaintext"].(string)
	for _, retired := range []string{"seo:automation:tune", "seo:task:handle", "seo:insight:view"} {
		call("POST", tokenPath, map[string]any{"name": "Retired", "permissions": []string{retired}}, "", "1001")
	}
	require.NoError(t, st.DB.Exec("INSERT INTO categories(id,slug,icon,source_locale) VALUES(880,'fixture-batch-http','lucide:box','en-US')").Error)
	ref := content.Ref{Entity: "category", ID: 880}
	require.NoError(t, st.WithTx(ctx, func(tx *gorm.DB) error {
		return (&store.Store{DB: tx}).WriteContentText(ctx, ref, "en-US", map[string]any{"name": "File manager"}, true)
	}))
	source := call("GET", "/translations/source?entity=category&id=880", nil, token, "0000")
	// Old clients may still send the retired task fields; they are accepted and ignored.
	body := map[string]any{"ref": ref, "model": "fixture", "taskId": 1, "fencing": 1, "translations": []any{map[string]any{"locale": "zh-CN", "sourceHash": source["sourceHash"], "expectedRevision": agentgate.Hash([]byte("null")), "idempotencyKey": "fixture-http-batch", "fields": map[string]any{"name": "文件管理工具"}}}}
	result := call("POST", "/translations/batch", body, token, "0000")
	require.Equal(t, "written", result["results"].([]any)[0].(map[string]any)["status"])
	require.NotContains(t, result, "taskId")
	// Non-article failures preserve the existing readback for recovery.
	body["translations"].([]any)[0].(map[string]any)["fields"] = map[string]any{"name": "另一个名字"}
	result = call("POST", "/translations/batch", body, token, "0000")
	require.Equal(t, "failed", result["results"].([]any)[0].(map[string]any)["status"])
	require.Equal(t, "idempotency_key_reused", result["results"].([]any)[0].(map[string]any)["reason"])
	require.Equal(t, "文件管理工具", result["results"].([]any)[0].(map[string]any)["value"].(map[string]any)["fields"].(map[string]any)["name"])
	// Both HTTP entry points accept articleId without a revision ID and resolve
	// the published revision. Errors never include another revision's text.
	require.NoError(t, st.WithTx(ctx, func(tx *gorm.DB) error {
		return tx.Exec(`INSERT INTO articles(id,slug,status,working_revision_id,published_revision_id,first_published_at) VALUES
 (901,'translation-reference-http','published',1901,1901,now()),
 (902,'translation-reference-other','published',901,901,now()),
 (903,'translation-reference-draft','draft',1903,NULL,NULL);
 INSERT INTO article_revisions(id,article_id,number,source_locale,type) VALUES
 (1901,901,1,'en-US','news'), (901,902,1,'en-US','news'), (1903,903,1,'en-US','news');
 INSERT INTO article_revision_i18n(revision_id,locale,title,source_locale,status) VALUES
 (1901,'en-US','Reference guide','en-US','source'),
 (901,'en-US','Unrelated article sentinel','en-US','source'),
 (1903,'en-US','Draft article sentinel','en-US','source')`).Error
	}))
	for _, bearer := range []string{"", token} {
		view := call("GET", "/translations/source?entity=article&articleId=901", nil, bearer, "0000")
		require.EqualValues(t, 1901, view["ref"].(map[string]any)["id"])
		call("GET", "/translations/source?entity=article&id=1901", nil, bearer, "0000")
		zeroID := envelope("GET", "/translations/source?entity=article&id=0&articleId=901", nil, bearer, "1001")
		require.Contains(t, zeroID["msg"], "article_ref_mismatch")
		articleBody := map[string]any{
			"ref": map[string]any{"entity": "article"}, "articleId": 901,
			"translations": []any{map[string]any{"locale": "de-DE", "sourceHash": view["sourceHash"], "fields": map[string]any{"title": "Referenzhandbuch"}}},
		}
		written := call("POST", "/translations/batch", articleBody, bearer, "0000")
		require.Equal(t, "written", written["results"].([]any)[0].(map[string]any)["status"])
		articleBody["ref"] = map[string]any{"entity": "article", "id": 901}
		mismatch := envelope("POST", "/translations/batch", articleBody, bearer, "1001")
		require.Contains(t, mismatch["msg"], "article_ref_mismatch")
		require.Nil(t, mismatch["data"])
		articleBody["ref"] = map[string]any{"entity": "article", "id": 0}
		mismatch = envelope("POST", "/translations/batch", articleBody, bearer, "1001")
		require.Contains(t, mismatch["msg"], "article_ref_mismatch")
		require.Nil(t, mismatch["data"])
		articleBody["ref"] = map[string]any{"entity": "article", "id": 901}
		delete(articleBody, "articleId")
		rejected := envelope("POST", "/translations/batch", articleBody, bearer, "1001")
		require.Contains(t, rejected["msg"], "article_id_probably_passed")
		require.Nil(t, rejected["data"])
		rejected = envelope("GET", "/translations/source?entity=article&id=901", nil, bearer, "1001")
		require.Contains(t, rejected["msg"], "article_ref_must_be_published_revision")
		require.Nil(t, rejected["data"])
		draft := envelope("GET", "/translations/source?entity=article&articleId=903", nil, bearer, "1001")
		require.Contains(t, draft["msg"], "article_must_be_published_first")
		require.Nil(t, draft["data"])
	}
	var otherTitle string
	require.NoError(t, st.DB.Raw("SELECT title FROM article_revision_i18n WHERE revision_id=901 AND locale='en-US'").Scan(&otherTitle).Error)
	require.Equal(t, "Unrelated article sentinel", otherTitle)
	// Lists longer than the token's batch limit are rejected before any write.
	require.NoError(t, st.DB.Exec("UPDATE agent_settings SET batch_limit=1").Error)
	translations := body["translations"].([]any)
	body["translations"] = append(translations, map[string]any{"locale": "ja-JP", "sourceHash": source["sourceHash"], "expectedRevision": agentgate.Hash([]byte("null")), "idempotencyKey": "fixture-http-batch-ja", "fields": map[string]any{"name": "ファイル管理"}})
	rejected := envelope("POST", "/translations/batch", body, token, "1001")
	require.Equal(t, "rejected: batch_too_large:1", rejected["msg"])
}
