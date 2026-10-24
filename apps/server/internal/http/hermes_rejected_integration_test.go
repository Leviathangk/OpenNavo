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
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/homebrew"
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

func TestHermesRejectionsAndOptionalReleaseSections(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewFactsStore(t)
	seedData, err := seeds.Load()
	require.NoError(t, err)
	hash, err := seed.HashPassword("fixture-only-password")
	require.NoError(t, err)
	require.NoError(t, st.Seed(ctx, seedData, "fixture-editor", hash))
	cfg := config.Config{JWTSecret: strings.Repeat("h", 32), JWTAccessTTL: time.Hour, JWTRefreshTTL: 24 * time.Hour, AgentGatewaySecret: strings.Repeat("g", 32), WebBaseURL: "https://example.com"}
	authentication, err := auth.New(st, cfg)
	require.NoError(t, err)
	pair, err := authentication.Login(ctx, "fixture-editor", "fixture-only-password", store.AuditActor{})
	require.NoError(t, err)
	manager := &management.Service{Store: st, Config: cfg}
	server, err := httpserver.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), st, httpserver.Dependencies{Admin: &admin.Handler{Auth: authentication, Management: manager}})
	require.NoError(t, err)
	spec, err := adminapi.GetSpec()
	require.NoError(t, err)
	token := ""
	call := func(method, path string, body map[string]any, agent bool, code string) map[string]any {
		t.Helper()
		base, bearer := "/admin-api", pair.Token
		if agent {
			base, bearer = "/agent-api", token
		}
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		req := httptest.NewRequest(method, base+path, strings.NewReader(string(raw)))
		req.Header.Set("Authorization", "Bearer "+bearer)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept-Language", "zh-CN")
		req.Header.Set("X-Agent-Gateway-Key", cfg.AgentGatewaySecret)
		req.Header.Set("X-Agent-Client-IP", "127.0.0.1")
		req.Header.Set("X-Agent-Tool", "fixture_hermes")
		req.Header.Set("X-Request-ID", uuid.NewString())
		out := httptest.NewRecorder()
		server.Engine.ServeHTTP(out, req)
		require.Equal(t, 200, out.Code)
		var envelope map[string]any
		require.NoError(t, json.Unmarshal(out.Body.Bytes(), &envelope))
		require.Equal(t, code, envelope["code"], "%s %s: %s", method, path, out.Body.String())
		if code == "0000" {
			testutil.ValidateResponse(t, spec, base, req, out)
		}
		return envelope
	}
	client := call("POST", "/agent/clients", map[string]any{"name": "Fixture editor"}, false, "0000")["data"].(map[string]any)
	tokenPath := "/agent/clients/" + strconv.FormatInt(int64(client["id"].(float64)), 10) + "/tokens"
	credential := call("POST", tokenPath, map[string]any{"name": "Fixture notes", "permissions": []string{"changelog:release:edit", "catalog:package:edit"}}, false, "0000")["data"].(map[string]any)
	token = credential["plaintext"].(string)
	pkg, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"fixture-notes","version":"1.0","name":["Fixture Notes"]}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: pkg}}))
	rows, err := st.CatalogRows(ctx, "cask")
	require.NoError(t, err)
	pid := rows["fixture-notes"].ID
	notesPath := "/packages/" + strconv.FormatInt(pid, 10) + "/versions/1.0/notes"
	readSections := func() string {
		t.Helper()
		var sections string
		require.NoError(t, st.DB.Raw(`SELECT i.sections::text FROM release_i18n i JOIN releases r ON r.id=i.release_id WHERE r.package_id=? AND r.source='editorial' AND NOT r.hidden AND i.locale='en-US'`, pid).Scan(&sections).Error)
		return sections
	}
	// The real Agent middleware and generated request validator must accept omission.
	call("PUT", notesPath, map[string]any{"sourceLocale": "en-US", "summary": "Initial notes"}, true, "0000")
	require.JSONEq(t, `[]`, readSections())
	sections := []any{map[string]any{"area": "Changes", "items": []any{"Keep this detail"}}}
	call("PUT", notesPath, map[string]any{"sourceLocale": "en-US", "summary": "Structured notes", "sections": sections}, true, "0000")
	want := readSections()
	call("PUT", notesPath, map[string]any{"sourceLocale": "en-US", "summary": "Agent summary edit"}, true, "0000")
	require.JSONEq(t, want, readSections())
	call("PUT", notesPath, map[string]any{"sourceLocale": "en-US", "summary": "Admin summary edit"}, false, "0000")
	require.JSONEq(t, want, readSections())
	call("PUT", notesPath+"?dryRun=true", map[string]any{"sourceLocale": "en-US", "summary": "Preview clear", "sections": []any{}}, true, "0000")
	require.JSONEq(t, want, readSections())
	call("PUT", notesPath, map[string]any{"sourceLocale": "en-US", "summary": "Null rejected", "sections": nil}, true, "1001")
	require.JSONEq(t, want, readSections())
	call("PUT", notesPath, map[string]any{"sourceLocale": "en-US", "summary": "Explicit clear", "sections": []any{}}, true, "0000")
	require.JSONEq(t, `[]`, readSections())
	// A service-level error must retain the cause through both shared handlers.
	invalidDate := map[string]any{"sourceLocale": "en-US", "summary": "Fixture", "publishedAt": "0001-01-01T00:00:00Z"}
	result := call("PUT", notesPath, invalidDate, true, "1001")
	require.Equal(t, "rejected: date_invalid:publishedAt", result["msg"])
	result = call("PUT", notesPath, invalidDate, false, "1001")
	require.Equal(t, domain.Message(domain.CodeValidation, "zh-CN"), result["msg"])
}
