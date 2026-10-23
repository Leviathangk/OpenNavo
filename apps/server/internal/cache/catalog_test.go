package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestInvalidationChannelIncludesNonDefaultRedisDatabase(t *testing.T) {
	for number, expected := range map[int]string{0: "cache:invalidate", 9: "cache:invalidate:db:9", 10: "cache:invalidate:db:10"} {
		client := redis.NewClient(&redis.Options{DB: number})
		require.Equal(t, expected, invalidationChannel(client))
		require.NoError(t, client.Close())
	}
}

func TestCoalescerAppliesQuietBatchesAtOnceAndMergesBursts(t *testing.T) {
	start := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	batch := coalescer{interval: 10 * time.Second}
	due, ok := batch.add(start, []string{"c:pkg:*", "*", "session:*"})
	require.True(t, ok)
	require.Equal(t, start, due, "the first invalidation after a quiet interval must not wait")
	require.Equal(t, []string{"c:pkg:*"}, batch.take(start), "invalid patterns are dropped")

	due, ok = batch.add(start.Add(time.Second), []string{"c:rel:*"})
	require.True(t, ok)
	require.Equal(t, start.Add(10*time.Second), due, "a burst waits for the interval since the previous pass")
	due, ok = batch.add(start.Add(2*time.Second), []string{"c:home:*", "c:rel:*"})
	require.True(t, ok)
	require.Equal(t, start.Add(10*time.Second), due)
	require.Equal(t, []string{"c:home:*", "c:rel:*"}, batch.take(due))

	_, _ = batch.add(start.Add(11*time.Second), []string{"c:pkg:*"})
	_, _ = batch.add(start.Add(12*time.Second), []string{"c:*"})
	require.Equal(t, []string{"c:*"}, batch.take(start.Add(20*time.Second)), "c:* supersedes narrower patterns")
	require.Nil(t, batch.take(start.Add(21*time.Second)))

	_, ok = batch.add(start.Add(22*time.Second), []string{"session:*"})
	require.False(t, ok, "a batch of invalid patterns schedules nothing")
	due, ok = batch.add(start.Add(time.Minute), []string{"c:cat:*"})
	require.True(t, ok)
	require.Equal(t, start.Add(time.Minute), due)
}

func TestPatternLiteralEscapesGlobMetacharacters(t *testing.T) {
	require.Equal(t, "visual-studio-code", PatternLiteral("visual-studio-code"))
	require.Equal(t, `a\*b\?c\[d\]e\\f`, PatternLiteral(`a*b?c[d]e\f`))
}

// limitedBackend mirrors the Redis backend's 512 KiB entry limit.
type limitedBackend struct{ entries map[string][]byte }

func (b *limitedBackend) Load(_ context.Context, key string) ([]byte, error) {
	if value, ok := b.entries[key]; ok {
		return value, nil
	}
	return nil, errors.New("miss")
}
func (b *limitedBackend) Save(_ context.Context, key string, value []byte, _ time.Duration) error {
	if len(value) > 512*1024 {
		delete(b.entries, key)
		return nil
	}
	b.entries[key] = value
	return nil
}

func TestReadThroughCompressedCachesValuesAboveTheEntryLimit(t *testing.T) {
	ctx := context.Background()
	backend := &limitedBackend{entries: map[string][]byte{}}
	type page struct{ Records []string }
	large := page{Records: make([]string, 40000)}
	for i := range large.Records {
		large.Records[i] = fmt.Sprintf(`{"locale":"en-US","inSitemap":true,"canonicalPath":"/apps/app-%d"}`, i%50)
	}
	loads := 0
	load := func(context.Context) (page, error) {
		loads++
		return large, nil
	}
	first, err := ReadThroughCompressed(ctx, backend, "c:test:large", time.Minute, load)
	require.NoError(t, err)
	require.Equal(t, large, first)
	raw, err := json.Marshal(large)
	require.NoError(t, err)
	require.Greater(t, len(raw), 512*1024, "the fixture must exceed the plain entry limit")
	require.Less(t, len(backend.entries["c:test:large"]), 512*1024)
	second, err := ReadThroughCompressed(ctx, backend, "c:test:large", time.Minute, load)
	require.NoError(t, err)
	require.Equal(t, large, second)
	require.Equal(t, 1, loads, "the second read is served from the compressed entry")

	// A corrupt entry is treated as a miss, and load errors are not cached.
	backend.entries["c:test:large"] = []byte("not gzip")
	_, err = ReadThroughCompressed(ctx, backend, "c:test:large", time.Minute, func(context.Context) (page, error) {
		return page{}, errors.New("unavailable")
	})
	require.Error(t, err)
	third, err := ReadThroughCompressed(ctx, backend, "c:test:large", time.Minute, load)
	require.NoError(t, err)
	require.Equal(t, large, third)
	require.Equal(t, 2, loads)
}
