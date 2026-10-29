package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGitHubReleaseAssetClassification(t *testing.T) {
	for name, want := range map[string]string{
		"OpenNavo_0.1.3_universal.dmg":             "downloads",
		"OpenNavo_aarch64.app.tar.gz":              "updates",
		"OpenNavo_x64.app.tar.gz":                  "updates",
		"OpenNavo_aarch64.app.tar.gz.sig":          "",
		"OpenNavo_x64.app.tar.gz.sig":              "",
		"OpenNavo_0.1.3_universal.dmg.sig":         "",
		"SHA256SUMS":                               "",
		"clamav-1.5.4-corresponding-source.tar.gz": "",
		"OpenNavo_0.1.3_x64.dmg":                   "",
		"latest.json":                              "",
		"source.tar.gz":                            "",
	} {
		t.Run(name, func(t *testing.T) { require.Equal(t, want, GitHubReleaseCategory(name)) })
	}
}

func releaseSnapshot(date string, id, count int64, name string) githubReleaseSnapshot {
	at, _ := time.Parse(time.RFC3339, date+"T08:10:00Z")
	return githubReleaseSnapshot{GitHubReleaseAsset: GitHubReleaseAsset{ReleaseTag: "desktop-v0.1.3", AssetID: id, AssetName: name, DownloadCount: count}, Day: date, CapturedAt: at}
}

func TestGitHubReleaseDailyRules(t *testing.T) {
	now := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	rows := []githubReleaseSnapshot{
		releaseSnapshot("2026-10-27", 1, 100, "OpenNavo_0.1.3_universal.dmg"),
		releaseSnapshot("2026-10-27", 2, 40, "OpenNavo_aarch64.app.tar.gz"),
		releaseSnapshot("2026-10-27", 3, 20, "OpenNavo_x64.app.tar.gz"),
	}
	first := githubReleaseOverview(rows, []string{"2026-10-27"}, now.AddDate(0, 0, -2))
	require.EqualValues(t, 100, *first.Downloads.Total)
	require.EqualValues(t, 60, *first.Updates.Total)
	require.Nil(t, first.Downloads.Today, "the initial cumulative count is not a daily count")
	require.Nil(t, first.DailyStartDate)
	require.Len(t, first.Days, 30)
	rows = append(rows,
		releaseSnapshot("2026-10-29", 1, 112, "OpenNavo_0.1.3_universal.dmg"),
		releaseSnapshot("2026-10-29", 2, 35, "OpenNavo_aarch64.app.tar.gz"), // negative per-asset diff clamps to zero
		releaseSnapshot("2026-10-29", 3, 27, "OpenNavo_x64.app.tar.gz"),
		releaseSnapshot("2026-10-29", 4, 50, "OpenNavo_0.1.4_universal.dmg"), // new asset counts in full
		releaseSnapshot("2026-10-29", 5, 900, "OpenNavo_x64.app.tar.gz.sig"),
		releaseSnapshot("2026-10-29", 6, 900, "clamav-1.5.4-corresponding-source.tar.gz"),
	)
	report := githubReleaseOverview(rows, []string{"2026-10-27", "2026-10-29"}, now)
	require.EqualValues(t, 62, *report.Downloads.Today, "the missed day's increase belongs to the next snapshot day")
	require.EqualValues(t, 7, *report.Updates.Today, "clamp each asset before summing")
	require.Nil(t, report.Downloads.Yesterday, "a missing snapshot is not zero transfers")
	require.EqualValues(t, 162, *report.Downloads.Total)
	require.EqualValues(t, 62, *report.Updates.Total)
	require.Equal(t, "2026-10-29", *report.DailyStartDate)
	require.Nil(t, report.Days[27].Downloads)
	require.Nil(t, report.Days[28].Downloads)
	// A further same-day snapshot on the very first day is still cumulative only.
	initial := githubReleaseOverview(rows[3:], []string{"2026-10-29"}, now)
	require.Nil(t, initial.Downloads.Today)
	require.Nil(t, initial.Updates.Today)
}

func TestGitHubReleaseAssetReplacementAndRename(t *testing.T) {
	const dmg = "OpenNavo_0.1.3_universal.dmg"
	const updater = "OpenNavo_aarch64.app.tar.gz"
	for _, tc := range []struct {
		name                         string
		previousName, currentName    string
		currentID, currentCount      int64
		downloads, updates           int64
		downloadsTotal, updatesTotal int64
	}{
		{"re-upload with same name and new ID", dmg, dmg, 2, 7, 7, 0, 7, 0},
		{"rename within category", dmg, "OpenNavo_renamed_universal.dmg", 1, 107, 7, 0, 107, 0},
		{"rename into another category", dmg, updater, 1, 107, 0, 7, 0, 107},
		{"rename into excluded signature", updater, updater + ".sig", 1, 107, 0, 0, 0, 0},
		{"rename from excluded signature", updater + ".sig", updater, 1, 107, 0, 7, 0, 107},
		{"rename with unchanged counter", dmg, updater, 1, 100, 0, 0, 0, 100},
		{"rename with reset counter", dmg, updater, 1, 7, 0, 0, 0, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := []githubReleaseSnapshot{
				releaseSnapshot("2026-10-28", 1, 100, tc.previousName),
				releaseSnapshot("2026-10-29", tc.currentID, tc.currentCount, tc.currentName),
			}
			report := githubReleaseOverview(rows, []string{"2026-10-28", "2026-10-29"}, time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC))
			require.Equal(t, &tc.downloads, report.Downloads.Today)
			require.Equal(t, &tc.updates, report.Updates.Today)
			require.Equal(t, &tc.downloadsTotal, report.Downloads.Total)
			require.Equal(t, &tc.updatesTotal, report.Updates.Total)
		})
	}
}

func TestGitHubReleaseOverviewBeijingBoundaryAndNoSnapshots(t *testing.T) {
	before := time.Date(2026, 10, 29, 15, 59, 59, 0, time.UTC)
	rows := []githubReleaseSnapshot{
		releaseSnapshot("2026-10-28", 1, 10, "OpenNavo_0.1.3_universal.dmg"),
		releaseSnapshot("2026-10-29", 1, 12, "OpenNavo_0.1.3_universal.dmg"),
	}
	firstDays := []string{"2026-10-28", "2026-10-29"}
	got := githubReleaseOverview(rows, firstDays, before)
	require.Equal(t, "2026-10-29", got.Days[29].Date)
	require.EqualValues(t, 2, *got.Downloads.Today)
	after := githubReleaseOverview(rows, firstDays, before.Add(time.Second))
	require.Equal(t, "2026-10-30", after.Days[29].Date)
	require.Nil(t, after.Downloads.Today)
	require.EqualValues(t, 2, *after.Downloads.Yesterday)
	require.Equal(t, got.LastSnapshotAt, after.LastSnapshotAt)
	empty := githubReleaseOverview(nil, nil, before)
	require.Nil(t, empty.Downloads.Total)
	require.Nil(t, empty.LastSnapshotAt)
	require.Nil(t, empty.DailyStartDate)
	require.Len(t, empty.Days, 30)
}
