package cache

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Visibility fences an entire cache refill with the database's committed
// visibility generation. The source must use the primary database, not a cache
// or a request's older repeatable-read snapshot.
type Visibility struct {
	Backend
	Generation func(context.Context) (int64, error)
}

type visibilityScope struct {
	Visibility
	generation int64
}

// Scope pins the generation before loading public data. A failed generation
// read bypasses the optional cache, so it cannot admit an old public response.
func Scope(ctx context.Context, backend Backend) Backend {
	if source, ok := backend.(Visibility); ok {
		if source.Backend == nil {
			return nil
		}
		generation, err := source.Generation(ctx)
		if err != nil {
			return nil
		}
		return visibilityScope{Visibility: source, generation: generation}
	}
	return backend
}

func (s Visibility) Load(ctx context.Context, key string) ([]byte, error) {
	if backend := Scope(ctx, s); backend != nil {
		return backend.Load(ctx, key)
	}
	return nil, errors.New("visibility generation unavailable")
}

func (s Visibility) Save(context.Context, string, []byte, time.Duration) error {
	return errors.New("visibility cache writes require a refill scope")
}

func Namespace(backend Backend) string {
	if scope, ok := backend.(visibilityScope); ok {
		return strconv.FormatInt(scope.generation, 10)
	}
	return ""
}

func (s visibilityScope) key(key string) string {
	// Preserve both invalidation prefixes and the browse freshness suffix.
	if strings.HasSuffix(key, ":fresh") {
		return strings.TrimSuffix(key, ":fresh") + ":visibility:" + Namespace(s) + ":fresh"
	}
	return key + ":visibility:" + Namespace(s)
}

func (s visibilityScope) Load(ctx context.Context, key string) ([]byte, error) {
	return s.Backend.Load(ctx, s.key(key))
}

func (s visibilityScope) Save(ctx context.Context, key string, data []byte, ttl time.Duration) error {
	current, err := s.Generation(ctx)
	if err != nil {
		return err
	}
	if current != s.generation {
		return errors.New("discarded stale visibility cache refill")
	}
	// If visibility commits between the check and this write, the old key is
	// still unreachable by readers scoped to the new generation.
	return s.Backend.Save(ctx, s.key(key), data, ttl)
}
