//go:build integration

package maintenance_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"net/http/httptest"

	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/observability"
	"github.com/opennavo/opennavo/server/internal/service/maintenance"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/stretchr/testify/require"
)

type deletions struct {
	keys []string
	fail bool
}

func (d *deletions) Delete(_ context.Context, key string) error {
	if d.fail {
		return errors.New("object unavailable")
	}
	d.keys = append(d.keys, key)
	return nil
}
func TestCleanupReferencesVariantsRetentionAndMetrics(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	now := time.Now().UTC()
	p, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"sample","version":"1"}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p}}))
	row, err := st.PublicPackage(ctx, "cask", "sample", "zh-CN")
	require.NoError(t, err)
	asset := func(name, kind string, old bool) int64 {
		t.Helper()
		a, err := st.SaveAsset(ctx, store.Asset{Kind: kind, StorageKey: "icons/" + name + "-256.png", URL: "https://cdn.example.test/" + name, MIME: "image/png", Width: 256, Height: 256, Bytes: 42, SHA256: strings.Repeat("a", 64)}, nil)
		require.NoError(t, err)
		if old {
			require.NoError(t, st.DB.WithContext(ctx).Exec("UPDATE assets SET created_at=? WHERE id=?", now.AddDate(0, 0, -31), a.ID).Error)
		}
		return a.ID
	}
	unused := asset("unused", "icon", true)
	icon := asset("referenced", "icon", true)
	screenshot := asset("screenshot", "screenshot", true)
	cover := asset("cover", "cover", true)
	asset("new", "icon", false)
	require.NoError(t, st.DB.WithContext(ctx).Exec("INSERT INTO package_meta(package_id,icon_asset_id) VALUES(?,?)", row.ID, icon).Error)
	require.NoError(t, st.DB.WithContext(ctx).Exec("INSERT INTO package_screenshots(package_id,asset_id) VALUES(?,?)", row.ID, screenshot).Error)
	require.NoError(t, st.DB.WithContext(ctx).Exec("INSERT INTO collections(slug,cover_asset_id) VALUES('sample',?)", cover).Error)
	require.NoError(t, st.DB.WithContext(ctx).Exec("UPDATE catalog_changes SET created_at=?", now.AddDate(0, 0, -15)).Error)
	require.NoError(t, st.DB.WithContext(ctx).Exec("INSERT INTO search_queries(query,normalized,locale,platform,result_count,created_at) VALUES('sample','sample','zh-CN','web',1,?)", now.AddDate(0, 0, -91)).Error)
	id, err := st.StartJobRun(ctx, "catalog:sync", "schedule", nil)
	require.NoError(t, err)
	require.NoError(t, st.FinishJobRun(ctx, id, "succeeded", map[string]any{}, nil))
	require.NoError(t, st.FinishJobRun(ctx, id, "succeeded", map[string]any{}, nil))
	require.NoError(t, st.DB.WithContext(ctx).Exec("UPDATE sync_runs SET started_at=? WHERE id=?", now.AddDate(0, 0, -91), id).Error)
	active, err := st.StartJobRun(ctx, "cleanup", "event", nil)
	require.NoError(t, err)
	require.NoError(t, st.DB.WithContext(ctx).Exec("UPDATE sync_runs SET started_at=? WHERE id=?", now.AddDate(0, 0, -91), active).Error)
	require.NoError(t, st.DB.Exec("INSERT INTO translation_metrics(entity,status,requests,tokens) VALUES('package','succeeded',1,150),('package','failed',1,0)").Error)
	st.ObserveGitHubQuota(ctx, 321)
	d := &deletions{}
	svc := &maintenance.Service{Store: st, Objects: d, Now: func() time.Time { return now }}
	stats, err := svc.Run(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), stats["assets"])
	require.Equal(t, int64(1), stats["syncRuns"])
	require.ElementsMatch(t, []string{"icons/unused-256.png", "icons/unused-512.png"}, d.keys)
	var count int64
	require.NoError(t, st.DB.WithContext(ctx).Table("assets").Count(&count).Error)
	require.Equal(t, int64(4), count)
	require.NoError(t, st.DB.WithContext(ctx).Table("assets").Where("id=?", unused).Count(&count).Error)
	require.Zero(t, count)
	low, _, err := st.CatalogBounds(ctx)
	require.NoError(t, err)
	require.Greater(t, low, int64(1))
	state, err := st.MetricState(ctx, now)
	require.NoError(t, err)
	require.Len(t, state.Jobs, 1)
	require.Equal(t, int64(1), state.Jobs[0].Runs)
	registry := prometheus.NewRegistry()
	registry.MustRegister(observability.NewBusinessMetrics(st))
	w := httptest.NewRecorder()
	promhttp.HandlerFor(registry, promhttp.HandlerOpts{}).ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	for _, name := range []string{"sync_runs_total", "catalog_packages", "changelog_fetch_total", "github_ratelimit_remaining 321", "content_translation_requests_total{entity=\"package\",status=\"failed\"} 1", "content_translation_tokens_total{entity=\"package\",status=\"succeeded\"} 150", "opennavo_metrics_collection_success 1"} {
		require.Contains(t, w.Body.String(), name)
	}
	expired := asset("retry", "icon", true)
	d.fail = true
	_, err = svc.Run(ctx)
	require.Error(t, err)
	require.NoError(t, st.DB.WithContext(ctx).Table("assets").Where("id=?", expired).Count(&count).Error)
	require.Equal(t, int64(1), count)
	d.fail = false
	_, err = svc.Run(ctx)
	require.NoError(t, err)
}
