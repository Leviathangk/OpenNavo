import { describe, expect, it } from 'vitest';
import { EDGE_CACHE_VALUE, edgeCacheHeader, type EdgeCacheInput } from '../server/utils/edgeCache';

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
  it('Caches public app, list and home pages while the switch is on', () => {
    for (const path of [
      '/zh/apps/visual-studio-code',
      '/apps/x/versions',
      '/',
      '/ja/categories/browsers',
      '/apps',
      '/articles',
      '/zh/articles/choose-a-screenshot-app',
      '/authors/lin-wei'
    ])
      expect(edgeCacheHeader(page({ path })), path).toBe(EDGE_CACHE_VALUE);
    expect(edgeCacheHeader(page({ method: 'HEAD' }))).toBe(EDGE_CACHE_VALUE);
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
      { path: '/apps/x/details' }
    ];
    for (const overrides of bypass)
      expect(edgeCacheHeader(page(overrides)), JSON.stringify(overrides)).toBe('no-store');
    expect(edgeCacheHeader(page({ contentType: 'application/json' }))).toBeUndefined();
  });
});
