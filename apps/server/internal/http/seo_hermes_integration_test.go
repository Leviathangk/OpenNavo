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
	"github.com/opennavo/opennavo/server/internal/service/auth"
	"github.com/opennavo/opennavo/server/internal/service/management"
	"github.com/opennavo/opennavo/server/internal/service/seed"
	"github.com/opennavo/opennavo/server/internal/service/seotasks"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/opennavo/opennavo/server/seeds"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestHermesTunerAndBatchHTTPContracts(t *testing.T) {
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
	call := func(method, path string, body any, token, want string) map[string]any {
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
		value, _ := result["data"].(map[string]any)
		return value
	}
	client := call("POST", "/agent/clients", map[string]any{"name": "Fixture Hermes", "notes": "isolated test"}, "", "0000")
	clientID := int64(client["id"].(float64))
	tokenPath := "/agent/clients/" + strconv.FormatInt(clientID, 10) + "/tokens"
	tuner := call("POST", tokenPath, map[string]any{"name": "Fixture tuner", "permissions": []string{"seo:automation:tune"}}, "", "0000")["plaintext"].(string)
	execution := call("POST", tokenPath, map[string]any{"name": "Fixture execution", "permissions": []string{"translation:review", "catalog:category:edit", "seo:task:handle"}}, "", "0000")
	token := execution["plaintext"].(string)
	tokenID := int64(execution["token"].(map[string]any)["id"].(float64))
	call("POST", tokenPath, map[string]any{"name": "Mixed denied", "permissions": []string{"seo:automation:tune", "translation:review"}}, "", "1001")
	call("PUT", "/agent/tokens/"+strconv.FormatInt(tokenID, 10), map[string]any{"permissions": []string{"seo:automation:tune", "seo:task:handle"}}, "", "1001")
	call("GET", "/seo/automation", nil, token, "1004")
	view := call("GET", "/seo/automation", nil, tuner, "0000")
	call("PUT", "/seo/automation", map[string]any{"automation": view["automation"], "expectedRevision": view["revision"], "idempotencyKey": "fixture-http-tune", "reason": "fixture"}, tuner, "0000")
	require.NoError(t, st.DB.Exec("INSERT INTO categories(id,slug,icon,source_locale) VALUES(880,'fixture-batch-http','lucide:box','en-US')").Error)
	ref := content.Ref{Entity: "category", ID: 880}
	require.NoError(t, st.WithTx(ctx, func(tx *gorm.DB) error {
		return (&store.Store{DB: tx}).WriteContentText(ctx, ref, "en-US", map[string]any{"name": "File manager"}, true)
	}))
	source := call("GET", "/translations/source?entity=category&id=880", nil, token, "0000")
	tasks := &seotasks.Service{DB: st.DB}
	leased, err := tasks.Claim(ctx, seotasks.Owner(clientID, tokenID), 1, []string{"translate"})
	require.NoError(t, err)
	require.Len(t, leased, 1)
	body := map[string]any{"ref": ref, "model": "fixture", "taskId": leased[0].ID, "fencing": leased[0].Fencing, "translations": []any{map[string]any{"locale": "zh-CN", "sourceHash": source["sourceHash"], "expectedRevision": seotasks.Hash([]byte("null")), "idempotencyKey": "fixture-http-batch", "fields": map[string]any{"name": "文件管理工具"}}}}
	result := call("POST", "/translations/batch", body, token, "0000")
	require.Equal(t, "written", result["results"].([]any)[0].(map[string]any)["status"])
	imported := call("POST", "/seo/tasks/import", map[string]any{"idempotencyKey": "fixture-http-import", "profile": "fixture", "cursor": map[string]any{"sequence": 1}, "entries": []any{map[string]any{"ref": ref, "field": "name", "locale": "en-US", "sourceVersion": source["sourceHash"], "state": "processed", "reason": "fixture", "evidence": map[string]any{}, "lastAttemptAt": time.Now().UTC().Format(time.RFC3339), "valueHash": seotasks.Hash([]byte(`"File manager"`))}}}, token, "0000")
	require.Equal(t, "done", imported["results"].([]any)[0].(map[string]any)["state"])
	// Invalid lease is a per-language failure, including a contract-valid readback.
	body["fencing"] = leased[0].Fencing + 1
	body["translations"].([]any)[0].(map[string]any)["idempotencyKey"] = "fixture-http-invalid-lease"
	result = call("POST", "/translations/batch", body, token, "0000")
	require.Equal(t, "failed", result["results"].([]any)[0].(map[string]any)["status"])
}
