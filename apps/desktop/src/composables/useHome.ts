import { unwrap } from '@opennavo/api';
import { rotateDaily, utcDay } from '@opennavo/shared';
import type { HomeData, PackageSummary } from '@opennavo/api';
import { api } from '@/api';
import { resourceCache } from './resourceCache';

const cached = resourceCache<HomeData>();
const popularCached = resourceCache<PackageSummary[]>();
const recentCached = resourceCache<{ total: number; records: PackageSummary[] }>();
export const RECENT_PAGE_SIZE = 24;
export const peekHome = (locale: string) => cached.peek(locale);
const popularKey = (locale: string) => `${locale}:${utcDay()}`;
export const peekPopularApps = (locale: string) => popularCached.peek(popularKey(locale));
export const peekRecentApps = (locale: string, page: number, size = RECENT_PAGE_SIZE) =>
  recentCached.peek(`${locale}:${page}:${size}`);

// Fetch the same 100-item candidate pool as the server, then expose the current
// twelve-item daily window for the three Discover rows.
export function fetchPopularApps(locale: string): Promise<PackageSummary[]> {
  return popularCached(popularKey(locale), async () => {
    const page = await unwrap(
      api.GET('/packages', {
        params: { query: { sort: 'popular', size: 100, includeFonts: true } },
        headers: { 'Accept-Language': locale }
      })
    );
    return rotateDaily(page.records, 12, 100);
  });
}

// Recently Updated page: the newest-version-first list that Discover's section previews, one page at a time.
export function fetchRecentApps(
  locale: string,
  page: number,
  size = RECENT_PAGE_SIZE
): Promise<{ total: number; records: PackageSummary[] }> {
  return recentCached(`${locale}:${page}:${size}`, async () => {
    const result = await unwrap(
      api.GET('/packages', {
        params: { query: { kind: 'cask', sort: 'updated', current: page, size, includeFonts: true } },
        headers: { 'Accept-Language': locale }
      })
    );
    return { total: result.total, records: result.records };
  });
}

export function fetchHome(locale: string): Promise<HomeData> {
  return cached(locale, () => unwrap(api.GET('/home', { headers: { 'Accept-Language': locale } })));
}

// Discover needs more than the short home preview to fill three responsive rows.
const recentPreviewCached = resourceCache<PackageSummary[]>();
export const peekRecentPreview = (locale: string) => recentPreviewCached.peek(locale);
export function fetchRecentPreview(locale: string): Promise<PackageSummary[]> {
  return recentPreviewCached(locale, async () => {
    const page = await unwrap(
      api.GET('/packages', {
        params: { query: { kind: 'cask', sort: 'updated', size: 100, includeFonts: true } },
        headers: { 'Accept-Language': locale }
      })
    );
    return page.records;
  });
}
