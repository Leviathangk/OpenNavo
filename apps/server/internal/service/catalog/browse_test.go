package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type memoryCache struct {
	mu      sync.Mutex
	entries map[string][]byte
}

func (c *memoryCache) Load(_ context.Context, key string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	value, ok := c.entries[key]
	if !ok {
		return nil, errors.New("miss")
	}
	return value, nil
}

func (c *memoryCache) Save(_ context.Context, key string, value []byte, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = value
	return nil
}

type lockedLog struct {
	mu   sync.Mutex
	text strings.Builder
}

func (l *lockedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.text.Write(p)
}

func (l *lockedLog) count(message string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Count(l.text.String(), message)
}

// A background build that fails is logged and leaves the stale pages serving; the next stale read tries again.
func TestBrowseRefreshFailureKeepsStalePagesAndRetries(t *testing.T) {
	// Nothing listens on port 1, so every build fails at its first query.
	db, err := gorm.Open(postgres.Open("host=127.0.0.1 port=1 user=nobody dbname=nothing sslmode=disable connect_timeout=1"), &gorm.Config{Logger: logger.Discard, DisableAutomaticPing: true})
	require.NoError(t, err)
	stale := publicapi.BrowsePage{Buckets: []publicapi.BrowseBucket{}, Items: []publicapi.BrowseItem{{Token: "stale-app", DisplayName: "Alpha"}}}
	raw, err := json.Marshal(stale)
	require.NoError(t, err)
	cached := &memoryCache{entries: map[string][]byte{browseKey("apps", "en-US", "a", 1): raw}}
	svc := &PublicService{Store: &store.Store{DB: db}, Cache: cached}
	logs := &lockedLog{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	key := svc.browseBuildKey("apps")
	for attempt := 1; attempt <= 2; attempt++ {
		page, err := svc.Browse(context.Background(), "apps", "a", "en-US", 1)
		require.NoError(t, err)
		require.Equal(t, stale, page)
		require.Eventually(t, func() bool {
			_, running := browseRefreshes.Load(key)
			return !running && logs.count("browse refresh failed") == attempt
		}, 10*time.Second, 10*time.Millisecond, "attempt %d", attempt)
	}
	_, err = cached.Load(context.Background(), browseFreshKey("apps"))
	require.Error(t, err, "a failed build never marks the pages fresh")
}

// Catalog and translation changes drop only the fresh marks, so the pattern must match each kind's mark and no page.
func TestBrowseRefreshPatternMatchesOnlyFreshMarks(t *testing.T) {
	for _, kind := range []string{"apps", "fonts"} {
		matched, err := path.Match(store.BrowseRefreshPattern, browseFreshKey(kind))
		require.NoError(t, err)
		require.True(t, matched, kind)
		for _, key := range []string{browseKey(kind, "en-US", "", 1), browseKey(kind, "zh-Hant", "a", 2), browseKey(kind, "de-DE", "other", 1)} {
			matched, err = path.Match(store.BrowseRefreshPattern, key)
			require.NoError(t, err)
			require.False(t, matched, key)
		}
	}
}
