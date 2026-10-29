//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
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

func TestUsageIntradayDashboardContract(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	password, err := seed.HashPassword("isolated-dashboard-test-only")
	require.NoError(t, err)
	data, err := seeds.Load()
	require.NoError(t, err)
	require.NoError(t, st.Seed(ctx, data, "intraday-tester", password))
	cfg := config.Config{JWTSecret: strings.Repeat("t", 32), JWTAccessTTL: time.Hour, JWTRefreshTTL: 24 * time.Hour}
	authentication, err := auth.New(st, cfg)
	require.NoError(t, err)
	pair, err := authentication.Login(ctx, "intraday-tester", "isolated-dashboard-test-only", store.AuditActor{})
	require.NoError(t, err)
	now := time.Date(2026, 10, 28, 17, 30, 0, 0, time.UTC) // Oct 29 01:30 Beijing.
	for _, platform := range []string{"web", "desktop"} {
		activity := store.UsageActivity{AnonymousID: "00000000-0000-4000-8000-000000000001", Platform: platform, Version: "1.0.0", Locale: "en-US", OccurredAt: now.Add(-time.Hour)}
		require.NoError(t, st.RecordUsage(ctx, activity, now))
	}
	svc := &management.Service{Store: st, Config: cfg, Now: func() time.Time { return now }}
	server, err := httpserver.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), st, httpserver.Dependencies{Admin: &admin.Handler{Auth: authentication, Management: svc}})
	require.NoError(t, err)
	spec, err := adminapi.GetSpec()
	require.NoError(t, err)
	call := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("GET", "/admin-api"+path, nil)
		r.Header.Set("Authorization", "Bearer "+pair.Token)
		w := httptest.NewRecorder()
		server.Engine.ServeHTTP(w, r)
		require.Equal(t, 200, w.Code)
		testutil.ValidateResponse(t, spec, "/admin-api", r, w)
		return w
	}
	var intraday struct {
		Code string
		Data store.UsageIntraday
	}
	require.NoError(t, json.Unmarshal(call("/dashboard/usage/intraday").Body.Bytes(), &intraday))
	require.Equal(t, "0000", intraday.Code)
	require.Equal(t, "2026-10-29", intraday.Data.Date)
	require.Len(t, intraday.Data.Hours, 24)
	require.EqualValues(t, 1, *intraday.Data.Hours[0].Desktop)
	require.Zero(t, *intraday.Data.Hours[1].Desktop)
	require.Nil(t, intraday.Data.Hours[2].Desktop)
	var overview struct {
		Code string
		Data struct{ Usage store.UsageOverview }
	}
	require.NoError(t, json.Unmarshal(call("/dashboard/overview").Body.Bytes(), &overview))
	require.Equal(t, "0000", overview.Code)
	today := overview.Data.Usage.Days[len(overview.Data.Usage.Days)-1]
	require.Equal(t, today.Desktop, *intraday.Data.Hours[1].CumulativeDesktop)
	require.Equal(t, today.Web, *intraday.Data.Hours[1].CumulativeWeb)
}
