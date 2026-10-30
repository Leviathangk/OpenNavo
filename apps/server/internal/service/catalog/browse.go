package catalog

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/cache"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/i18n"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
)

// browseTTL bounds staleness only for changes nothing invalidates: catalog and translation changes drop the fresh
// marks (store.BrowseRefreshPattern) and SEO changes clear c:browse:* as they happen. Pages past it, or whose mark was
// dropped, are still served while a background build replaces them.
const browseTTL = 30 * time.Minute

// browseStaleTTL is how long a build's pages stay cached (the cache keeps no entry longer). A request waits for a
// build only when nothing is cached: after c:browse:* is cleared, or when no request read the pages for that long.
// Pages a rebuild no longer has are left to expire; the language's root stops listing them at once.
const browseStaleTTL = time.Hour

// browseBuilds lets one directory build per kind serve every concurrent caller. Without it, crawlers
// arriving after an invalidation each rebuilt the whole catalog, and pages took 26–30 s on 2 CPUs.
var browseBuilds singleflight.Group

// browseRefreshes holds the kinds whose background build is running, so stale reads start at most one each.
var browseRefreshes sync.Map

// browseBuildTimeout bounds a shared build, which no single request's deadline controls: a database wait
// that never ends must not hold every later caller of the same kind.
const browseBuildTimeout = time.Minute

var errBrowseBuiltElsewhere = errors.New("browse rebuilt by another API instance")

func browseKey(kind, locale, bucket string, page int) string {
	return fmt.Sprintf("c:browse:v2:%s:%s:%s:%d", kind, locale, bucket, page)
}

// browseFreshKey holds the time of the kind's last build for browseTTL. A build writes it after its pages, so cached
// pages without it are stale. store.BrowseRefreshPattern matches it.
func browseFreshKey(kind string) string {
	return "c:browse:v2:" + kind + ":fresh"
}

func (s *PublicService) Browse(ctx context.Context, kind, bucket, locale string, page int) (publicapi.BrowsePage, error) {
	scoped := *s
	scoped.Cache = cache.Scope(ctx, s.Cache)
	s = &scoped
	if (kind != "apps" && kind != "fonts") || page < 1 || !i18n.Valid(locale) {
		return publicapi.BrowsePage{}, domain.Validation()
	}
	notFound := &domain.AppError{Code: domain.CodeNotFound, HTTPStatus: 404}
	if bucket != "" && bucket != "0-9" && bucket != "other" && (len(bucket) != 1 || bucket[0] < 'a' || bucket[0] > 'z') {
		return publicapi.BrowsePage{}, notFound
	}
	if bucket == "" {
		page = 1
	}
	// The language's root lists every bucket and its page count, so a page outside them is a 404 without rebuilding
	// the directory, even while an older build's entry for it lingers in the cache.
	if bucket != "" {
		if root, ok := s.cachedBrowsePage(ctx, browseKey(kind, locale, "", 1)); ok && !store.HasBrowsePage(root.Buckets, bucket, page) {
			s.refreshStaleBrowse(ctx, kind)
			return publicapi.BrowsePage{}, notFound
		}
	}
	if value, ok := s.cachedBrowsePage(ctx, browseKey(kind, locale, bucket, page)); ok {
		s.refreshStaleBrowse(ctx, kind)
		return value, nil
	}
	directories, err := s.sharedBrowseBuild(ctx, kind)
	if errors.Is(err, errBrowseBuiltElsewhere) {
		if value, ok := s.cachedBrowsePage(ctx, browseKey(kind, locale, bucket, page)); ok {
			return value, nil
		}
		if bucket != "" {
			if root, ok := s.cachedBrowsePage(ctx, browseKey(kind, locale, "", 1)); ok && !store.HasBrowsePage(root.Buckets, bucket, page) {
				return publicapi.BrowsePage{}, notFound
			}
		}
		// A completion marker is not proof that this slice survived eviction or
		// was written successfully. Build once under the same distributed lock
		// and serve its in-memory result, even if the cache keeps failing writes.
		directories, err = waitBrowseBuild(ctx, s.startBrowseBuild(ctx, kind, true))
	}
	if err != nil {
		return publicapi.BrowsePage{}, err
	}
	return directories[locale].Page(bucket, page)
}

