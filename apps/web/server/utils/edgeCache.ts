import { webRouteType } from '@opennavo/shared';

// HTML edge caching (WP5) is controlled per response with Cloudflare-CDN-Cache-Control, which only
// Cloudflare reads: browsers keep the normal Cache-Control. The admin switch turns it off at once
// (no-store), even while a Cloudflare cache rule makes HTML eligible.

/** Page types cached at the edge first; the rest stays at the origin until this list grows. */
export const EDGE_CACHE_ROUTES = new Set([
  'home',
  'list',
  'category',
  'app',
  'app_versions',
  'article_list',
  'article',
  'author'
]);
/**
 * Pages the Go URL change feed purges by prefix when their app or article changes, and again after
 * every web release (the /apps, /articles and /authors prefixes in urlnotify), so crawlers' long
 * tail can be answered at the edge for hours. Keep both lists in step.
 */
export const LONG_EDGE_CACHE_ROUTES = new Set(['app', 'app_versions', 'article', 'author']);
export const LONG_EDGE_CACHE_VALUE = 'max-age=21600, stale-while-revalidate=86400';
/** Home and lists change with every catalog sync and are never purged, so they expire quickly. */
export const SHORT_EDGE_CACHE_VALUE = 'max-age=600, stale-while-revalidate=3600';

export interface EdgeCacheInput {
  enabled: boolean;
  method: string;
  path: string;
  status: number;
  contentType: string;
  cacheControl: string;
  setCookie: boolean;
  authorization: boolean;
}

/** The Cloudflare-CDN-Cache-Control value for an HTML response, or undefined for other responses. */
export function edgeCacheHeader(input: EdgeCacheInput): string | undefined {
  if (!input.contentType.includes('text/html')) return undefined;
  const shareable =
    input.enabled &&
    (input.method === 'GET' || input.method === 'HEAD') &&
    input.status === 200 &&
    !input.setCookie &&
    !input.authorization &&
    // Only pages the origin already caches as shared (SWR route rules set s-maxage); search and
    // collections send no-store and stay uncached.
    /\bs-maxage=\d+/.test(input.cacheControl) &&
    !/\b(no-store|private|no-cache)\b/.test(input.cacheControl) &&
    EDGE_CACHE_ROUTES.has(webRouteType(input.path));
  if (!shareable) return 'no-store';
  return LONG_EDGE_CACHE_ROUTES.has(webRouteType(input.path)) ? LONG_EDGE_CACHE_VALUE : SHORT_EDGE_CACHE_VALUE;
}
