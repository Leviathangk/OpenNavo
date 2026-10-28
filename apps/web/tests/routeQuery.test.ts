import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import {
  cacheQuery,
  canonicalQuery,
  queryNoindex,
  ROUTE_QUERY,
  routeQueryRule,
  withoutLocale
} from '../shared/utils/routeQuery';

const params = (query: string) => new URLSearchParams(query);

describe('Route query contract (S4)', () => {
  it('matches page paths with or without a language prefix', () => {
    expect(withoutLocale('/zh-hant/apps')).toBe('/apps');
    expect(withoutLocale('/zh/apps')).toBe('/apps');
    expect(withoutLocale('/de')).toBe('/');
    expect(withoutLocale('/apps/zh')).toBe('/apps/zh');
    expect(routeQueryRule('/apps').pattern).toBe('/apps');
    expect(routeQueryRule('/ja/categories/utilities').pattern).toBe('/categories/:slug');
    expect(routeQueryRule('/zh-hant/apps/visual-studio-code/versions').pattern).toBe('/apps/:token/versions');
    expect(routeQueryRule('/fr/browse/a').pattern).toBe('/browse/:bucket');
    for (const path of ['/', '/apps/x', '/apps/x/details', '/download', '/categories']) {
      expect(routeQueryRule(path).pattern, path).toBeNull();
      expect(routeQueryRule(path).rule.cache, path).toEqual([]);
    }
  });

  it('keeps canonical parameters in contract order, without defaults, off flags or page 1', () => {
    const cases: [string, string, string][] = [
      ['/apps', 'sort=popular&page=1', ''],
      ['/apps', 'disabled=1&utm_source=x&sort=name&page=2', 'page=2&sort=name&disabled=1'],
      ['/zh/categories/utilities', 'disabled=0&page=3', 'page=3'],
      ['/apps', 'page=2&page=3', ''],
      ['/rankings', 'period=30d&category=developer-tools', 'category=developer-tools'],
      ['/articles', 'type=tutorial&page=1', 'type=tutorial'],
      ['/apps/nextpad++/versions', 'original=1&notes=true&page=2', 'page=2&original=1'],
      ['/feedback', 'type=bug&q=crash', ''],
      ['/', 'ref=x&page=2', ''],
      ['/apps/wechat', 'page=3', '']
    ];
    for (const [path, query, expected] of cases)
      expect(canonicalQuery(path, params(query)), `${path}?${query}`).toBe(expected);
  });

  it('keeps sorted, filtered and prefilled variants out of the index', () => {
    const indexed = [
      '/apps?sort=popular',
      '/apps?disabled=0',
      '/recent?page=1',
      '/categories/utilities?page=2',
      '/rankings?period=30d',
      '/download?sort=name'
    ];
    const variants = [
      '/recent?page=2',
      '/zh/recent?page=6',
      '/apps?sort=name',
      '/zh/apps?disabled=1',
      '/categories/utilities?sort=updated',
      '/rankings?period=90d',
      '/rankings?category=browsers',
      '/articles?type=review',
      '/apps/x/versions?notes=1',
      '/apps/x/versions?original=1',
      '/feedback?q=crash'
    ];
    for (const url of indexed) expect(queryNoindex(url.split('?')[0]!, params(url.split('?')[1]!)), url).toBe(false);
    for (const url of variants) expect(queryNoindex(url.split('?')[0]!, params(url.split('?')[1]!)), url).toBe(true);
  });

  it('renders and caches with only the contract parameters, kept exactly as written', () => {
    expect(cacheQuery('/apps', 'utm_source=x&page=2&fbclid=y')).toBe('page=2');
    expect(cacheQuery('/apps', 'page=2&page=3')).toBe('page=2&page=3');
    expect(cacheQuery('/search', 'q=a%2Bb&utm_medium=1')).toBe('q=a%2Bb');
    expect(cacheQuery('/', 'ref=x')).toBe('');
    expect(cacheQuery('/apps/x', 'returnTo=%2Frecent')).toBe('');
    expect(cacheQuery('/feedback', 'type=bug&kind=cask&token=x&q=hi&gclid=1')).toBe('type=bug&kind=cask&token=x&q=hi');
    expect(cacheQuery('/apps', 'bad%ZZ=1&page=2')).toBe('page=2');
  });

  it('matches the Cloudflare HTML cache key mirrored in ops/cloudflare/html-cache-key.json', () => {
    const mirror = JSON.parse(
      readFileSync(new URL('../../../ops/cloudflare/html-cache-key.json', import.meta.url), 'utf8')
    );
    const union = [...new Set(Object.values(ROUTE_QUERY).flatMap(rule => [...rule.cache]))].sort();
    expect(mirror.queryString.include).toEqual(union);
  });
});
