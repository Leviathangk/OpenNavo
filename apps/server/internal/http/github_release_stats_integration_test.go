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

	"github.com/hibiken/asynq"
	"github.com/opennavo/opennavo/server/gen/adminapi"
	"github.com/opennavo/opennavo/server/internal/config"
	httpserver "github.com/opennavo/opennavo/server/internal/http"
	"github.com/opennavo/opennavo/server/internal/http/admin"
	"github.com/opennavo/opennavo/server/internal/jobs"
	"github.com/opennavo/opennavo/server/internal/service/auth"
	"github.com/opennavo/opennavo/server/internal/service/management"
	"github.com/opennavo/opennavo/server/internal/service/seed"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/opennavo/opennavo/server/seeds"
	"github.com/stretchr/testify/require"
)

func TestDashboardGitHubReleaseStatsContract(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	data, err := seeds.Load()
	require.NoError(t, err)
	hash, err := seed.HashPassword("isolated-test-password")
	require.NoError(t, err)
	require.NoError(t, st.Seed(ctx, data, "release_tester", hash))
	cfg := config.Config{JWTSecret: strings.Repeat("a", 32), JWTAccessTTL: time.Hour, JWTRefreshTTL: 24 * time.Hour}
	authentication, err := auth.New(st, cfg)
	require.NoError(t, err)
	pair, err := authentication.Login(ctx, "release_tester", "isolated-test-password", store.AuditActor{})
	require.NoError(t, err)
	now := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	svc := &management.Service{Store: st, Config: cfg, Now: func() time.Time { return now }}
	app, err := httpserver.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), st, httpserver.Dependencies{Admin: &admin.Handler{Auth: authentication, Management: svc}})
	require.NoError(t, err)
	spec, err := adminapi.GetSpec()
	require.NoError(t, err)
	read := func() store.GitHubReleaseOverview {
		t.Helper()
		req := httptest.NewRequest("GET", "/admin-api/dashboard/overview", nil)
		req.Header.Set("Authorization", "Bearer "+pair.Token)
		rec := httptest.NewRecorder()
		app.Engine.ServeHTTP(rec, req)
		require.Equal(t, 200, rec.Code)
		testutil.ValidateResponse(t, spec, "/admin-api", req, rec)
		var envelope struct {
			Code string
			Data struct{ GitHubReleases store.GitHubReleaseOverview }
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
		require.Equal(t, "0000", envelope.Code)
		return envelope.Data.GitHubReleases
	}
	empty := read()
	require.Nil(t, empty.LastSnapshotAt)
	require.Nil(t, empty.Downloads.Total)
	require.Len(t, empty.Days, 30)
	assets := []store.GitHubReleaseAsset{
		{ReleaseTag: "desktop-v0.1.3", AssetID: 1, AssetName: "OpenNavo_0.1.3_universal.dmg", DownloadCount: 953},
		{ReleaseTag: "desktop-v0.1.3", AssetID: 2, AssetName: "OpenNavo_aarch64.app.tar.gz", DownloadCount: 582},
	}
	require.NoError(t, st.RecordGitHubReleaseSnapshot(ctx, assets, now.AddDate(0, 0, -1)))
	first := read()
	require.EqualValues(t, 953, *first.Downloads.Total)
	require.Nil(t, first.Downloads.Yesterday)
	assets[0].DownloadCount += 10
	assets[1].DownloadCount += 5
	require.NoError(t, st.RecordGitHubReleaseSnapshot(ctx, assets, now))
	second := read()
	require.EqualValues(t, 10, *second.Downloads.Today)
	require.EqualValues(t, 5, *second.Updates.Today)
	require.EqualValues(t, 963, *second.Downloads.Total)
	require.Equal(t, "2026-10-29", *second.DailyStartDate)
	require.Equal(t, now, *second.LastSnapshotAt)

	// The scheduled type must also remain inspectable and manually triggerable
	// through the job center's enum contract and transactional outbox.
	svc.Queue = &jobs.Enqueuer{Client: asynq.NewClientFromRedisClient(st.Redis), Redis: st.Redis}
	req := httptest.NewRequest("POST", "/admin-api/jobs/trigger/github-release_snapshot", nil)
	req.Header.Set("Authorization", "Bearer "+pair.Token)
	rec := httptest.NewRecorder()
	app.Engine.ServeHTTP(rec, req)
	require.Equal(t, 200, rec.Code)
	testutil.ValidateResponse(t, spec, "/admin-api", req, rec)
	require.Contains(t, rec.Body.String(), `"code":"0000"`)
	pending, err := asynq.NewInspectorFromRedisClient(st.Redis).ListPendingTasks("default")
	require.NoError(t, err)
	var types []string
	for _, task := range pending {
		types = append(types, task.Type)
	}
	require.Contains(t, types, "github-release:snapshot")
	runID, err := st.StartJobRun(ctx, "github-release:snapshot", "schedule", nil)
	require.NoError(t, err)
	require.NoError(t, st.FinishJobRun(ctx, runID, "succeeded", map[string]any{"assets": 2}, nil))
	req = httptest.NewRequest("GET", "/admin-api/jobs/runs?jobType=github-release_snapshot", nil)
	req.Header.Set("Authorization", "Bearer "+pair.Token)
	rec = httptest.NewRecorder()
	app.Engine.ServeHTTP(rec, req)
	require.Equal(t, 200, rec.Code)
	testutil.ValidateResponse(t, spec, "/admin-api", req, rec)
	require.Contains(t, rec.Body.String(), `"jobType":"github-release_snapshot"`)
}
