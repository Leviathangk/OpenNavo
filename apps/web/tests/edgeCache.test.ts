import { webRouteType } from '@opennavo/shared';
import { describe, expect, it } from 'vitest';
import {
  edgeCacheHeader,
  edgeRouteType,
  LONG_EDGE_CACHE_VALUE,
  RECENT_EDGE_CACHE_VALUE,
  SHORT_EDGE_CACHE_VALUE,
  type EdgeCacheInput
} from '../server/utils/edgeCache';

const page = (overrides: Partial<EdgeCacheInput> = {}): EdgeCacheInput => ({
  enabled: true,
  method: 'GET',
  path: '/zh/apps/visual-studio-code',
  status: 200,
  contentType: 'text/html;charset=utf-8',
  cacheControl: 's-maxage=600, stale-while-revalidate',
  setCookie: false,
  authorization: false,
  ...overrides
});

describe('HTML edge cache switch', () => {
  it('keeps negotiable HTML on the same edge policy as every other HTML page', () => {
    expect(edgeCacheHeader(page({ path: '/' }))).toBe(SHORT_EDGE_CACHE_VALUE);
    expect(edgeCacheHeader(page({ path: '/apps/iterm2' }))).toBe(LONG_EDGE_CACHE_VALUE);
  });

  it('Keeps purged pages for two days and lists for ten minutes, serving either while the origin errs', () => {
    expect(LONG_EDGE_CACHE_VALUE).toBe('max-age=172800, stale-while-revalidate=604800, stale-if-error=604800');
    expect(SHORT_EDGE_CACHE_VALUE).toBe('max-age=600, stale-while-revalidate=86400, stale-if-error=86400');
  });

  it('Caches purged app and article pages for hours, home and lists for minutes', () => {
    for (const path of [
      '/zh/apps/visual-studio-code',
      '/apps/x/versions',
      '/apps/x/details',
      '/ja/apps/x/dependencies',
      '/zh/articles/choose-a-screenshot-app',
      '/authors/lin-wei'
    ])
      expect(edgeCacheHeader(page({ path })), path).toBe(LONG_EDGE_CACHE_VALUE);
    for (const path of [
      '/',
      '/zh',
      '/ja/categories/browsers',
      '/apps',
      '/articles',
      '/collections/new-mac',
      '/download',
      '/de/security',
      '/security/clamav',
      '/about'
    ])
      expect(edgeCacheHeader(page({ path })), path).toBe(SHORT_EDGE_CACHE_VALUE);
    expect(edgeCacheHeader(page({ method: 'HEAD' }))).toBe(LONG_EDGE_CACHE_VALUE);
  });

  it('Caches Recently Updated like the other lists without changing its analytics label', () => {
    for (const path of ['/recent', '/zh/recent', '/ru/recent', '/recent?page=2', '/ja/recent?page=3&size=12']) {
      expect(edgeRouteType(path), path).toBe('list');
      expect(edgeCacheHeader(page({ path, cacheControl: 's-maxage=300, stale-while-revalidate' })), path).toBe(
        RECENT_EDGE_CACHE_VALUE
      );
    }
    // GA4 page_type and the crawler counters keep reporting it as "other".
    expect(webRouteType('/recent')).toBe('other');
    expect(webRouteType('/zh/recent?page=2')).toBe('other');
    expect(RECENT_EDGE_CACHE_VALUE).toBe('max-age=60, stale-while-revalidate=300, stale-if-error=300');
    for (const status of [404, 429, 500, 503])
      expect(edgeCacheHeader(page({ path: '/zh/recent', status }))).toBe('no-store');
    for (const path of ['/recent/extra', '/xx/recent', '/recentx', '/apps/recent'])
      expect(edgeRouteType(path), path).toBe(webRouteType(path));
    expect(edgeCacheHeader(page({ path: '/recent?page=99999', status: 404 }))).toBe('no-store');
    expect(edgeCacheHeader(page({ path: '/recent/extra' }))).toBe('no-store');
  });

  it('Bypasses private, failed, uncached and not-yet-enabled responses', () => {
    const bypass: Partial<EdgeCacheInput>[] = [
      { enabled: false },
      { method: 'POST' },
      { status: 404 },
      { status: 503 },
      { status: 410, path: '/articles/withdrawn-article' },
      { setCookie: true },
      { authorization: true },
      { cacheControl: 'no-store' },
      { cacheControl: 's-maxage=600, private' },
      { cacheControl: '' },
      { path: '/search' },
      { path: '/feedback' },
      { path: '/apps/x/unknown-tab' }
    ];
    for (const overrides of bypass)
      expect(edgeCacheHeader(page(overrides)), JSON.stringify(overrides)).toBe('no-store');
    expect(edgeCacheHeader(page({ contentType: 'application/json' }))).toBeUndefined();
    // The load-shedding 503 is plain text and already no-store; the edge rule never touches it.
    expect(edgeCacheHeader(page({ status: 503, contentType: 'text/plain; charset=utf-8' }))).toBeUndefined();
  });
});

describe('RSS edge cache', () => {
  const feed = (path: string, overrides: Partial<EdgeCacheInput> = {}) =>
    edgeCacheHeader(
      page({ path, contentType: 'application/rss+xml; charset=utf-8', cacheControl: 's-maxage=600', ...overrides })
    );

  it('Keeps app release feeds as long as app pages and the other feeds as long as lists', () => {
    // Purged with their app: the /apps/{token} prefix covers the feed in every language.
    for (const path of ['/apps/visual-studio-code/feed.xml', '/zh/apps/nextpad++/feed.xml', '/zh-hant/apps/x/feed.xml'])
      expect(feed(path), path).toBe(LONG_EDGE_CACHE_VALUE);
    for (const path of ['/articles/feed.xml', '/ja/articles/feed.xml', '/collections/editors/feed.xml'])
      expect(feed(path), path).toBe(SHORT_EDGE_CACHE_VALUE);
    expect(feed('/zh/collections/editors/feed.xml', { method: 'HEAD' })).toBe(SHORT_EDGE_CACHE_VALUE);
  });

  it('Shares only successful, publicly cached feeds at feed paths', () => {
    const bypass: [string, Partial<EdgeCacheInput>][] = [
      ['/apps/x/feed.xml', { enabled: false }],
      ['/apps/x/feed.xml', { status: 404 }],
      ['/apps/x/feed.xml', { status: 503 }],
      ['/articles/feed.xml', { cacheControl: '' }],
      ['/articles/feed.xml', { cacheControl: 's-maxage=600, private' }],
      ['/articles/feed.xml', { setCookie: true }],
      ['/articles/feed.xml', { method: 'POST' }],
      // RSS anywhere but a feed path, and a feed path answered with HTML, are not feeds.
      ['/apps/x', {}],
      ['/xx/articles/feed.xml', {}]
    ];
    for (const [path, overrides] of bypass)
      expect(feed(path, overrides), `${path} ${JSON.stringify(overrides)}`).toBe('no-store');
    expect(edgeCacheHeader(page({ path: '/apps/x/feed.xml' }))).toBe('no-store');
  });
});
