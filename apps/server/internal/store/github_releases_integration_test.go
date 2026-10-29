//go:build integration

package store_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestGitHubReleaseSnapshotsAtomicUpsertsAndDailyOverview(t *testing.T) {
	st := testutil.NewStore(t)
	ctx := context.Background()
	before := time.Date(2026, 10, 29, 15, 59, 59, 0, time.UTC)
	assets := []store.GitHubReleaseAsset{
		{ReleaseTag: "desktop-v0.1.3", AssetID: 1, AssetName: "OpenNavo_0.1.3_universal.dmg", DownloadCount: 100},
		{ReleaseTag: "desktop-v0.1.3", AssetID: 2, AssetName: "OpenNavo_aarch64.app.tar.gz", DownloadCount: 40},
		{ReleaseTag: "desktop-v0.1.3", AssetID: 3, AssetName: "OpenNavo_x64.app.tar.gz", DownloadCount: 20},
		{ReleaseTag: "desktop-v0.1.3", AssetID: 4, AssetName: "SHA256SUMS", DownloadCount: 1000},
	}
	empty, err := st.GitHubReleaseOverview(ctx, before)
	require.NoError(t, err)
	require.Nil(t, empty.Downloads.Total)
	require.NoError(t, st.RecordGitHubReleaseSnapshot(ctx, assets, before.Add(-time.Hour)))
	assets[0].DownloadCount = 110
	require.NoError(t, st.RecordGitHubReleaseSnapshot(ctx, assets, before))
	first, err := st.GitHubReleaseOverview(ctx, before)
	require.NoError(t, err)
	require.EqualValues(t, 110, *first.Downloads.Total)
	require.EqualValues(t, 60, *first.Updates.Total)
	require.Nil(t, first.Downloads.Today)
	require.Nil(t, first.DailyStartDate)
	var count int64
	require.NoError(t, st.DB.Table("github_release_snapshots").Count(&count).Error)
	require.EqualValues(t, 4, count, "same-day rows were upserted, including excluded assets")
	// Crossing UTC 16:00 creates a new Beijing natural day.
	after := before.Add(time.Second)
	assets[0].DownloadCount = 115
	assets[1].DownloadCount = 35
	assets[2].DownloadCount = 23
	assets = append(assets, store.GitHubReleaseAsset{ReleaseTag: "desktop-v0.1.4", AssetID: 5, AssetName: "OpenNavo_0.1.4_universal.dmg", DownloadCount: 30})
	require.NoError(t, st.RecordGitHubReleaseSnapshot(ctx, assets, after))
	second, err := st.GitHubReleaseOverview(ctx, after)
	require.NoError(t, err)
	require.EqualValues(t, 35, *second.Downloads.Today)
	require.EqualValues(t, 3, *second.Updates.Today)
	require.Nil(t, second.Downloads.Yesterday)
	require.Equal(t, "2026-10-30", *second.DailyStartDate)
	require.Equal(t, after, *second.LastSnapshotAt)
	// Concurrent retries can only move the latest complete snapshot forward.
	var wg sync.WaitGroup
	errors := make(chan error, 6)
	for n := range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errors <- st.RecordGitHubReleaseSnapshot(ctx, assets, after.Add(time.Duration(n)*time.Minute))
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	// A failed/invalid batch must leave the latest timestamp and counters intact.
	latest, err := st.GitHubReleaseOverview(ctx, after)
	require.NoError(t, err)
	require.Error(t, st.RecordGitHubReleaseSnapshot(ctx, append(assets, assets[0]), after.Add(time.Hour)))
	require.Error(t, st.RecordGitHubReleaseSnapshot(ctx, nil, after.Add(time.Hour)))
	unchanged, err := st.GitHubReleaseOverview(ctx, after)
	require.NoError(t, err)
	require.Equal(t, latest, unchanged)
	// Deleted assets leave the complete same-day snapshot, without deleting history.
	assets = assets[1:]
	require.NoError(t, st.RecordGitHubReleaseSnapshot(ctx, assets, after.Add(time.Hour)))
	deleted, err := st.GitHubReleaseOverview(ctx, after)
	require.NoError(t, err)
	require.EqualValues(t, 30, *deleted.Downloads.Total)
	// Miss a full day: the increase is merged into the next snapshot day.
	assets[0].DownloadCount = 39
	require.NoError(t, st.RecordGitHubReleaseSnapshot(ctx, assets, after.AddDate(0, 0, 2)))
	missed, err := st.GitHubReleaseOverview(ctx, after.AddDate(0, 0, 2))
	require.NoError(t, err)
	require.Nil(t, missed.Updates.Yesterday)
	require.EqualValues(t, 4, *missed.Updates.Today)
	stale, err := st.GitHubReleaseOverview(ctx, after.AddDate(0, 0, 60))
	require.NoError(t, err)
	require.Equal(t, missed.LastSnapshotAt, stale.LastSnapshotAt)
	require.Equal(t, missed.Downloads.Total, stale.Downloads.Total)
	require.Equal(t, missed.DailyStartDate, stale.DailyStartDate)
	for _, day := range stale.Days {
		require.Nil(t, day.Downloads)
		require.Nil(t, day.Updates)
	}
}
