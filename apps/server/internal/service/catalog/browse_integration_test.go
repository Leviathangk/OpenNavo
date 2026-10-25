//go:build integration

package catalog_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/service/catalog"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/opennavo/opennavo/server/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestBrowseReusesDirectoryAcrossBucketsAndPages(t *testing.T) {
	st := testutil.NewFactsStore(t)
	ctx := context.Background()
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(kind,token,full_token,tap,name,version,version_base,raw,raw_hash,desc_en,source_locale)
 SELECT 'cask','browse-'||n,'browse-'||n,'homebrew/cask',CASE WHEN n=302 THEN 'Beta' ELSE 'Alpha '||n END,'1','1','{}','fixture','Useful app','en-US' FROM generate_series(1,302) n`).Error)
	svc := &catalog.PublicService{Store: st, Cache: cache.Redis{Client: st.Redis}}
	// A caller whose request ends stops waiting at once; the shared build carries on for the next caller.
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err := svc.Browse(canceled, "apps", "", "en-US", 1)
	require.ErrorIs(t, err, context.Canceled)
	root, err := svc.Browse(ctx, "apps", "", "en-US", 1)
	require.NoError(t, err)
	require.Len(t, root.Buckets, 2)
	// A different bucket and page must use the same directory's cached slices.
	svc.Store = nil
	page, err := svc.Browse(ctx, "apps", "a", "en-US", 2)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	page, err = svc.Browse(ctx, "apps", "b", "en-US", 1)
	require.NoError(t, err)
	require.Equal(t, "browse-302", page.Items[0].Token)
	// The same build stored every other language, so no language waits for its own catalog read.
	japanese, err := svc.Browse(ctx, "apps", "", "ja-JP", 1)
	require.NoError(t, err)
	require.NotNil(t, japanese.Buckets)
	// A page outside the cached root's buckets is a 404 without a rebuild (the store is unset here).
	for _, missing := range []struct {
		bucket string
		page   int
	}{{"b", 2}, {"z", 1}} {
		_, err = svc.Browse(ctx, "apps", missing.bucket, "en-US", missing.page)
		var appErr *domain.AppError
		require.ErrorAs(t, err, &appErr, missing.bucket)
		require.Equal(t, 404, appErr.HTTPStatus)
	}
	notFound := func(bucket string) {
		t.Helper()
		_, err := svc.Browse(ctx, "apps", bucket, "en-US", 1)
		var appErr *domain.AppError
		require.ErrorAs(t, err, &appErr, bucket)
		require.Equal(t, 404, appErr.HTTPStatus)
	}
	// The SEO and management caches still clear every slice: the next request waits for a rebuild.
	svc.Store = st
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(kind,token,full_token,tap,name,version,version_base,raw,raw_hash,desc_en,source_locale)
 VALUES('cask','browse-cobalt','browse-cobalt','homebrew/cask','Cobalt','1','1','{}','fixture','Useful app','en-US')`).Error)
	require.NoError(t, cache.Invalidate(ctx, st.Redis, []string{"c:browse:*"}))
	page, err = svc.Browse(ctx, "apps", "c", "en-US", 1)
	require.NoError(t, err)
	require.Equal(t, "browse-cobalt", page.Items[0].Token)
	// A catalog or translation change only marks the pages stale: the next request still gets the old page while one
	// rebuild replaces it, and once that finishes the disabled app is gone.
	require.NoError(t, st.DB.Exec(`UPDATE packages SET disabled=true WHERE token='browse-302'`).Error)
	require.NoError(t, cache.Invalidate(ctx, st.Redis, []string{store.BrowseRefreshPattern}))
	page, err = svc.Browse(ctx, "apps", "b", "en-US", 1)
	require.NoError(t, err)
	require.Equal(t, "browse-302", page.Items[0].Token, "the old page is served at once")
	require.Eventually(t, func() bool { return st.Redis.Exists(ctx, "c:browse:v2:apps:fresh").Val() == 1 }, 30*time.Second, 20*time.Millisecond)
	notFound("b")
	root, err = svc.Browse(ctx, "apps", "", "en-US", 1)
	require.NoError(t, err)
	require.Len(t, root.Buckets, 2, "a and c remain")
	// The rebuild leaves the dropped page to expire; the new root already answers for it.
	require.EqualValues(t, 1, st.Redis.Exists(ctx, "c:browse:v2:apps:en-US:b:1").Val())
}

