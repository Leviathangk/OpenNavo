import { unwrap } from '@opennavo/api';
import type { HomeData, PackageSummary } from '@opennavo/api';
import { api } from '@/api';
import { resourceCache } from './resourceCache';

const cached = resourceCache<HomeData>();
const popularCached = resourceCache<PackageSummary[]>();
const recentCached = resourceCache<{ total: number; records: PackageSummary[] }>();
export const RECENT_PAGE_SIZE = 24;
export const peekHome = (locale: string) => cached.peek(locale);
export const peekPopularApps = (locale: string) => popularCached.peek(locale);
export const peekRecentApps = (locale: string, page: number) => recentCached.peek(`${locale}:${page}`);

// The home endpoint only provides twelve apps; fetch enough for three rows in wide desktop windows.
export function fetchPopularApps(locale: string): Promise<PackageSummary[]> {
  return popularCached(locale, async () => {
    const page = await unwrap(
      api.GET('/packages', {
        params: { query: { kind: 'cask', sort: 'popular', size: 30, includeFonts: true } },
        headers: { 'Accept-Language': locale }
      })
    );
    return page.records;
  });
}

// Recently Updated page: the newest-version-first list that Discover's section previews, one page at a time.
export function fetchRecentApps(locale: string, page: number): Promise<{ total: number; records: PackageSummary[] }> {
  return recentCached(`${locale}:${page}`, async () => {
    const result = await unwrap(
      api.GET('/packages', {
        params: { query: { kind: 'cask', sort: 'updated', current: page, size: RECENT_PAGE_SIZE, includeFonts: true } },
        headers: { 'Accept-Language': locale }
      })
    );
    return { total: result.total, records: result.records };
  });
}

export function fetchHome(locale: string): Promise<HomeData> {
  return cached(locale, () => unwrap(api.GET('/home', { headers: { 'Accept-Language': locale } })));
}
