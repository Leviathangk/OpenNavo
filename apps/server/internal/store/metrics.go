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
}

func (s *Store) MetricState(ctx context.Context, now time.Time) (MetricState, error) {
	out := MetricState{GitHubRemaining: -1}
	if err := s.DB.WithContext(ctx).Table("translation_metrics").Find(&out.Translations).Error; err != nil {
		return out, err
	}
	if err := s.DB.WithContext(ctx).Raw("SELECT * FROM job_metrics").Scan(&out.Jobs).Error; err != nil {
		return out, err
	}
	if err := s.DB.WithContext(ctx).Raw("SELECT kind,count(*) AS count FROM packages WHERE removed_at IS NULL GROUP BY kind").Scan(&out.Packages).Error; err != nil {
		return out, err
	}
	if s.Redis != nil {
		if value, err := s.Redis.Get(ctx, "metrics:github:remaining").Int64(); err == nil {
			out.GitHubRemaining = value
		}
	}
	return out, nil
}
func (s *Store) ObserveGitHubQuota(ctx context.Context, remaining int64) {
	if s.Redis != nil {
		_ = s.Redis.Set(ctx, "metrics:github:remaining", remaining, time.Hour).Err()
	}
}