// Pages older than the browse TTL are served at once while one background build per kind replaces them, even a
// build that cannot progress; the build outlives the request that started it. Only a page with nothing cached waits.
func TestBrowseServesStalePagesWhileOneBuildRefreshesThem(t *testing.T) {
	st := testutil.NewFactsStore(t)
	ctx := context.Background()
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(kind,token,full_token,tap,name,version,version_base,raw,raw_hash,desc_en,source_locale)
 SELECT 'cask','stale-'||n,'stale-'||n,'homebrew/cask','Alpha '||n,'1','1','{}','fixture','Useful app','en-US' FROM generate_series(1,3) n`).Error)
	svc := &catalog.PublicService{Store: st, Cache: cache.Redis{Client: st.Redis}}
	const fresh = "c:browse:v2:apps:fresh"
	tokens := func(ctx context.Context, bucket, locale string) []string {
		t.Helper()
		page, err := svc.Browse(ctx, "apps", bucket, locale, 1)
		require.NoError(t, err)
		out := []string{}
		for _, item := range page.Items {
			out = append(out, item.Token)
		}
		return out
	}
	require.Equal(t, []string{"stale-1", "stale-2", "stale-3"}, tokens(ctx, "a", "en-US"))
	require.EqualValues(t, 1, st.Redis.Exists(ctx, fresh).Val(), "a build marks its pages fresh")
	// A change nothing invalidates leaves fresh pages as they are.
	require.NoError(t, st.DB.Exec(`UPDATE packages SET name='Beta' WHERE token='stale-1'`).Error)
	require.Equal(t, []string{"stale-1", "stale-2", "stale-3"}, tokens(ctx, "a", "en-US"))

	// Stale pages: hold the next build on a lock of the catalog.
	require.NoError(t, st.Redis.Del(ctx, fresh).Err())
	lock := st.DB.Begin()
	require.NoError(t, lock.Error)
	released := false
	t.Cleanup(func() {
		if !released {
			lock.Rollback()
		}
	})
	require.NoError(t, lock.Exec("LOCK TABLE packages IN ACCESS EXCLUSIVE MODE").Error)
	builds := func() int64 {
		t.Helper()
		var count int64
		require.NoError(t, st.DB.Raw(`SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock'`).Scan(&count).Error)
		return count
	}
	request, cancel := context.WithCancel(ctx)
	require.Equal(t, []string{"stale-1", "stale-2", "stale-3"}, tokens(request, "a", "en-US"), "a stale page is served at once")
	cancel()
	require.Eventually(t, func() bool { return builds() == 1 }, 10*time.Second, 20*time.Millisecond, "the stale read started a build")
	// More stale reads of any page, language or the root start no second build and never wait for the first.
	var readers sync.WaitGroup
	for i := 0; i < 20; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for _, locale := range []string{"en-US", "ja-JP"} {
				for _, bucket := range []string{"", "a"} {
					if _, err := svc.Browse(ctx, "apps", bucket, locale, 1); err != nil {
						t.Error(err)
					}
				}
			}
			// A page outside the stale root is still a 404 without waiting.
			_, err := svc.Browse(ctx, "apps", "z", "en-US", 1)
			var appErr *domain.AppError
			if !errors.As(err, &appErr) || appErr.HTTPStatus != 404 {
				t.Errorf("want a 404, got %v", err)
			}
		}()
	}
	readers.Wait()
	time.Sleep(200 * time.Millisecond)
	require.EqualValues(t, 1, builds(), "one build per kind")

	// Once the catalog is readable, that build, detached from its canceled request, replaces the pages.
	require.NoError(t, lock.Rollback().Error)
	released = true
	require.Eventually(t, func() bool { return st.Redis.Exists(ctx, fresh).Val() == 1 }, 30*time.Second, 20*time.Millisecond)
	require.Equal(t, []string{"stale-2", "stale-3"}, tokens(ctx, "a", "en-US"))
	require.Equal(t, []string{"stale-1"}, tokens(ctx, "b", "en-US"))

	// A page with nothing cached waits for a build of its own, even beside cached pages.
	require.NoError(t, st.DB.Exec(`UPDATE packages SET name='Beta 2' WHERE token='stale-2'`).Error)
	require.NoError(t, st.Redis.Del(ctx, "c:browse:v2:apps:en-US:b:1").Err())
	require.Equal(t, []string{"stale-1", "stale-2"}, tokens(ctx, "b", "en-US"))
}

