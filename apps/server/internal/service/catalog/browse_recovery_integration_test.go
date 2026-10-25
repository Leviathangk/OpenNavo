//go:build integration

package catalog

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type missingBrowseSlice struct {
	cache.Backend
	key      string
	observed chan struct{}
	once     sync.Once
}

func (c *missingBrowseSlice) Load(ctx context.Context, key string) ([]byte, error) {
	data, err := c.Backend.Load(ctx, key)
	if key == browseFreshKey("apps") {
		c.once.Do(func() { close(c.observed) })
	}
	return data, err
}
func (c *missingBrowseSlice) Save(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if key == c.key {
		return errors.New("fixture cache write failure")
	}
	return c.Backend.Save(ctx, key, value, ttl)
}

func TestBrowseMaterializesMissingSliceAfterOtherInstance(t *testing.T) {
	st := testutil.NewFactsStore(t)
	ctx := context.Background()
	for _, bucket := range []string{"", "a"} {
		t.Run("bucket="+bucket, func(t *testing.T) {
			require.NoError(t, cache.Invalidate(ctx, st.Redis, []string{"c:browse:*"}))
			require.NoError(t, st.Redis.Set(ctx, "browse:build:apps", "another-instance", time.Minute).Err())
			backend := &missingBrowseSlice{Backend: cache.Redis{Client: st.Redis}, key: browseKey("apps", "en-US", bucket, 1), observed: make(chan struct{})}
			svc := &PublicService{Store: st, Cache: backend}
			// Even a legitimate bucket must be served when its slice cannot be cached.
			require.NoError(t, st.DB.Exec(`INSERT INTO packages(kind,token,full_token,tap,name,version,version_base,raw,raw_hash,desc_en,source_locale)
    VALUES('cask','recovery-app','recovery-app','homebrew/cask','Alpha','1','1','{}','fixture','Useful app','en-US') ON CONFLICT DO NOTHING`).Error)
			done := make(chan error, 1)
			go func() {
				page, err := svc.Browse(ctx, "apps", bucket, "en-US", 1)
				if err == nil && bucket == "a" && len(page.Items) != 1 {
					err = errors.New("missing rebuilt app")
				}
				done <- err
			}()
			select {
			case <-backend.observed:
			case <-time.After(5 * time.Second):
				t.Fatal("did not wait for other builder")
			}
			// Another process finishes, but leaves no target slice behind.
			require.NoError(t, st.Redis.Set(ctx, browseFreshKey("apps"), "other-generation", time.Minute).Err())
			// Keep its lock until our caller has observed the generation change.
			require.Never(t, func() bool {
				select {
				case <-done:
					return true
				default:
					return false
				}
			}, 300*time.Millisecond, 20*time.Millisecond)
			require.NoError(t, st.Redis.Del(ctx, "browse:build:apps").Err())
			select {
			case err := <-done:
				require.NoError(t, err)
			case <-time.After(10 * time.Second):
				t.Fatal("fallback did not finish")
			}
			_, err := backend.Load(ctx, backend.key)
			require.Error(t, err, "the response must come from the built directory even when cache writes fail")
		})
	}
}

func TestBrowseFailedRefreshReleasesOnlyItsReservation(t *testing.T) {
	st := testutil.NewFactsStore(t)
	ctx := context.Background()
	for _, replaced := range []bool{false, true} {
		name := "retry"
		if replaced {
			name = "replacement-owner"
		}
		t.Run(name, func(t *testing.T) {
			require.NoError(t, st.Redis.Del(ctx, "browse:refresh:apps", browseFreshKey("apps")).Err())
			backend := cache.Redis{Client: st.Redis}
			require.NoError(t, backend.Save(ctx, browseKey("apps", "en-US", "", 1), []byte(`{"buckets":[],"items":[]}`), time.Hour))
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			callback := "fixture:browse-failure"
			require.NoError(t, st.DB.Callback().Row().Before("gorm:row").Register(callback, func(db *gorm.DB) {
				once.Do(func() { close(entered); <-release })
				_ = db.AddError(errors.New("fixture transient database failure"))
			}))
			svc := &PublicService{Store: st, Cache: backend}
			_, err := svc.Browse(ctx, "apps", "", "en-US", 1)
			require.NoError(t, err, "stale page remains available")
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("refresh did not start")
			}
			require.NotEmpty(t, st.Redis.Get(ctx, "browse:refresh:apps").Val())
			if replaced {
				require.NoError(t, st.Redis.Set(ctx, "browse:refresh:apps", "new-owner", time.Minute).Err())
			}
			close(release)
			require.Eventually(t, func() bool { _, running := browseRefreshes.Load(svc.browseBuildKey("apps")); return !running }, 5*time.Second, 10*time.Millisecond)
			require.NoError(t, st.DB.Callback().Row().Remove(callback))
			if replaced {
				require.Equal(t, "new-owner", st.Redis.Get(ctx, "browse:refresh:apps").Val())
				return
			}
			require.EqualValues(t, 0, st.Redis.Exists(ctx, "browse:refresh:apps").Val())
			_, err = svc.Browse(ctx, "apps", "", "en-US", 1)
			require.NoError(t, err)
			require.Eventually(t, func() bool {
				_, running := browseRefreshes.Load(svc.browseBuildKey("apps"))
				return !running && st.Redis.Exists(ctx, browseFreshKey("apps")).Val() == 1
			}, 10*time.Second, 20*time.Millisecond)
		})
	}
}
