//go:build integration

package catalog

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/homebrew"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestCatalogRetriesInvalidationAfterCommittedSync(t *testing.T) {
	for _, notModified := range []bool{false, true} {
		name := "identical_response"
		if notModified {
			name = "not_modified"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			st := testutil.NewFactsStore(t)
			redis := st.Redis
			defer func() { st.Redis = redis }()
			stop, err := cache.Subscribe(ctx, redis, slog.New(slog.NewTextHandler(io.Discard, nil)))
			require.NoError(t, err)
			defer stop()
			keys := []string{"c:pkg:c5:cask:sample:en-US", "c:rank:sentinel", "c:home:sentinel", "c:cat:sentinel", "c:sug:sentinel", "c:dep:sentinel", "c:related:sentinel", browseFreshKey("apps")}
			// Browse pages outlive the sync: they serve until a rebuild replaces them.
			browsePages := []string{browseKey("apps", "en-US", "", 1), browseKey("apps", "en-US", "s", 1)}
			for _, key := range append(append([]string{}, keys...), browsePages...) {
				require.NoError(t, redis.Set(ctx, key, "old", time.Hour).Err())
			}
			require.NoError(t, redis.Set(ctx, "session:sentinel", "keep", time.Hour).Err())
			st.Redis = testutil.RedisWithoutPublish(t, redis)
			source := &testSource{Data: map[string][]string{"cask": {`{"token":"sample","version":"1"}`}}, Previous: map[string]homebrew.Conditional{}}
			service := &Service{Store: st, Source: source, Queue: &testQueue{}}
			_, err = service.Run(ctx)
			require.ErrorContains(t, err, "publish cache invalidations")
			var count int64
			require.NoError(t, st.DB.Table("packages").Where("token='sample'").Count(&count).Error)
			require.EqualValues(t, 1, count, "catalog committed before publication failed")
			require.NoError(t, st.DB.Table("job_outbox").Where("job_type='cache:invalidate'").Count(&count).Error)
			var invalidations []struct{ UniqueKey, Payload string }
			require.NoError(t, st.DB.Raw("SELECT unique_key,payload::text FROM job_outbox WHERE job_type='cache:invalidate' ORDER BY unique_key").Scan(&invalidations).Error)
			require.EqualValues(t, 1, count, "pending invalidations: %+v", invalidations)
			require.EqualValues(t, len(keys), redis.Exists(ctx, keys...).Val())

			st.Redis = redis
			source.NotModified = notModified
			stats, err := service.Run(ctx)
			require.NoError(t, err)
			kind := stats["kinds"].([]Stats)[0]
			require.Zero(t, kind.Inserted+kind.Updated+kind.Removed)
			require.Eventually(t, func() bool { return redis.Exists(ctx, keys...).Val() == 0 }, 2*time.Second, 10*time.Millisecond)
			require.Equal(t, "keep", redis.Get(ctx, "session:sentinel").Val())
			require.EqualValues(t, len(browsePages), redis.Exists(ctx, browsePages...).Val())
			require.NoError(t, st.DB.Table("job_outbox").Where("job_type='cache:invalidate'").Count(&count).Error)
			require.Zero(t, count)

			// A normal unchanged sync should keep caches warm and send no event.
			messages := redis.Subscribe(ctx, "cache:invalidate")
			defer func() { require.NoError(t, messages.Close()) }()
			_, err = messages.Receive(ctx)
			require.NoError(t, err)
			_, err = service.Run(ctx)
			require.NoError(t, err)
			messageCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
			defer cancel()
			_, err = messages.ReceiveMessage(messageCtx)
			require.Error(t, err)
		})
	}
}
