import { webRouteType } from '@opennavo/shared';
import { describe, expect, it } from 'vitest';
import {
  edgeCacheHeader,
  edgeRouteType,
  LONG_EDGE_CACHE_VALUE,
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
    for (const path of ['/', '/zh', '/ja/categories/browsers', '/apps', '/articles'])
      expect(edgeCacheHeader(page({ path })), path).toBe(SHORT_EDGE_CACHE_VALUE);
    expect(edgeCacheHeader(page({ method: 'HEAD' }))).toBe(LONG_EDGE_CACHE_VALUE);
  });

  it('Caches Recently Updated like the other lists without changing its analytics label', () => {
    for (const path of ['/recent', '/zh/recent', '/ru/recent', '/recent?page=2', '/ja/recent?page=3&size=12']) {
      expect(edgeRouteType(path), path).toBe('list');
      expect(edgeCacheHeader(page({ path, cacheControl: 's-maxage=300, stale-while-revalidate' })), path).toBe(
        SHORT_EDGE_CACHE_VALUE
      );
    }
    // GA4 page_type and the crawler counters keep reporting it as "other".
    expect(webRouteType('/recent')).toBe('other');
    expect(webRouteType('/zh/recent?page=2')).toBe('other');
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
      { path: '/collections/new-mac' },
      { path: '/apps/x/unknown-tab' }
    ];
    for (const overrides of bypass)
      expect(edgeCacheHeader(page(overrides)), JSON.stringify(overrides)).toBe('no-store');
    expect(edgeCacheHeader(page({ contentType: 'application/json' }))).toBeUndefined();
  });
});
