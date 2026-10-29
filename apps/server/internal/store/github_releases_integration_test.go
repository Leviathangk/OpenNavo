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

func TestGitHubReleaseDeletedAndReuploadedAsset(t *testing.T) {
	st := testutil.NewStore(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 27, 8, 10, 0, 0, time.UTC)
	assets := []store.GitHubReleaseAsset{
		{ReleaseTag: "desktop-v0.1.3", AssetID: 1, AssetName: "OpenNavo_0.1.3_universal.dmg", DownloadCount: 100},
		{ReleaseTag: "desktop-v0.1.3", AssetID: 2, AssetName: "OpenNavo_aarch64.app.tar.gz", DownloadCount: 40},
	}
	require.NoError(t, st.RecordGitHubReleaseSnapshot(ctx, assets, at))
	assets[0].DownloadCount = 110
	assets[1].DownloadCount = 44
	require.NoError(t, st.RecordGitHubReleaseSnapshot(ctx, assets, at.AddDate(0, 0, 1)))

	// Deletion removes the old asset from current totals without a negative daily count.
	replacement := assets[0]
	assets = assets[1:]
	assets[0].DownloadCount = 46
	at = at.AddDate(0, 0, 2)
	require.NoError(t, st.RecordGitHubReleaseSnapshot(ctx, assets, at))
	deleted, err := st.GitHubReleaseOverview(ctx, at)
	require.NoError(t, err)
	require.EqualValues(t, 0, *deleted.Downloads.Today)
	require.EqualValues(t, 0, *deleted.Downloads.Total)
	require.EqualValues(t, 10, *deleted.Downloads.Yesterday)

	// The re-upload has the same name but a new identity and restarted counter.
	replacement.AssetID, replacement.DownloadCount = 3, 7
	assets = append(assets, replacement)
	require.NoError(t, st.RecordGitHubReleaseSnapshot(ctx, assets, at.Add(time.Hour)))
	reuploaded, err := st.GitHubReleaseOverview(ctx, at)
	require.NoError(t, err)
	require.EqualValues(t, 7, *reuploaded.Downloads.Today, "the new ID uses zero, not the old asset's 110")
	require.EqualValues(t, 7, *reuploaded.Downloads.Total, "totals contain only the replacement's current counter")
	require.EqualValues(t, 10, *reuploaded.Downloads.Yesterday, "deletion/re-upload preserves historical daily data")
	require.EqualValues(t, 2, *reuploaded.Updates.Today)
	require.EqualValues(t, 46, *reuploaded.Updates.Total)
	var oldRows, currentOldRows int64
	require.NoError(t, st.DB.Table("github_release_snapshots").Where("asset_id = ?", 1).Count(&oldRows).Error)
	require.EqualValues(t, 2, oldRows, "the deleted asset's earlier snapshots remain")
	require.NoError(t, st.DB.Table("github_release_snapshots").Where("day = ?::date AND asset_id = ?", "2026-10-29", 1).Count(&currentOldRows).Error)
	require.Zero(t, currentOldRows)

	assets[1].DownloadCount = 11
	require.NoError(t, st.RecordGitHubReleaseSnapshot(ctx, assets, at.AddDate(0, 0, 1)))
	next, err := st.GitHubReleaseOverview(ctx, at.AddDate(0, 0, 1))
	require.NoError(t, err)
	require.EqualValues(t, 4, *next.Downloads.Today, "subsequent days use the replacement ID's previous counter")
	require.EqualValues(t, 7, *next.Downloads.Yesterday)
	require.EqualValues(t, 11, *next.Downloads.Total)
}

func TestGitHubReleaseRenamedAssetUsesCurrentNameAndSameIDBaseline(t *testing.T) {
	st := testutil.NewStore(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 26, 8, 10, 0, 0, time.UTC)
	const updater = "OpenNavo_aarch64.app.tar.gz"
	assets := []store.GitHubReleaseAsset{{ReleaseTag: "desktop-v0.1.3", AssetID: 1, AssetName: updater, DownloadCount: 40}}
	require.NoError(t, st.RecordGitHubReleaseSnapshot(ctx, assets, at))
	assets[0].DownloadCount = 47
	require.NoError(t, st.RecordGitHubReleaseSnapshot(ctx, assets, at.AddDate(0, 0, 1)))
	at = at.AddDate(0, 0, 2)
	assets[0].DownloadCount = 50
	require.NoError(t, st.RecordGitHubReleaseSnapshot(ctx, assets, at))
	// A same-day rename must update the stored name and reclassify the complete day.
	assets[0].AssetName, assets[0].DownloadCount = updater+".sig", 52
	require.NoError(t, st.RecordGitHubReleaseSnapshot(ctx, assets, at.Add(time.Hour)))

	for _, tc := range []struct {
		name, assetName              string
		days                         int
		count, downloads, updates    int64
		downloadsTotal, updatesTotal int64
	}{
		{"excluded signature", updater + ".sig", 0, 52, 0, 0, 0, 0},
		{"renamed back from signature", updater, 1, 55, 0, 3, 0, 55},
		{"renamed into downloads", "OpenNavo_0.1.3_universal.dmg", 2, 58, 3, 0, 58, 0},
		{"renamed within category with reset", "OpenNavo_renamed_universal.dmg", 3, 4, 0, 0, 4, 0},
		{"increase after reset", "OpenNavo_renamed_universal.dmg", 4, 9, 5, 0, 9, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observedAt := at.AddDate(0, 0, tc.days)
			if tc.days > 0 {
				assets[0].AssetName, assets[0].DownloadCount = tc.assetName, tc.count
				require.NoError(t, st.RecordGitHubReleaseSnapshot(ctx, assets, observedAt))
			}
			report, err := st.GitHubReleaseOverview(ctx, observedAt)
			require.NoError(t, err)
			require.Equal(t, &tc.downloads, report.Downloads.Today)
			require.Equal(t, &tc.updates, report.Updates.Today)
			require.Equal(t, &tc.downloadsTotal, report.Downloads.Total)
			require.Equal(t, &tc.updatesTotal, report.Updates.Total)
			for _, day := range report.Days {
				if day.Date == "2026-10-27" {
					require.NotNil(t, day.Updates)
					require.EqualValues(t, 7, *day.Updates, "later renames do not reclassify historical snapshots")
					require.EqualValues(t, 0, *day.Downloads)
				}
			}
		})
	}
}
