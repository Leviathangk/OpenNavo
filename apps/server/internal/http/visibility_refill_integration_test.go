//go:build integration

package httpserver_test

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/config"
	httpserver "github.com/opennavo/opennavo/server/internal/http"
	"github.com/opennavo/opennavo/server/internal/http/public"
	"github.com/opennavo/opennavo/server/internal/service/catalog"
	"github.com/opennavo/opennavo/server/internal/service/search"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestPublicVisibilityGenerationRejectsConcurrentRefills(t *testing.T) {
	st := testutil.NewStore(t)
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash,desc_en,popularity,installs_30d,rank_30d)
VALUES(991,'cask','refill-secret','refill-secret','homebrew/cask','Refill secret','1','1','{}','fixture','Useful refill application',100,100,1);
INSERT INTO package_meta(package_id) VALUES(991);
INSERT INTO collections(id,slug,status) VALUES(991,'refill-reference','published');
INSERT INTO collection_i18n(collection_id,locale,title) VALUES(991,'en-US','Reference');
INSERT INTO collection_items(collection_id,package_id,sort) VALUES(991,991,0);
INSERT INTO articles(id,slug,status,working_revision_id,published_revision_id,first_published_at) VALUES(991,'refill-reference','published',991,991,now());
INSERT INTO article_revisions(id,article_id,number,source_locale,type) VALUES(991,991,1,'en-US','roundup');
INSERT INTO article_revision_i18n(revision_id,locale,source_locale,title) VALUES(991,'en-US','en-US','Reference');
INSERT INTO article_revision_packages(revision_id,package_id,sort) VALUES(991,991,0)`).Error)
	// Match deployments with business Redis and disposable cache in different DBs.
	opts := *st.Redis.Options()
	opts.DB = 9
	cacheRedis := redis.NewClient(&opts)
	t.Cleanup(func() { require.NoError(t, cacheRedis.Close()) })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, path := range []string{
		"/packages/cask/refill-secret", "/home", "/rankings?kind=cask",
		"/search/suggest?q=refill", "/articles/refill-reference", "/browse?kind=apps",
		"/sitemap/packages?group=apps",
	} {
		t.Run(path, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			require.NoError(t, st.DB.Exec("UPDATE package_meta SET hidden=false,private=false WHERE package_id=991").Error)
			require.NoError(t, cache.Invalidate(ctx, cacheRedis, []string{"c:*"}))
			before, err := st.VisibilityGeneration(ctx)
			require.NoError(t, err)
			started, finish := make(chan struct{}), make(chan struct{})
			var calls atomic.Int64
			backend := cache.Visibility{Backend: cache.Redis{Client: cacheRedis}, Generation: func(ctx context.Context) (int64, error) {
				if calls.Add(1) == 2 { // The response was loaded; pause its final cache write.
					close(started)
					select {
					case <-finish:
					case <-ctx.Done():
						return 0, ctx.Err()
					}
				}
				return st.VisibilityGeneration(ctx)
			}}
			searchService := search.New(ctx, st, backend, logger)
			defer searchService.Close()
			svc := &catalog.PublicService{Store: st, Cache: backend}
			server, err := httpserver.New(config.Config{}, logger, st, httpserver.Dependencies{Public: &public.Handler{Catalog: svc, Search: searchService}})
			require.NoError(t, err)
			call := func(path string) *httptest.ResponseRecorder {
				r := httptest.NewRequest("GET", "/api/v1"+path, nil).WithContext(ctx)
				r.Header.Set("Accept-Language", "en-US")
				w := httptest.NewRecorder()
				server.Engine.ServeHTTP(w, r)
				return w
			}
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() { done <- call(path) }()
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal("public read did not reach its cache refill")
			}
			require.NoError(t, st.DB.Exec("UPDATE package_meta SET private=true,visibility_reason='Owner decision' WHERE package_id=991").Error)
			after, err := st.VisibilityGeneration(ctx)
			require.NoError(t, err)
			require.Greater(t, after, before)
			require.NoError(t, cache.Invalidate(ctx, cacheRedis, []string{"c:*"}))
			close(finish)
			old := <-done
			require.Equal(t, 200, old.Code, old.Body.String())
			keys, err := cacheRedis.Keys(ctx, "c:*").Result()
			require.NoError(t, err)
			require.Empty(t, keys, "the read started before privacy and must not refill after commit")
			for _, next := range []string{path, "/packages", "/search?q=refill", "/collections", "/collections/refill-reference", "/collections/refill-reference/brewfile", "/articles/refill-reference"} {
				response := call(next)
				if next == "/packages/cask/refill-secret" {
					require.Equal(t, 404, response.Code)
					continue
				}
				require.Equal(t, 200, response.Code, response.Body.String())
				require.NotContains(t, response.Body.String(), "refill-secret", next)
			}
		})
	}
}
