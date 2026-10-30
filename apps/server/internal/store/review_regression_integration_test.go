//go:build integration

package store_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/catalog"
	"github.com/opennavo/opennavo/server/internal/service/management"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestRegressionDetailSourceFactInvalidation(t *testing.T) {
	st := testutil.NewStore(t)
	ctx := context.Background()
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: remediationCask(t, "cached")}}))
	row, err := st.CatalogRow(ctx, "cask", "cached")
	require.NoError(t, err)
	require.NoError(t, st.DB.Exec("UPDATE job_outbox SET available_at=clock_timestamp()-interval '1 minute' WHERE job_type='cache:invalidate'").Error)
	require.NoError(t, st.DispatchCacheInvalidations(ctx))
	stop, err := cache.Subscribe(ctx, st.Redis, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	defer stop()
	svc := catalog.PublicService{Store: st, Cache: cache.Redis{Client: st.Redis}}
	_, err = svc.Detail(ctx, "cask", "cached", "en-US")
	require.NoError(t, err)
	const key = cache.PackageDetailPrefix + "cask:cached:en-US"
	require.EqualValues(t, 1, st.Redis.Exists(ctx, key).Val())
	require.NoError(t, st.SaveSourceFact(ctx, store.SourceFact{PackageID: row.ID, Source: "github", Input: "example/cached", Status: "none", CheckedAt: time.Now(), NextCheckAt: time.Now()}, "cached", true))
	require.NoError(t, st.DB.Exec("UPDATE job_outbox SET available_at=clock_timestamp()-interval '1 minute' WHERE job_type='cache:invalidate'").Error)
	require.NoError(t, st.DispatchCacheInvalidations(ctx))
	require.Eventually(t, func() bool { return st.Redis.Exists(ctx, key).Val() == 0 }, 2*time.Second, 10*time.Millisecond, "changed source facts must invalidate the live detail cache")
}
func TestRegressionRemovedLinkDirectAdoptionVisibility(t *testing.T) {
	st := testutil.NewStore(t)
	ctx := context.Background()
	id := remediationLink(t, st, "removed-link")
	require.NoError(t, st.DB.Exec("INSERT INTO package_meta(package_id,notes) VALUES(?,?) ON CONFLICT(package_id) DO UPDATE SET notes=excluded.notes", id, "editor notes").Error)
	require.NoError(t, st.RemoveLinkApp(ctx, id))
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: remediationCask(t, "removed-link")}}))
	row, err := st.CatalogRow(ctx, "cask", "removed-link")
	require.NoError(t, err)
	require.Equal(t, id, row.ID)
	var notes string
	require.NoError(t, st.DB.Raw("SELECT notes FROM package_meta WHERE package_id=?", id).Scan(&notes).Error)
	require.Equal(t, "editor notes", notes)
	var hidden bool
	require.NoError(t, st.DB.Raw("SELECT coalesce(m.hidden,false) FROM packages p LEFT JOIN package_meta m ON m.package_id=p.id WHERE p.id=?", id).Scan(&hidden).Error)
	require.True(t, hidden, "removed link must remain hidden after adoption, as rename adoption already guarantees")
}

func TestLinkAdoptionRecordsVisibilityReason(t *testing.T) {
	st := testutil.NewStore(t)
	ctx := context.Background()
	id := remediationLink(t, st, "adopted-link")
	require.NoError(t, st.RemoveLinkApp(ctx, id))
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: remediationCask(t, "adopted-link")}}))
	var hidden bool
	var reason string
	require.NoError(t, st.DB.Raw("SELECT hidden,visibility_reason FROM package_meta WHERE package_id=?", id).Row().Scan(&hidden, &reason))
	require.True(t, hidden)
	require.NotEmpty(t, strings.TrimSpace(reason), "adoption that hides a package must record a visibility reason")
}

