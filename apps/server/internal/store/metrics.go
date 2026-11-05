package store

import (
	"context"
	"time"
)

type JobMetric struct {
	JobType, Status, Source string
	Runs                    int64
	LastFinishedAt          time.Time
}
type PackageMetric struct {
	Kind  string
	Count int64
}
type TranslationMetric struct {
	Entity, Status   string
	Requests, Tokens int64
}
type MetricState struct {
	Translations    []TranslationMetric
	Jobs            []JobMetric
	Packages        []PackageMetric
	GitHubRemaining int64
	// ChangelogDue is how many changelog sources were still due after the last schedule tick, -1 when no
	// recent tick reported (a stopped scheduler must not look like a finished backlog).
	ChangelogDue int64
	// SnapshotRevocationGap is revocation_generation - snapshot_revocation.
	SnapshotRevocationGap int64
	// OldestPurgeDebtSeconds is the age of the oldest queued snapshot CDN purge, 0 when none is queued.
	OldestPurgeDebtSeconds float64
	// SnapshotUnavailableSeconds is how long Latest() has returned 503, 0 when it is healthy.
	SnapshotUnavailableSeconds float64
}

func (s *Store) MetricState(ctx context.Context, now time.Time) (MetricState, error) {
	out := MetricState{GitHubRemaining: -1, ChangelogDue: -1}
	if err := s.DB.WithContext(ctx).Table("translation_metrics").Find(&out.Translations).Error; err != nil {
		return out, err
	}
	if err := s.DB.WithContext(ctx).Raw("SELECT * FROM job_metrics").Scan(&out.Jobs).Error; err != nil {
		return out, err
	}
	if err := s.DB.WithContext(ctx).Raw("SELECT kind,count(*) AS count FROM packages WHERE removed_at IS NULL GROUP BY kind").Scan(&out.Packages).Error; err != nil {
		return out, err
	}
	if err := s.DB.WithContext(ctx).Raw("SELECT revocation_generation-snapshot_revocation FROM catalog_visibility WHERE id=true").Scan(&out.SnapshotRevocationGap).Error; err != nil {
		return out, err
	}
	if err := s.DB.WithContext(ctx).Raw(`SELECT COALESCE(EXTRACT(EPOCH FROM (?-pending_snapshot_purges_since)),0)
FROM url_notify_state WHERE id=true AND pending_snapshot_purges_since IS NOT NULL`, now).Scan(&out.OldestPurgeDebtSeconds).Error; err != nil {
		return out, err
	}
	if s.Redis != nil {
		if value, err := s.Redis.Get(ctx, "metrics:github:remaining").Int64(); err == nil {
			out.GitHubRemaining = value
		}
		if since, err := s.Redis.Get(ctx, "metrics:snapshot:unavailable_since").Int64(); err == nil {
			out.SnapshotUnavailableSeconds = max(0, float64(now.Unix()-since))
		}
		if due, err := s.Redis.Get(ctx, "metrics:changelog:due").Int64(); err == nil {
			out.ChangelogDue = due
		}
	}
	return out, nil
}

// ObserveChangelogBacklog records the sources still due after a schedule tick. The TTL covers three
// schedule periods, so a scheduler that stops reports nothing (unknown) instead of freezing its last
// backlog as if the schedule were still running.
func (s *Store) ObserveChangelogBacklog(ctx context.Context, due int64) {
	if s.Redis != nil {
		_ = s.Redis.Set(ctx, "metrics:changelog:due", due, 30*time.Minute).Err()
	}
}

func (s *Store) ObserveGitHubQuota(ctx context.Context, remaining int64) {
	if s.Redis != nil {
		_ = s.Redis.Set(ctx, "metrics:github:remaining", remaining, time.Hour).Err()
	}
}
