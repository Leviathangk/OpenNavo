package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/opennavo/opennavo/server/gen/publicapi"
	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/i18n"
	"github.com/opennavo/opennavo/server/internal/store"
	"golang.org/x/sync/singleflight"
)

// browseTTL bounds staleness only for changes nothing invalidates: catalog, translation and SEO
// changes clear c:browse:* as they happen.
const browseTTL = 30 * time.Minute

// browseBuilds lets one directory build per kind serve every concurrent caller. Without it, crawlers
// arriving after an invalidation each rebuilt the whole catalog, and pages took 26–30 s on 2 CPUs.
var browseBuilds singleflight.Group

// browseBuildTimeout bounds a shared build, which no single request's deadline controls: a database wait
// that never ends must not hold every later caller of the same kind.
const browseBuildTimeout = time.Minute

func browseKey(kind, locale, bucket string, page int) string {
	return fmt.Sprintf("c:browse:v2:%s:%s:%s:%d", kind, locale, bucket, page)
}

func (s *PublicService) Browse(ctx context.Context, kind, bucket, locale string, page int) (publicapi.BrowsePage, error) {
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
	if value, ok := s.cachedBrowsePage(ctx, browseKey(kind, locale, bucket, page)); ok {
		return value, nil
	}
	// The language's root lists every bucket and its page count, so a page outside them is a 404
	// without rebuilding the directory.
	if bucket != "" {
		if root, ok := s.cachedBrowsePage(ctx, browseKey(kind, locale, "", 1)); ok && !store.HasBrowsePage(root.Buckets, bucket, page) {
			return publicapi.BrowsePage{}, notFound
		}
	}
	directories, err := s.sharedBrowseBuild(ctx, kind)
	if err != nil {
		return publicapi.BrowsePage{}, err
	}
	return directories[locale].Page(bucket, page)
}

// sharedBrowseBuild joins the kind's running build or starts one. The build outlives a caller that
// disconnects (others may be waiting for it) but stops at browseBuildTimeout; a caller whose own request
// ends stops waiting at once.
func (s *PublicService) sharedBrowseBuild(ctx context.Context, kind string) (map[string]store.BrowseDirectory, error) {
	result := browseBuilds.DoChan(kind, func() (any, error) {
		build, cancel := context.WithTimeout(context.WithoutCancel(ctx), browseBuildTimeout)
		defer cancel()
		return s.buildBrowse(build, kind)
	})
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
	policy, err := s.Store.IndexPolicy(ctx)
	if err != nil {
		return nil, err
	}
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
			_ = s.Cache.Save(ctx, browseKey(kind, locale, bucket, page), data, browseTTL)
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
	return directories, nil
}
