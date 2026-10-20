//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/opennavo/opennavo/server/gen/adminapi"
	"github.com/opennavo/opennavo/server/internal/config"
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

func TestSEOGeoHTTPPermissionsDryRunAndContract(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewFactsStore(t)
	data, err := seeds.Load()
	require.NoError(t, err)
	hash, err := seed.HashPassword("fixture-geo-password")
	require.NoError(t, err)
	require.NoError(t, st.Seed(ctx, data, "fixture-geo", hash))
	cfg := config.Config{JWTSecret: strings.Repeat("j", 32), JWTAccessTTL: time.Hour, JWTRefreshTTL: 24 * time.Hour, AgentGatewaySecret: strings.Repeat("g", 32), WebBaseURL: "https://fixture.example"}
	authentication, err := auth.New(st, cfg)
	require.NoError(t, err)
	pair, err := authentication.Login(ctx, "fixture-geo", "fixture-geo-password", store.AuditActor{})
	require.NoError(t, err)
	server, err := httpserver.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), st, httpserver.Dependencies{Admin: &admin.Handler{Auth: authentication, Management: &management.Service{Store: st, Config: cfg}}})
	require.NoError(t, err)
	spec, err := adminapi.GetSpec()
	require.NoError(t, err)
	call := func(method, path string, body any, token, want string) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(body)
		base := "/agent-api"
		if token == "" {
			base = "/admin-api"
			token = pair.Token
		}
		req := httptest.NewRequest(method, base+path, strings.NewReader(string(raw)))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Agent-Gateway-Key", cfg.AgentGatewaySecret)
		req.Header.Set("X-Agent-Client-IP", "127.0.0.1")
		req.Header.Set("X-Agent-Tool", "seo_geo_record")
		req.Header.Set("X-Request-ID", uuid.NewString())
		out := httptest.NewRecorder()
		server.Engine.ServeHTTP(out, req)
		require.Equal(t, 200, out.Code, out.Body.String())
		var v map[string]any
		require.NoError(t, json.Unmarshal(out.Body.Bytes(), &v))
		require.Equal(t, want, v["code"], out.Body.String())
		if want == "0000" {
			testutil.ValidateResponse(t, spec, base, req, out)
		}
		result, _ := v["data"].(map[string]any)
		return result
	}
	client := call("POST", "/agent/clients", map[string]any{"name": "Fixture GEO"}, "", "0000")
	path := "/agent/clients/" + strconv.FormatInt(int64(client["id"].(float64)), 10) + "/tokens"
	token := func(permission string) string {
		return call("POST", path, map[string]any{"name": permission, "permissions": []string{permission}}, "", "0000")["plaintext"].(string)
	}
	reader, writer, tuner := token("seo:insight:view"), token("seo:task:handle"), token("seo:automation:tune")
	raw, err := os.ReadFile("../service/management/testdata/seo_geo_import.json")
	require.NoError(t, err)
	var body map[string]any
	require.NoError(t, json.Unmarshal(raw, &body))
	require.Nil(t, call("GET", "/seo/overview", nil, reader, "0000")["geo"])
	call("POST", "/seo/geo", body, reader, "1004")
	call("POST", "/seo/geo", body, tuner, "1004")
	call("GET", "/seo/geo", nil, writer, "1004")
	call("POST", "/seo/geo?dryRun=true", body, writer, "0000")
	require.EqualValues(t, 0, call("GET", "/seo/geo", nil, reader, "0000")["total"])
	result := call("POST", "/seo/geo", body, writer, "0000")
	require.Equal(t, result, call("POST", "/seo/geo", body, writer, "0000"))
	detailPath := "/seo/geo/" + strconv.FormatInt(int64(result["id"].(float64)), 10)
	call("GET", detailPath, nil, reader, "0000")
	call("GET", detailPath, nil, writer, "1004")
	call("GET", detailPath, nil, "", "0000")
	require.NotNil(t, call("GET", "/seo/overview", nil, reader, "0000")["geo"])
	body["snapshot"].(map[string]any)["rawResponse"] = "fixture must never persist"
	call("POST", "/seo/geo", body, writer, "1001")
	var leaked int64
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM seo_geo_snapshots WHERE snapshot::text LIKE '%fixture must never persist%'").Scan(&leaked).Error)
	require.Zero(t, leaked)
}
