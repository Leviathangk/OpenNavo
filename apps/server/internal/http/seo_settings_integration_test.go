//go:build integration

package httpserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/gen/adminapi"
	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/config"
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

func TestSEOSettingsSecretsPublicSubsetAndIndexNowKeyFile(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	data, err := seeds.Load()
	require.NoError(t, err)
	hash, err := seed.HashPassword("isolated-test-password")
	require.NoError(t, err)
	require.NoError(t, st.Seed(ctx, data, "seo_tester", hash))
	cfg := config.Config{AppEnv: "dev", JWTSecret: strings.Repeat("a", 32), JWTAccessTTL: time.Hour, JWTRefreshTTL: 24 * time.Hour, WebBaseURL: "http://localhost:3000"}
	authentication, err := auth.New(st, cfg)
	require.NoError(t, err)
	pair, err := authentication.Login(ctx, "seo_tester", "isolated-test-password", store.AuditActor{})
	require.NoError(t, err)
	var logs bytes.Buffer
	app, err := httpserver.New(cfg, slog.New(slog.NewTextHandler(&logs, nil)), st, httpserver.Dependencies{
		Admin:  &admin.Handler{Auth: authentication, Management: &management.Service{Store: st, Config: cfg}},
		Public: &public.Handler{Catalog: &catalog.PublicService{Store: st, WebBaseURL: cfg.WebBaseURL, Secrets: cfg}},
	})
	require.NoError(t, err)
	adminSpec, err := adminapi.GetSpec()
	require.NoError(t, err)
	publicSpec, err := publicapi.GetSpec()
	require.NoError(t, err)
	const indexNowKey, cloudflareToken = "fixture-indexnow-key-0001", "fixture-cloudflare-credential"
	secret := func(t *testing.T, text string) {
		t.Helper()
		require.NotContains(t, text, indexNowKey)
		require.NotContains(t, text, cloudflareToken)
	}
	call := func(method string, body any, code string) map[string]any {
		t.Helper()
		var raw io.Reader
		if body != nil {
			encoded, err := json.Marshal(body)
			require.NoError(t, err)
			raw = bytes.NewReader(encoded)
		}
		req := httptest.NewRequest(method, "/admin-api/system/seo-settings", raw)
		req.Header.Set("Authorization", "Bearer "+pair.Token)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		app.Engine.ServeHTTP(rec, req)
		require.Equal(t, 200, rec.Code)
		secret(t, rec.Body.String())
		var envelope struct {
			Code string         `json:"code"`
			Data map[string]any `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
		require.Equal(t, code, envelope.Code, rec.Body.String())
		testutil.ValidateResponse(t, adminSpec, "/admin-api", req, rec)
		return envelope.Data
	}
	get := func(path string, status int) (map[string]any, string) {
		t.Helper()
		req := httptest.NewRequest("GET", path, nil)
		rec := httptest.NewRecorder()
		app.Engine.ServeHTTP(rec, req)
		require.Equal(t, status, rec.Code, rec.Body.String())
		testutil.ValidateResponse(t, publicSpec, "/api/v1", req, rec)
		var envelope struct {
			Data map[string]any `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
		return envelope.Data, rec.Header().Get("Cache-Control")
	}

	initial := call("GET", nil, "0000")
	require.Equal(t, false, initial["indexNowKeyConfigured"])
	require.Equal(t, false, initial["cloudflareTokenConfigured"])
	require.EqualValues(t, 1, initial["revision"])
	require.Equal(t, "http://localhost:3000", initial["siteUrl"])
	settings := initial["settings"].(map[string]any)
	require.Equal(t, "OpenNavo", settings["identity"].(map[string]any)["name"])
	require.Equal(t, "limit", settings["crawlers"].(map[string]any)["training"].(map[string]any)["mode"])
	site, cacheControl := get("/api/v1/site/settings", 200)
	require.Equal(t, "public, max-age=60", cacheControl)
	require.NotContains(t, site, "indexNow")
	require.NotContains(t, site, "cloudflare")
	get("/api/v1/site/indexnow-keys/"+indexNowKey, 404)

	// Switches that act on credentials are rejected until the credential exists.
	settings["indexNow"] = map[string]any{"enabled": true}
	call("PUT", map[string]any{"settings": settings}, "1001")
	settings["indexNow"] = map[string]any{"enabled": false}
	settings["cloudflare"] = map[string]any{"zoneId": strings.Repeat("0f", 16), "purgeEnabled": true}
	call("PUT", map[string]any{"settings": settings}, "1001")

	settings["indexNow"] = map[string]any{"enabled": true}
	settings["analytics"] = map[string]any{"ga4Enabled": true, "ga4MeasurementId": "G-FIXTURE1", "consentRegions": []string{"DE", "GB"}}
	saved := call("PUT", map[string]any{"settings": settings, "indexNowKey": indexNowKey, "cloudflareToken": cloudflareToken}, "0000")
	require.Equal(t, true, saved["indexNowKeyConfigured"])
	require.Equal(t, true, saved["cloudflareTokenConfigured"])
	require.EqualValues(t, 2, saved["revision"])
	row, err := st.SEOSettings(ctx)
	require.NoError(t, err)
	secret(t, row.IndexNowKeyCiphertext+row.CloudflareTokenCiphertext)
	stored, err := st.SEOSecret(ctx, cfg, "indexnow")
	require.NoError(t, err)
	require.Equal(t, indexNowKey, stored)
	stored, err = st.SEOSecret(ctx, cfg, "cloudflare")
	require.NoError(t, err)
	require.Equal(t, cloudflareToken, stored)
	site, _ = get("/api/v1/site/settings", 200)
	require.Equal(t, "G-FIXTURE1", site["analytics"].(map[string]any)["ga4MeasurementId"])
	require.EqualValues(t, 2, site["revision"])
	require.NotContains(t, site, "cloudflare")
	verified, cacheControl := get("/api/v1/site/indexnow-keys/"+indexNowKey, 200)
	require.Equal(t, indexNowKey, verified["key"])
	require.Equal(t, "public, max-age=300", cacheControl)
	get("/api/v1/site/indexnow-keys/fixture-indexnow-key-0002", 404)

	// Omitted credentials keep their ciphertexts; clearing a credential still in use is rejected.
	call("PUT", map[string]any{"settings": settings}, "0000")
	unchanged, err := st.SEOSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, row.IndexNowKeyCiphertext, unchanged.IndexNowKeyCiphertext)
	require.Equal(t, row.CloudflareTokenCiphertext, unchanged.CloudflareTokenCiphertext)
	call("PUT", map[string]any{"settings": settings, "clearIndexNowKey": true}, "1001")
	call("PUT", map[string]any{"settings": settings, "clearCloudflareToken": true}, "1001")
	settings["crawlers"].(map[string]any)["training"].(map[string]any)["bots"] = []string{"Googlebot"}
	call("PUT", map[string]any{"settings": settings}, "1001")
	settings["crawlers"].(map[string]any)["training"].(map[string]any)["bots"] = []string{"ClaudeBot", "GPTBot"}

	settings["indexNow"] = map[string]any{"enabled": false}
	settings["cloudflare"] = map[string]any{"zoneId": "", "purgeEnabled": false}
	cleared := call("PUT", map[string]any{"settings": settings, "clearIndexNowKey": true, "clearCloudflareToken": true}, "0000")
	require.Equal(t, false, cleared["indexNowKeyConfigured"])
	require.Equal(t, false, cleared["cloudflareTokenConfigured"])
	require.EqualValues(t, 4, cleared["revision"], "rejected saves must not bump the revision")
	get("/api/v1/site/indexnow-keys/"+indexNowKey, 404)
	site, _ = get("/api/v1/site/settings", 200)
	require.Equal(t, []any{"ClaudeBot", "GPTBot"}, site["crawlers"].(map[string]any)["training"].(map[string]any)["bots"])

	var audit string
	require.NoError(t, st.DB.Raw("SELECT string_agg(coalesce(before::text,'')||coalesce(after::text,''),'') FROM audit_logs WHERE entity_type='seo_settings'").Scan(&audit).Error)
	require.Contains(t, audit, "indexNowKeyConfigured")
	secret(t, audit)
	require.NotContains(t, audit, row.IndexNowKeyCiphertext)
	require.NotContains(t, audit, row.CloudflareTokenCiphertext)
	secret(t, logs.String())
	var roles []string
	require.NoError(t, st.DB.Raw("SELECT r.role_code FROM admin_role_permissions p JOIN admin_roles r ON r.id=p.role_id WHERE permission_code='system:seo:edit'").Scan(&roles).Error)
	require.Equal(t, []string{"R_ADMIN"}, roles)
}
