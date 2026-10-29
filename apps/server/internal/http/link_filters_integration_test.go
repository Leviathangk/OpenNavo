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

func TestAdminLinkReleaseFilterContract(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	data, err := seeds.Load()
	require.NoError(t, err)
	hash, err := seed.HashPassword("fixture-admin-password")
	require.NoError(t, err)
	require.NoError(t, st.Seed(ctx, data, "fixture-admin", hash))
	cfg := config.Config{JWTSecret: strings.Repeat("a", 32), JWTAccessTTL: time.Hour, JWTRefreshTTL: 24 * time.Hour}
	authentication, err := auth.New(st, cfg)
	require.NoError(t, err)
	pair, err := authentication.Login(ctx, "fixture-admin", "fixture-admin-password", store.AuditActor{})
	require.NoError(t, err)
	server, err := httpserver.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), st, httpserver.Dependencies{Admin: &admin.Handler{Auth: authentication, Management: &management.Service{Store: st}}})
	require.NoError(t, err)
	spec, err := adminapi.GetSpec()
	require.NoError(t, err)
	call := func(method, path string, body any, code string) json.RawMessage {
		t.Helper()
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		r := httptest.NewRequest(method, "/admin-api"+path, strings.NewReader(string(raw)))
		r.Header.Set("Authorization", "Bearer "+pair.Token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		server.Engine.ServeHTTP(w, r)
		require.Equal(t, 200, w.Code, w.Body.String())
		testutil.ValidateResponse(t, spec, "/admin-api", r, w)
		var envelope struct {
			Code string
			Data json.RawMessage
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
		require.Equal(t, code, envelope.Code, w.Body.String())
		return envelope.Data
	}
	input := map[string]any{"token": "http-desktop", "name": "HTTP Desktop", "githubRepo": "owner/app", "pricing": "free", "githubTagPrefix": "rapid-mac-v", "githubAssetPattern": "*.dmg", "includePrereleases": true}
	preview := call("POST", "/links/packages?dryRun=true", input, "0000")
	require.Contains(t, string(preview), `"githubTagPrefix":"rapid-mac-v"`)
	var count int64
	require.NoError(t, st.DB.Table("link_apps").Count(&count).Error)
	require.Zero(t, count)
	var created struct {
		ID                 int64
		GithubTagPrefix    string
		IncludePrereleases bool
	}
	require.NoError(t, json.Unmarshal(call("POST", "/links/packages", input, "0000"), &created))
	require.Equal(t, "rapid-mac-v", created.GithubTagPrefix)
	require.True(t, created.IncludePrereleases)
	path := "/links/packages/" + strconv.FormatInt(created.ID, 10)
	call("PATCH", path+"?dryRun=true", map[string]any{"githubTagPrefix": nil, "githubTagRegex": "^app-", "includePrereleases": false}, "0000")
	row, err := st.LinkApp(ctx, created.ID)
	require.NoError(t, err)
	require.Equal(t, "rapid-mac-v", *row.GithubTagPrefix)
	call("PATCH", path, map[string]any{"githubTagPrefix": nil, "githubTagRegex": "^app-", "includePrereleases": false}, "0000")
	row, err = st.LinkApp(ctx, created.ID)
	require.NoError(t, err)
	require.Nil(t, row.GithubTagPrefix)
	require.Equal(t, "^app-", *row.GithubTagRegex)
	require.Equal(t, "*.dmg", *row.GithubAssetPattern)
	require.False(t, row.IncludePrereleases)
	for _, body := range []map[string]any{
		{"githubTagRegex": "(?=app)"}, {"githubAssetPattern": "["},
		{"githubTagPrefix": strings.Repeat("a", 257)}, {"githubTagRegex": strings.Repeat("a", 1025)},
		{"githubAssetPattern": strings.Repeat("a", 513)}, {"includePrereleases": nil},
		{"githubTagPrefix": "app-"}, {"githubRepo": nil},
	} {
		call("PATCH", path+"?dryRun=true", body, "1001")
	}
	call("GET", "/links/packages", nil, "0000")
}
