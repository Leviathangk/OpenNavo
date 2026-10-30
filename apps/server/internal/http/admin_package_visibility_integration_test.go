//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/gen/adminapi"
	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/config"
	"github.com/opennavo/opennavo/server/internal/domain"
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

func TestAdminVisibilityRequiresReasonAndAuditsIt(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	data, err := seeds.Load()
	require.NoError(t, err)
	hash, err := seed.HashPassword("isolated-test-only")
	require.NoError(t, err)
	require.NoError(t, st.Seed(ctx, data, "visibility-fixture", hash))
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash) VALUES(901,'cask','visibility-sample','visibility-sample','homebrew/cask','Visibility sample','1','1','{}','fixture')`).Error)
	cfg := config.Config{JWTSecret: strings.Repeat("v", 32), JWTAccessTTL: time.Hour, JWTRefreshTTL: time.Hour}
	authentication, err := auth.New(st, cfg)
	require.NoError(t, err)
	pair, err := authentication.Login(ctx, "visibility-fixture", "isolated-test-only", store.AuditActor{})
	require.NoError(t, err)
	manager := &management.Service{Store: st, Config: cfg, InvalidatePublic: func(ctx context.Context) error {
		return cache.Invalidate(ctx, st.Redis, []string{"c:*"})
	}}
	publicService := &catalog.PublicService{Store: st, Cache: cache.Redis{Client: st.Redis}}
	server, err := httpserver.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), st,
		httpserver.Dependencies{Admin: &admin.Handler{Auth: authentication, Management: manager}, Public: &public.Handler{Catalog: publicService}})
	require.NoError(t, err)
	read := func(t *testing.T, status int) {
		t.Helper()
		r := httptest.NewRequest("GET", "/api/v1/packages/cask/visibility-sample", nil)
		w := httptest.NewRecorder()
		server.Engine.ServeHTTP(w, r)
		require.Equal(t, status, w.Code, w.Body.String())
	}
	spec, err := adminapi.GetSpec()
	require.NoError(t, err)
	patch := func(body, code string) {
		t.Helper()
		r := httptest.NewRequest("PUT", "/admin-api/packages/901/meta", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+pair.Token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		server.Engine.ServeHTTP(w, r)
		require.Equal(t, 200, w.Code)
		testutil.ValidateResponse(t, spec, "/admin-api", r, w)
		var result struct{ Code string }
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
		require.Equal(t, code, result.Code, w.Body.String())
	}
	for _, field := range []string{"hidden", "private"} {
		t.Run(field, func(t *testing.T) {
			read(t, 200) // Warm the real public detail cache before restricting the package.
			patch(fmt.Sprintf(`{"%s":true}`, field), domain.CodeValidation)
			patch(fmt.Sprintf(`{"%s":true,"visibilityReason":" \t "}`, field), domain.CodeValidation)
			patch(fmt.Sprintf(`{"%s":true,"visibilityReason":"  Catalog visibility review.  "}`, field), domain.CodeOK)
			status := 200
			if field == "private" {
				status = 404
			}
			read(t, status)
			var reason string
			require.NoError(t, st.DB.Raw("SELECT visibility_reason FROM package_meta WHERE package_id=901").Scan(&reason).Error)
			require.Equal(t, "Catalog visibility review.", reason)
			var after string
			require.NoError(t, st.DB.Raw(`SELECT after::text FROM audit_logs WHERE action='UpdatePackageMeta' ORDER BY id DESC LIMIT 1`).Scan(&after).Error)
			require.JSONEq(t, fmt.Sprintf(`{"%s":true,"visibilityReason":"Catalog visibility review."}`, field), after)
			patch(fmt.Sprintf(`{"%s":false}`, field), domain.CodeOK)
			read(t, 200)
		})
	}
}
