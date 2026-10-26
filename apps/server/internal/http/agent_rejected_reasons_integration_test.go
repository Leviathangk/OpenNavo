//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/domain"
	httpserver "github.com/opennavo/opennavo/server/internal/http"
	"github.com/opennavo/opennavo/server/internal/http/admin"
	"github.com/opennavo/opennavo/server/internal/service/auth"
	"github.com/opennavo/opennavo/server/internal/service/management"
	"github.com/opennavo/opennavo/server/internal/service/seed"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/opennavo/opennavo/server/seeds"
	"github.com/stretchr/testify/require"
)

var agentReasonFormat = regexp.MustCompile(`^rejected: [a-z][a-z0-9_]*(:[A-Za-z0-9_.@+/-]+)*$`)

// Agents get the cause of a refusal, not just its code; the admin console shows
// the same cause after its localized message.
func TestAgentRefusalsNameTheirReason(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewFactsStore(t)
	data, err := seeds.Load()
	require.NoError(t, err)
	hash, err := seed.HashPassword("fixture-admin-password")
	require.NoError(t, err)
	require.NoError(t, st.Seed(ctx, data, "fixture-admin", hash))
	cfg := config.Config{JWTSecret: strings.Repeat("r", 32), JWTAccessTTL: time.Hour, JWTRefreshTTL: 24 * time.Hour, AgentGatewaySecret: strings.Repeat("g", 32), WebBaseURL: "https://example.com"}
	authentication, err := auth.New(st, cfg)
	require.NoError(t, err)
	pair, err := authentication.Login(ctx, "fixture-admin", "fixture-admin-password", store.AuditActor{})
	require.NoError(t, err)
	manager := &management.Service{Store: st, Config: cfg}
	server, err := httpserver.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), st, httpserver.Dependencies{Admin: &admin.Handler{Auth: authentication, Management: manager}})
	require.NoError(t, err)
	call := func(method, path string, body any, token string, headers map[string]string) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(body)
		base, bearer := "/agent-api", token
		if token == "" {
			base, bearer = "/admin-api", pair.Token
		}
		req := httptest.NewRequest(method, base+path, strings.NewReader(string(raw)))
		req.Header.Set("Authorization", "Bearer "+bearer)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Agent-Gateway-Key", cfg.AgentGatewaySecret)
		req.Header.Set("X-Agent-Client-IP", "127.0.0.1")
		req.Header.Set("X-Agent-Tool", "fixture_reasons")
		req.Header.Set("X-Request-ID", uuid.NewString())
		for name, value := range headers {
			req.Header.Set(name, value)
		}
		out := httptest.NewRecorder()
		server.Engine.ServeHTTP(out, req)
		require.Equal(t, 200, out.Code)
		var result map[string]any
		require.NoError(t, json.Unmarshal(out.Body.Bytes(), &result))
		return result
	}
	token := func(name string, permissions []string) string {
		t.Helper()
		client := call("POST", "/agent/clients", map[string]any{"name": "Reasons " + name}, "", nil)["data"].(map[string]any)
		path := "/agent/clients/" + strconv.FormatInt(int64(client["id"].(float64)), 10) + "/tokens"
		created := call("POST", path, map[string]any{"name": name, "permissions": permissions, "allowDelete": false}, "", nil)
		require.Equal(t, "0000", created["code"], created)
		return created["data"].(map[string]any)["plaintext"].(string)
	}
	editor := token("editor", []string{"content:glossary:edit", "catalog:asset:upload"})
	viewer := token("viewer", []string{"catalog:package:view"})
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES(990,'cask','reasons-app','reasons-app','homebrew/cask','Reasons App','1','1','{}','fixture');
 INSERT INTO assets(id,kind,storage_key,url,width,height,bytes,sha256,mime) VALUES(990,'screenshot','reasons-shot','https://assets.example.test/shot.png',1280,800,1,'shot','image/png')`).Error)
	term := map[string]any{"term": "Reasons", "translations": map[string]any{}, "doNotTranslate": true}
	for _, tc := range []struct {
		name, method, path string
		body               any
		token              string
		headers            map[string]string
		code, reason       string
	}{
		{"contract required field", "PUT", "/glossary", map[string]any{"term": "Reasons", "translations": map[string]any{}}, editor, nil, domain.CodeValidation, "field_required:doNotTranslate"},
		{"contract unknown field", "PUT", "/glossary", map[string]any{"term": "Reasons", "translations": map[string]any{}, "doNotTranslate": true, "bogus": 1}, editor, nil, domain.CodeValidation, "unknown_field:bogus"},
		{"malformed header", "PUT", "/glossary", term, editor, map[string]string{"Idempotency-Key": "short"}, domain.CodeValidation, "header_invalid:Idempotency-Key"},
		{"missing permission", "PUT", "/glossary", term, viewer, nil, domain.CodeForbidden, "permission_required:content:glossary:edit"},
		{"delete without grant", "DELETE", "/glossary/1", nil, editor, nil, domain.CodeForbidden, "delete_not_allowed"},
		{"business rule", "PUT", "/packages/990/icon", map[string]any{"assetId": 990}, editor, nil, domain.CodeValidation, "asset_kind_mismatch:icon"},
		{"stale revision", "PUT", "/glossary", term, editor, map[string]string{"X-Expected-Revision": "5"}, domain.CodeConflict, "revision_conflict:0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := call(tc.method, tc.path, tc.body, tc.token, tc.headers)
			require.Equal(t, tc.code, result["code"], result)
			require.Equal(t, "rejected: "+tc.reason, result["msg"])
			require.Regexp(t, agentReasonFormat, result["msg"])
		})
	}
	// The admin console keeps its localized message and appends the same cause.
	result := call("PUT", "/glossary", map[string]any{"term": "Reasons", "translations": map[string]any{}, "doNotTranslate": true, "bogus": 1}, "", map[string]string{"Accept-Language": "zh-CN"})
	require.Equal(t, domain.CodeValidation, result["code"])
	require.Equal(t, domain.Message(domain.CodeValidation, "zh-CN")+" (unknown_field:bogus)", result["msg"])
}
