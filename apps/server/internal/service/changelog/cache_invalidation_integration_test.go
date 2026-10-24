//go:build integration

package changelog_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/cache"
	pipeline "github.com/opennavo/opennavo/server/internal/changelog"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/service/changelog"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestFetchRetriesInvalidationAndClearsDetailsInEveryLocale(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewStore(t)
	p, err := homebrew.Normalize("cask", json.RawMessage(`{"token":"sample","version":"1.0.0"}`))
	require.NoError(t, err)
	require.NoError(t, st.ApplyCatalogBatch(ctx, []store.CatalogMutation{{Package: p}}))
	var pid, sid int64
	require.NoError(t, st.DB.Raw("SELECT id FROM packages WHERE token='sample'").Scan(&pid).Error)
	require.NoError(t, st.DB.Exec("DELETE FROM job_outbox").Error)
	require.NoError(t, st.DB.Raw(`INSERT INTO changelog_sources(package_id,type,config,priority,resolved_by) VALUES(?,'homebrew_commits','{"repo":"Homebrew/homebrew-cask","path":"Casks/s/sample.rb"}',90,'auto') RETURNING id`, pid).Scan(&sid).Error)
	redis := st.Redis
	defer func() { st.Redis = redis }()
	stop, err := cache.Subscribe(ctx, redis, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	defer stop()
	keys := []string{"c:pkg:c5:cask:sample:en-US", "c:pkg:c5:cask:sample:zh-CN", "c:pkg:c5:cask:sample:ja-JP", "c:rel:c4:cask:sample:en-US:1"}
	for _, key := range keys {
		require.NoError(t, redis.Set(ctx, key, "old", time.Hour).Err())
	}
	preserved := []string{"c:pkg:c5:cask:sample-other:en-US", "c:rel:c4:cask:sample-other:en-US:1", "c:rank:sentinel", "session:sentinel"}
	for _, key := range preserved {
		require.NoError(t, redis.Set(ctx, key, "keep", time.Hour).Err())
	}
	st.Redis = testutil.RedisWithoutPublish(t, redis)
	status := http.StatusOK
	fetcher := pipeline.NewFetcher("test")
	fetcher.HTTP.Client = &http.Client{Transport: responseTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: status, Header: http.Header{"Etag": {`"v1"`}}, Body: io.NopCloser(strings.NewReader(`[{"sha":"first","commit":{"message":"sample 0.9.0","committer":{"date":"2026-09-30T00:00:00Z"}}}]`))}, nil
	})}
	service := &changelog.Service{Store: st, Fetcher: fetcher}
	_, err = service.Fetch(ctx, sid)
	require.ErrorContains(t, err, "publish cache invalidations")
	var count int64
	require.NoError(t, st.DB.Table("package_versions").Where("package_id=?", pid).Count(&count).Error)
	require.EqualValues(t, 2, count, "new version committed before publication failed")
	require.NoError(t, st.DB.Table("job_outbox").Where("job_type='cache:invalidate'").Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.EqualValues(t, len(keys), redis.Exists(ctx, keys...).Val())

	st.Redis = redis
	status = http.StatusNotModified
	stats, err := service.Fetch(ctx, sid)
	require.NoError(t, err)
	require.Equal(t, false, stats["changed"])
	require.Equal(t, true, stats["notModified"])
	require.Eventually(t, func() bool { return redis.Exists(ctx, keys...).Val() == 0 }, 2*time.Second, 10*time.Millisecond)
	for _, key := range preserved {
		require.Equal(t, "keep", redis.Get(ctx, key).Val())
	}
	require.NoError(t, st.DB.Table("job_outbox").Where("job_type='cache:invalidate'").Count(&count).Error)
	require.Zero(t, count)

	messages := redis.Subscribe(ctx, "cache:invalidate")
	defer func() { require.NoError(t, messages.Close()) }()
	_, err = messages.Receive(ctx)
	require.NoError(t, err)
	_, err = service.Fetch(ctx, sid)
	require.NoError(t, err)
	messageCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	_, err = messages.ReceiveMessage(messageCtx)
	require.Error(t, err, "an unchanged fetch must not publish another invalidation")
}
