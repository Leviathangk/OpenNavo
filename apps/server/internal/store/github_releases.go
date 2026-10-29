package store

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type GitHubReleaseAsset struct {
	ReleaseTag    string
	AssetID       int64
	AssetName     string
	DownloadCount int64
}

type githubReleaseSnapshot struct {
	GitHubReleaseAsset `gorm:"embedded"`
	Day                string
	CapturedAt         time.Time
}

// GitHubReleaseCategory counts transfers of the published installers and updater
// archives. Signatures, checksums, source archives and other assets are excluded.
func GitHubReleaseCategory(name string) string {
	switch {
	case strings.HasSuffix(name, "_universal.dmg"):
		return "downloads"
	case strings.HasSuffix(name, ".app.tar.gz"):
		return "updates"
	default:
		return ""
	}
}

// RecordGitHubReleaseSnapshot replaces one complete observation atomically. The
// clock is the start of the API read; a slower, older run cannot overwrite a newer
// one. No historical days are backfilled, and each day retains only its last batch.
func (s *Store) RecordGitHubReleaseSnapshot(ctx context.Context, assets []GitHubReleaseAsset, at time.Time) error {
	if len(assets) == 0 || at.IsZero() {
		return errors.New("empty GitHub release snapshot")
	}
	at = at.UTC().Truncate(time.Microsecond)
	day := at.In(usageZone).Format(time.DateOnly)
	rows := make([]githubReleaseSnapshot, 0, len(assets))
	seen := make(map[int64]bool, len(assets))
	for _, asset := range assets {
		if !strings.HasPrefix(asset.ReleaseTag, "desktop-v") || asset.AssetID <= 0 || asset.AssetName == "" || asset.DownloadCount < 0 || seen[asset.AssetID] {
			return errors.New("invalid GitHub release snapshot")
		}
		seen[asset.AssetID] = true
		rows = append(rows, githubReleaseSnapshot{GitHubReleaseAsset: asset, Day: day, CapturedAt: at})
	}
	return s.WithTx(ctx, func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended('github-release:snapshot',0))").Error; err != nil {
			return err
		}
		var latest struct{ At *time.Time }
		if err := tx.Raw("SELECT max(captured_at) AS at FROM github_release_snapshots").Scan(&latest).Error; err != nil {
			return err
		}
		if latest.At != nil && !at.After(*latest.At) {
			return nil
		}
		if err := tx.Table("github_release_snapshots").Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "day"}, {Name: "asset_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"release_tag", "asset_name", "download_count", "captured_at"}),
		}).CreateInBatches(rows, 500).Error; err != nil {
			return err
		}
		// An asset deleted upstream during this day must not remain in today's
		// complete snapshot, or its count would still enter the cumulative total.
		return tx.Exec("DELETE FROM github_release_snapshots WHERE day=?::date AND captured_at<>?", day, at).Error
	})
}

type GitHubReleaseCounts struct {
	Today     *int64 `json:"today"`
	Yesterday *int64 `json:"yesterday"`
	Total     *int64 `json:"total"`
}

type GitHubReleaseDay struct {
	Date      string `json:"date"`
	Downloads *int64 `json:"downloads"`
	Updates   *int64 `json:"updates"`
}

type GitHubReleaseOverview struct {
	Downloads      GitHubReleaseCounts `json:"downloads"`
	Updates        GitHubReleaseCounts `json:"updates"`
	Days           []GitHubReleaseDay  `json:"days"`
	TimeZone       string              `json:"timeZone"`
	LastSnapshotAt *time.Time          `json:"lastSnapshotAt"`
	DailyStartDate *string             `json:"dailyStartDate"`
}

func (s *Store) GitHubReleaseOverview(ctx context.Context, now time.Time) (GitHubReleaseOverview, error) {
	today := now.In(usageZone).Format(time.DateOnly)
	var rows []githubReleaseSnapshot
	var firstDays []string
	// Both reads see one committed batch even while an hourly snapshot is saved.
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Raw(`SELECT to_char(day,'YYYY-MM-DD') FROM github_release_snapshots
 WHERE day<=?::date GROUP BY day ORDER BY day LIMIT 2`, today).Scan(&firstDays).Error; err != nil {
			return err
		}
		// Include the last snapshot before the chart window as its baseline (or
		// as the cumulative snapshot when the API has been unavailable longer).
		return tx.Raw(`SELECT to_char(day,'YYYY-MM-DD') AS day,release_tag,asset_id,asset_name,download_count,captured_at
 FROM github_release_snapshots WHERE day<=?::date AND (day>=?::date-29 OR day=(
 SELECT max(day) FROM github_release_snapshots WHERE day<?::date-29)) ORDER BY day,asset_id`, today, today, today).Scan(&rows).Error
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	return githubReleaseOverview(rows, firstDays, now), err
}

func githubReleaseOverview(rows []githubReleaseSnapshot, firstDays []string, now time.Time) GitHubReleaseOverview {
	result := GitHubReleaseOverview{TimeZone: "Asia/Shanghai", Days: make([]GitHubReleaseDay, 30)}
	if len(firstDays) > 1 {
		result.DailyStartDate = &firstDays[1]
	}
	byDay := map[string][]githubReleaseSnapshot{}
	for _, row := range rows {
		byDay[row.Day] = append(byDay[row.Day], row)
	}
	dates := make([]string, 0, len(byDay))
	for day := range byDay {
		dates = append(dates, day)
	}
	sort.Strings(dates)
	daily := map[string]GitHubReleaseDay{}
	previous := map[int64]int64{}
	for _, date := range dates {
		counts := map[string]int64{"downloads": 0, "updates": 0}
		totals := map[string]int64{"downloads": 0, "updates": 0}
		next := make(map[int64]int64, len(byDay[date]))
		for _, row := range byDay[date] {
			next[row.AssetID] = row.DownloadCount
			if category := GitHubReleaseCategory(row.AssetName); category != "" {
				counts[category] += max(0, row.DownloadCount-previous[row.AssetID])
				totals[category] += row.DownloadCount
			}
			if result.LastSnapshotAt == nil || row.CapturedAt.After(*result.LastSnapshotAt) {
				at := row.CapturedAt.UTC()
				result.LastSnapshotAt = &at
			}
		}
		// Only a day with an earlier global snapshot day has daily data. A new
		// asset on any later day uses zero as its previous cumulative count.
		if len(previous) > 0 && len(firstDays) > 0 && date != firstDays[0] {
			downloads, updates := counts["downloads"], counts["updates"]
			daily[date] = GitHubReleaseDay{Date: date, Downloads: &downloads, Updates: &updates}
		}
		downloads, updates := totals["downloads"], totals["updates"]
		result.Downloads.Total, result.Updates.Total = &downloads, &updates
		previous = next
	}
	for i := range result.Days {
		date := now.In(usageZone).AddDate(0, 0, i-29).Format(time.DateOnly)
		result.Days[i] = GitHubReleaseDay{Date: date}
		if entry, ok := daily[date]; ok {
			result.Days[i] = entry
		}
	}
	result.Downloads.Today, result.Updates.Today = result.Days[29].Downloads, result.Days[29].Updates
	result.Downloads.Yesterday, result.Updates.Yesterday = result.Days[28].Downloads, result.Days[28].Updates
	return result
}