// sharedBrowseBuild waits for the kind's build; a caller whose own request ends stops waiting at once.
func (s *PublicService) sharedBrowseBuild(ctx context.Context, kind string) (map[string]store.BrowseDirectory, error) {
	return waitBrowseBuild(ctx, s.startBrowseBuild(ctx, kind, false))
}

func waitBrowseBuild(ctx context.Context, result <-chan singleflight.Result) (map[string]store.BrowseDirectory, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case done := <-result:
		if done.Err != nil {
			return nil, done.Err
		}
		return done.Val.(map[string]store.BrowseDirectory), nil
	}
}

// startBrowseBuild joins the kind's running build or starts one. The build outlives the caller that started it
// (others may be waiting for it, or it refreshes stale pages in the background) but stops at browseBuildTimeout.
func (s *PublicService) startBrowseBuild(ctx context.Context, kind string, force bool) <-chan singleflight.Result {
	scoped := *s
	scoped.Cache = cache.Scope(ctx, s.Cache)
	s = &scoped
	key := s.browseBuildKey(kind)
	if force {
		key += "|materialize"
	}
	return browseBuilds.DoChan(key, func() (any, error) {
		build, cancel := context.WithTimeout(context.WithoutCancel(ctx), browseBuildTimeout)
		defer cancel()
		if s.Store.Redis == nil || s.Cache == nil {
			return s.buildBrowse(build, kind)
		}
		before, _ := s.Cache.Load(build, browseFreshKey(kind))
		rebuilt := func() bool {
			after, err := s.Cache.Load(build, browseFreshKey(kind))
			return err == nil && !bytes.Equal(before, after)
		}
		key, owner := "browse:build:"+kind, rand.Text()
		waited := false
		for {
			owned, err := s.Store.Redis.SetNX(build, key, owner, browseBuildTimeout+15*time.Second).Result()
			if err != nil {
				// Redis is an optional cache: its outage must not make the catalog unavailable.
				if build.Err() != nil {
					return nil, build.Err()
				}
				return s.buildBrowse(build, kind)
			}
			if owned {
				defer func() {
					cleanup, stop := context.WithTimeout(context.WithoutCancel(build), time.Second)
					defer stop()
					_ = redis.NewScript("if redis.call('get',KEYS[1]) == ARGV[1] then return redis.call('del',KEYS[1]) end return 0").Run(cleanup, s.Store.Redis, []string{key}, owner).Err()
				}()
				if waited && !force {
					if rebuilt() {
						return nil, errBrowseBuiltElsewhere
					}
				}
				return s.buildBrowse(build, kind)
			}
			waited = true
			timer := time.NewTimer(200 * time.Millisecond)
			select {
			case <-build.Done():
				timer.Stop()
				return nil, build.Err()
			case <-timer.C:
			}
			if !force && rebuilt() {
				return nil, errBrowseBuiltElsewhere
			}
		}
	})
}

// browseBuildKey names one kind's build of one store, so a build never serves another database.
func (s *PublicService) browseBuildKey(kind string) string {
	return fmt.Sprintf("%p|%s|%s", s.Store, kind, cache.Namespace(s.Cache))
}

