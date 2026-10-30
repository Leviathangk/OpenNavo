package cache

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestVisibilityGenerationDiscardsConcurrentRefill(t *testing.T) {
	for _, compressed := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "compressed"}[compressed], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			memory := &limitedBackend{entries: map[string][]byte{}}
			var generation atomic.Int64
			backend := Visibility{Backend: memory, Generation: func(context.Context) (int64, error) { return generation.Load(), nil }}
			read := ReadThrough[[]string]
			if compressed {
				read = ReadThroughCompressed[[]string]
			}
			started, finish, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			go func() {
				_, err := read(ctx, backend, "c:test:public", time.Minute, func(context.Context) ([]string, error) {
					close(started)
					select {
					case <-finish:
						return []string{"now-private"}, nil
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				})
				done <- err
			}()
			<-started
			generation.Add(1)
			clear(memory.entries) // The old implementation's after-commit clear.
			close(finish)
			require.NoError(t, <-done)
			require.Empty(t, memory.entries, "the pre-change read must not repopulate the cache")
			fresh, err := read(ctx, backend, "c:test:public", time.Minute, func(context.Context) ([]string, error) { return []string{}, nil })
			require.NoError(t, err)
			require.Empty(t, fresh)
		})
	}
}

func TestVisibilityGenerationSeparatesWriteAfterFinalCheck(t *testing.T) {
	ctx := context.Background()
	memory := &limitedBackend{entries: map[string][]byte{}}
	var generation int64
	backend := Visibility{Backend: memory, Generation: func(context.Context) (int64, error) { return generation, nil }}
	old := Scope(ctx, backend)
	generation++
	// Simulate the final SET racing a commit after its generation check.
	oldScope := old.(visibilityScope)
	require.NoError(t, memory.Save(ctx, oldScope.key("c:pkg:detail"), []byte(`"private"`), time.Minute))
	_, err := Scope(ctx, backend).Load(ctx, "c:pkg:detail")
	require.Error(t, err)
}