// A language closed for indexing (pages.openLocales) keeps its A–Z directory, which the web renders with noindex:
// it lists the apps translated into it, as an open language would.
func TestBrowseKeepsTheDirectoriesOfClosedLanguages(t *testing.T) {
	st := testutil.NewFactsStore(t)
	ctx := context.Background()
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(id,kind,token,full_token,tap,name,version,version_base,raw,raw_hash,desc_en,source_locale)
 VALUES(951,'cask','closed-translated','closed-translated','homebrew/cask','Atlas','1','1','{}','fixture','Useful app','en-US'),
 (952,'cask','closed-english','closed-english','homebrew/cask','Aurora','1','1','{}','fixture','Useful app','en-US')`).Error)
	require.NoError(t, st.DB.Exec(`INSERT INTO package_i18n(package_id,locale,source_locale,display_name,summary,description,status,source_hash,source)
 SELECT id,locale,'en-US',name,'A useful macOS app','A complete description of a useful macOS app','machine',repeat('a',64),'rule'
 FROM packages CROSS JOIN unnest(ARRAY['en-US','de-DE']) locale WHERE id=951 OR locale='en-US'`).Error)
	require.NoError(t, st.DB.Exec(`UPDATE seo_settings SET settings=jsonb_set(settings,'{pages}',COALESCE(settings->'pages','{}'::jsonb)||'{"openLocales":["en-US"]}')`).Error)
	policy, err := st.IndexPolicy(ctx)
	require.NoError(t, err)
	require.False(t, policy.LocaleOpen("de-DE"), "German is closed for indexing")
	svc := &catalog.PublicService{Store: st, Cache: cache.Redis{Client: st.Redis}}
	tokens := func(locale string) []string {
		t.Helper()
		page, err := svc.Browse(ctx, "apps", "a", locale, 1)
		require.NoError(t, err, locale)
		out := []string{}
		for _, item := range page.Items {
			out = append(out, item.Token)
		}
		return out
	}
	require.Equal(t, []string{"closed-translated"}, tokens("de-DE"), "the German directory lists the app translated into German")
	require.Equal(t, []string{"closed-translated", "closed-english"}, tokens("en-US"))
	root, err := svc.Browse(ctx, "apps", "", "de-DE", 1)
	require.NoError(t, err)
	require.Len(t, root.Buckets, 1)
}

func TestBrowseWarmupAfterEvictionWithoutTraffic(t *testing.T) {
	st := testutil.NewFactsStore(t)
	svc := &catalog.PublicService{Store: st, Cache: cache.Redis{Client: st.Redis}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); svc.WarmBrowse(ctx) }()
	require.Eventually(t, func() bool {
		return st.Redis.Exists(ctx, "c:browse:v2:apps:fresh", "c:browse:v2:fonts:fresh").Val() == 2
	}, 30*time.Second, 20*time.Millisecond)
	require.NoError(t, cache.Invalidate(ctx, st.Redis, []string{store.BrowseRefreshPattern}))
	require.Never(t, func() bool {
		return st.Redis.Exists(ctx, "c:browse:v2:apps:fresh", "c:browse:v2:fonts:fresh").Val() > 0
	}, 31*time.Second, 100*time.Millisecond, "warmup must not rebuild merely stale directories")
	require.NoError(t, cache.Invalidate(ctx, st.Redis, []string{"c:browse:*"}))
	require.Eventually(t, func() bool {
		return st.Redis.Exists(ctx, "c:browse:v2:apps:en-US::1", "c:browse:v2:fonts:en-US::1").Val() == 2
	}, 45*time.Second, 50*time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("warmup did not stop")
	}
}

func TestPackageNameCollisionsExcludeVariants(t *testing.T) {
	st := testutil.NewFactsStore(t)
	ctx := context.Background()
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(kind,token,full_token,tap,name,version,version_base,raw,raw_hash)
 VALUES ('cask','buzz','buzz','homebrew/cask','Buzz','1','1','{}','fixture'),
 ('cask','buzz@beta','buzz@beta','homebrew/cask','Buzz','1','1','{}','fixture')`).Error)
	p := store.PublicPackage{Kind: "cask", Token: "buzz", Name: "Buzz"}
	require.NoError(t, st.DB.Raw("SELECT id FROM packages WHERE token='buzz'").Scan(&p.ID).Error)
	collision, err := st.PackageNameCollides(ctx, p)
	require.NoError(t, err)
	require.False(t, collision)
	require.NoError(t, st.DB.Exec(`INSERT INTO packages(kind,token,full_token,tap,name,version,version_base,raw,raw_hash)
 VALUES ('link','buzz-chat','buzz-chat','links',' buzz ','1','1','{}','fixture')`).Error)
	collision, err = st.PackageNameCollides(ctx, p)
	require.NoError(t, err)
	require.True(t, collision)
	require.NoError(t, st.DB.Exec("UPDATE packages SET removed_at=now() WHERE token='buzz-chat'").Error)
	collision, err = st.PackageNameCollides(ctx, p)
	require.NoError(t, err)
	require.False(t, collision)
}