func TestRegressionDetailInvalidationProducers(t *testing.T) {
	st := testutil.NewStore(t)
	ctx := context.Background()
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: remediationCask(t, "detail")}}))
	row, err := st.CatalogRow(ctx, "cask", "detail")
	require.NoError(t, err)
	require.NoError(t, st.DB.Exec("UPDATE job_outbox SET available_at=clock_timestamp()-interval '1 minute' WHERE job_type='cache:invalidate'").Error)
	require.NoError(t, st.DispatchCacheInvalidations(ctx))
	svc := catalog.PublicService{Store: st, Cache: cache.Redis{Client: st.Redis}}
	managementService := management.Service{Store: st}
	changes := []struct {
		name string
		run  func() error
	}{
		{"tap history", func() error {
			_, err := st.RecordTapVersions(ctx, row.ID, []store.TapVersion{{Version: "0.9", SHA: "fixture", CommittedAt: time.Now()}}, time.Now())
			return err
		}},
		{"related articles", func() error {
			return cache.PublishInvalidation(ctx, st.Redis, store.ArticleCachePatterns([]string{"/apps/detail"})...)
		}},
		{"index override", func() error {
			_, err := managementService.Execute(ctx, "SetPackageIndexStatus", management.Input{ID: row.ID, Body: map[string]any{"override": "noindex", "reason": "fixture"}})
			return err
		}},
		{"index policy", func() error {
			settings, err := st.SEOSettings(ctx)
			if err != nil {
				return err
			}
			settings.Settings.Pages.IndexPolicy.Mode = "enforce"
			_, err = managementService.Execute(ctx, "UpdateSeoSettings", management.Input{Body: map[string]any{"settings": settings.Settings}})
			return err
		}},
	}
	for _, change := range changes {
		t.Run(change.name, func(t *testing.T) {
			stop, err := cache.Subscribe(ctx, st.Redis, slog.New(slog.NewTextHandler(io.Discard, nil)))
			require.NoError(t, err)
			defer stop()
			keys := []string{}
			for _, locale := range []string{"en-US", "zh-CN"} {
				for _, links := range []bool{false, true} {
					caps := domain.WithLinks(ctx, links)
					_, err = svc.Detail(caps, "cask", "detail", locale)
					require.NoError(t, err)
					keys = append(keys, cache.PackageDetailPrefix+"cask:detail:"+locale+domain.LinksCacheKey(caps))
				}
			}
			require.EqualValues(t, len(keys), st.Redis.Exists(ctx, keys...).Val())
			require.NoError(t, st.Redis.Set(ctx, "session:regression", "keep", time.Minute).Err())
			require.NoError(t, change.run())
			require.NoError(t, st.DB.Exec("UPDATE job_outbox SET available_at=clock_timestamp()-interval '1 minute' WHERE job_type='cache:invalidate'").Error)
			require.NoError(t, st.DispatchCacheInvalidations(ctx))
			require.Eventually(t, func() bool { return st.Redis.Exists(ctx, keys...).Val() == 0 }, 2*time.Second, 10*time.Millisecond, "producer must invalidate both capabilities and every locale")
			require.Equal(t, "keep", st.Redis.Get(ctx, "session:regression").Val())
		})
	}
}

func TestRegressionRemovedLinkAdoptionWithoutMetadataRollsBack(t *testing.T) {
	st := testutil.NewStore(t)
	ctx := context.Background()
	id := remediationLink(t, st, "no-metadata")
	require.NoError(t, st.RemoveLinkApp(ctx, id))
	require.NoError(t, st.DB.Exec("DELETE FROM package_meta WHERE package_id=?", id).Error)
	require.NoError(t, st.DB.Exec("UPDATE job_outbox SET available_at=clock_timestamp()-interval '1 minute' WHERE job_type='cache:invalidate'").Error)
	require.NoError(t, st.DispatchCacheInvalidations(ctx))
	require.NoError(t, st.DB.Exec("ALTER TABLE job_outbox ADD CONSTRAINT reject_adoption_cache CHECK (job_type<>'cache:invalidate')").Error)
	mutation := []store.CatalogMutation{{Package: remediationCask(t, "no-metadata")}}
	require.Error(t, st.ApplyCatalogBatch(ctx, mutation))
	var unchanged int64
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM packages p JOIN link_apps l ON l.package_id=p.id WHERE p.id=? AND p.kind='link' AND p.removed_at IS NOT NULL AND l.adopted_by_cask_at IS NULL", id).Scan(&unchanged).Error)
	require.EqualValues(t, 1, unchanged)
	var metadata int64
	require.NoError(t, st.DB.Raw("SELECT count(*) FROM package_meta WHERE package_id=?", id).Scan(&metadata).Error)
	require.Zero(t, metadata)
	require.NoError(t, st.DB.Exec("ALTER TABLE job_outbox DROP CONSTRAINT reject_adoption_cache").Error)
	require.NoError(t, st.ApplyCatalogBatch(ctx, mutation))
	var hidden bool
	require.NoError(t, st.DB.Raw("SELECT hidden FROM package_meta WHERE package_id=?", id).Scan(&hidden).Error)
	require.True(t, hidden)
	visible := remediationLink(t, st, "visible")
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: remediationCask(t, "visible")}}))
	require.NoError(t, st.DB.Raw("SELECT coalesce(m.hidden,false) FROM packages p LEFT JOIN package_meta m ON m.package_id=p.id WHERE p.id=?", visible).Scan(&hidden).Error)
	require.False(t, hidden)
}
