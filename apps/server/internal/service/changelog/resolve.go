package changelog

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/opennavo/opennavo/server/internal/changelog"
	"github.com/opennavo/opennavo/server/internal/store"
)

type persistenceError struct{ cause error }

func (e *persistenceError) Error() string {
	return fmt.Sprintf("persist changelog sources: %v", e.cause)
}
func (e *persistenceError) Unwrap() error { return e.cause }

type Service struct {
	Store    *store.Store
	Resolver *changelog.Resolver
	Fetcher  *changelog.Fetcher
	Queue    Enqueuer
	Now      func() time.Time
	// Release gives a claim back; nil means the store's own ReleaseChangelogClaim. Tests replace it to fail
	// the release, which no database state can force on its own.
	Release func(context.Context, []int64) error
}

func (s *Service) Resolve(ctx context.Context, id int64) (map[string]any, error) {
	p, err := s.Store.ChangelogCandidate(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.resolve(ctx, p)
}
func (s *Service) resolve(ctx context.Context, p changelog.Candidate) (map[string]any, error) {
	settings, err := s.Store.PackageChangelogSettings(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	if settings.Excluded {
		return map[string]any{"skipped": true, "reason": "excluded"}, nil
	}
	sources, stats, err := s.Resolver.Resolve(ctx, p)
	if err != nil {
		return stats, err
	}
	if saveErr := s.Store.SaveChangelogSources(ctx, p.ID, sources); saveErr != nil {
		return stats, &persistenceError{cause: saveErr}
	}
	stats["sources"] = len(sources)
	return stats, err
}
func (s *Service) Top(ctx context.Context, limit int) (map[string]any, error) {
	rows, err := s.Store.ChangelogTop(ctx, limit)
	if err != nil {
		return nil, err
	}
	stats := map[string]any{"packages": len(rows), "homebrew": 0, "github": 0}
	for _, p := range rows {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		counts, err := s.resolve(ctx, p)
		if err != nil {
			var persistent *persistenceError
			if errors.As(err, &persistent) {
				return stats, err
			}
			var rate *changelog.RateLimitError
			if !errors.As(err, &rate) {
				stats["upstreamErrors"] = true
			}
		}
		for _, key := range []string{"homebrew", "github"} {
			if value, ok := counts[key].(bool); ok && value {
				stats[key] = stats[key].(int) + 1
			}
		}
	}
	return stats, nil
}