func TestBrowseRefreshThrottleAndCrossInstanceLock(t *testing.T) {
	st := testutil.NewFactsStore(t)
	ctx := context.Background()
	backend := cache.Redis{Client: st.Redis}
	first := &catalog.PublicService{Store: st, Cache: backend}
	// Distinct Store pointers simulate independent singleflight groups sharing Redis.
	second := &catalog.PublicService{Store: &store.Store{DB: st.DB, Redis: st.Redis}, Cache: backend}
	require.NoError(t, st.Redis.Set(ctx, "browse:build:apps", "other-instance", time.Minute).Err())
	done := make(chan error, 1)
	go func() { _, err := second.Browse(ctx, "apps", "", "en-US", 1); done <- err }()
	require.Eventually(t, func() bool { return st.Redis.Exists(ctx, "browse:build:apps").Val() == 1 }, time.Second, time.Millisecond)
	// The waiting instance must not query/build while the other one owns the lease.
	require.Never(t, func() bool { return st.Redis.Exists(ctx, "c:browse:v2:apps:fresh").Val() == 1 }, 300*time.Millisecond, 20*time.Millisecond)
	require.NoError(t, st.Redis.Del(ctx, "browse:build:apps").Err())
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(15 * time.Second):
		t.Fatal("cold build did not recover")
	}
	require.NoError(t, cache.Invalidate(ctx, st.Redis, []string{store.BrowseRefreshPattern}))
	_, err := first.Browse(ctx, "apps", "", "en-US", 1)
	require.NoError(t, err)
	require.Eventually(t, func() bool { return st.Redis.Exists(ctx, "c:browse:v2:apps:fresh").Val() == 1 }, 15*time.Second, 20*time.Millisecond)
	require.NoError(t, cache.Invalidate(ctx, st.Redis, []string{store.BrowseRefreshPattern}))
	_, err = second.Browse(ctx, "apps", "", "en-US", 1)
	require.NoError(t, err)
	require.Never(t, func() bool { return st.Redis.Exists(ctx, "c:browse:v2:apps:fresh").Val() == 1 }, 300*time.Millisecond, 20*time.Millisecond)
	// A true cold miss bypasses the stale refresh interval.
	require.NoError(t, cache.Invalidate(ctx, st.Redis, []string{"c:browse:*"}))
	_, err = second.Browse(ctx, "apps", "", "en-US", 1)
	require.NoError(t, err)
	require.EqualValues(t, 1, st.Redis.Exists(ctx, "c:browse:v2:apps:fresh").Val())
}