// refreshStaleBrowse starts a background build of the kind once its pages are older than browseTTL. The caller
// keeps the page it read and never waits for the build.
func (s *PublicService) refreshStaleBrowse(ctx context.Context, kind string) {
	// A caller that is gone cannot tell a missing mark from its own canceled read; the next reader decides.
	if _, err := s.Cache.Load(ctx, browseFreshKey(kind)); err == nil || ctx.Err() != nil {
		return
	}
	key := s.browseBuildKey(kind)
	if _, running := browseRefreshes.LoadOrStore(key, true); running {
		return
	}
	owner := rand.Text()
	// Successful stale refreshes are spaced across instances. Failed attempts
	// release only their own reservation so another instance can retry.
	if s.Store != nil && s.Store.Redis != nil {
		allowed, err := s.Store.Redis.SetNX(ctx, "browse:refresh:"+kind, owner, 5*time.Minute).Result()
		if err != nil || !allowed {
			browseRefreshes.Delete(key)
			return
		}
	}
	result := s.startBrowseBuild(ctx, kind, false)
	go func() {
		defer browseRefreshes.Delete(key)
		if done := <-result; done.Err != nil && !errors.Is(done.Err, errBrowseBuiltElsewhere) {
			if s.Store != nil && s.Store.Redis != nil {
				cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
				defer stop()
				_ = redis.NewScript("if redis.call('get',KEYS[1]) == ARGV[1] then return redis.call('del',KEYS[1]) end return 0").Run(cleanup, s.Store.Redis, []string{"browse:refresh:" + kind}, owner).Err()
			}
			slog.WarnContext(ctx, "browse refresh failed", "kind", kind, "error", done.Err)
		}
	}()
}

func (s *PublicService) cachedBrowsePage(ctx context.Context, key string) (publicapi.BrowsePage, bool) {
	var value publicapi.BrowsePage
	if s.Cache == nil {
		return value, false
	}
	data, err := s.Cache.Load(ctx, key)
	return value, err == nil && json.Unmarshal(data, &value) == nil
}

// buildBrowse reads the catalog once for every site language and stores each language's root and pages,
// keeping individual entries below the cache's size limit even when the whole catalog exceeds it.
func (s *PublicService) buildBrowse(ctx context.Context, kind string) (map[string]store.BrowseDirectory, error) {
	scoped := *s
	scoped.Cache = cache.Scope(ctx, s.Cache)
	s = &scoped
	policy, err := s.Store.IndexPolicy(ctx)
	if err != nil {
		return nil, err
	}
	// A language closed for indexing (pages.openLocales) keeps its directory: the web still renders it, with noindex,
	// so it lists the apps it would list if the language were open.
	policy.OpenLocales = nil
	locales := make([]string, len(i18n.SiteLocales))
	for i, locale := range i18n.SiteLocales {
		locales[i] = string(locale)
	}
	directories, err := s.Store.BrowseDirectories(ctx, kind, locales, policy)
	if err != nil {
		return nil, err
	}
	if s.Cache == nil {
		return directories, nil
	}
	save := func(directory store.BrowseDirectory, locale, bucket string, page int) {
		value, err := directory.Page(bucket, page)
		if err != nil {
			return
		}
		if data, err := json.Marshal(value); err == nil {
			_ = s.Cache.Save(ctx, browseKey(kind, locale, bucket, page), data, browseStaleTTL)
		}
	}
	for locale, directory := range directories {
		for _, b := range directory.Buckets {
			for p := 1; p <= b.Pages; p++ {
				save(directory, locale, b.Key, p)
			}
		}
		save(directory, locale, "", 1)
	}
	_ = s.Cache.Save(ctx, browseFreshKey(kind), []byte(rand.Text()), browseTTL)
	return directories, nil
}

// WarmBrowse keeps directory caches ready even when no visitor has requested them.
// Builds run sequentially to avoid competing for CPU on small origins. The shared
// build group also joins requests that arrive during a warmup.
func (s *PublicService) WarmBrowse(ctx context.Context) {
	if s.Cache == nil {
		return
	}
	timer := time.NewTicker(30 * time.Second)
	defer timer.Stop()
	for {
		for _, kind := range []string{"apps", "fonts"} {
			if ctx.Err() != nil {
				return
			}
			missing := false
			for _, locale := range i18n.SiteLocales {
				if _, ok := s.cachedBrowsePage(ctx, browseKey(kind, string(locale), "", 1)); !ok {
					missing = true
					break
				}
			}
			if !missing {
				continue
			}
			if _, err := s.sharedBrowseBuild(ctx, kind); err != nil && !errors.Is(err, errBrowseBuiltElsewhere) && ctx.Err() == nil {
				slog.WarnContext(ctx, "browse warmup failed", "kind", kind, "error", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}
