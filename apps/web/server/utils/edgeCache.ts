import { LOCALE_METADATA, webRouteType } from '@opennavo/shared';

// HTML and RSS edge caching (WP5) is controlled per response with Cloudflare-CDN-Cache-Control, which
// only Cloudflare reads: browsers keep the normal Cache-Control. The admin switch turns it off at once
// (no-store), even while a Cloudflare cache rule makes every path eligible.

/**
 * Recently Updated in every language. It is a list like /apps, but webRouteType reports it as
 * "other", and that label stays: GA4 page_type and the crawler counters are keyed on it.
 */
const RECENT_PATHS = new Set(Object.values(LOCALE_METADATA).map(({ prefix }) => `${prefix}/recent`));

/** Page type for edge caching: webRouteType, except that Recently Updated is cached as a list. */
export function edgeRouteType(path: string): string {
  return RECENT_PATHS.has(path.split('?', 1)[0] ?? '') ? 'list' : webRouteType(path);
}

/**
 * Page types cached at the edge; the rest stays at the origin. The download, security, about and collection pages
 * change with a release or an edit at most, so the short lifetime covers them without a purge.
 */
export const EDGE_CACHE_ROUTES = new Set([
  'home',
  'list',
  'category',
  'collection',
  'app',
  'app_versions',
  'app_details',
  'app_dependencies',
  'article_list',
  'article',
  'author',
  'download',
  'security',
  'security_topic',
  'about'
]);
/**
 * Pages that keep the two-day edge lifetime: version history (which changes rarely), articles and
 * authors. App overview tabs use APP_EDGE_CACHE_VALUE below so a correction never outlives the
 * origin's six-hour rule. The Go change feed purges these prefixes on content changes and releases.
 */
export const LONG_EDGE_CACHE_ROUTES = new Set(['app_versions', 'article', 'author']);
/** App overview tabs match the origin's six-hour s-maxage; only version history keeps the longer edge life. */
export const APP_EDGE_CACHE_ROUTES = new Set(['app', 'app_details', 'app_dependencies']);
// stale-if-error lets Cloudflare answer from its copy while the origin sheds load: the shed 503 itself is
// no-store (loadShed.ts), so it never replaces the cached page.
export const LONG_EDGE_CACHE_VALUE = 'max-age=172800, stale-while-revalidate=604800, stale-if-error=604800';
export const APP_EDGE_CACHE_VALUE = 'max-age=21600, stale-while-revalidate=86400, stale-if-error=86400';
/** Home and other lists expire quickly. Recently Updated also participates in content purges. */
export const SHORT_EDGE_CACHE_VALUE = 'max-age=600, stale-while-revalidate=86400, stale-if-error=86400';
export const RECENT_EDGE_CACHE_VALUE = 'max-age=60, stale-while-revalidate=300, stale-if-error=300';

/** An app's release feed sits under /apps/{token}, which the change feed purges with the app. */
const APP_FEED_PATH = /^(?:\/[a-z-]+)?\/apps\/[^/]+\/feed\.xml$/;

/** Responses the edge may cache: HTML pages and RSS feeds. */
export function edgeCacheable(contentType: string): boolean {
  return contentType.includes('text/html') || contentType.includes('application/rss+xml');
}

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

/**
 * The Cloudflare-CDN-Cache-Control value for an HTML or RSS response, or undefined for other responses.
 * App release feeds stay as long as app pages; the article and collection feeds expire like lists.
 */
export function edgeCacheHeader(input: EdgeCacheInput): string | undefined {
  if (!edgeCacheable(input.contentType)) return undefined;
  const feed = input.contentType.includes('application/rss+xml');
  const type = edgeRouteType(input.path);
  const shareable =
    input.enabled &&
    (input.method === 'GET' || input.method === 'HEAD') &&
    input.status === 200 &&
    !input.setCookie &&
    !input.authorization &&
    // Only pages the origin already caches as shared (SWR route rules set s-maxage); search sends
    // no-store and stays uncached.
    /\bs-maxage=\d+/.test(input.cacheControl) &&
    !/\b(no-store|private|no-cache)\b/.test(input.cacheControl) &&
    (feed ? type === 'feed' : EDGE_CACHE_ROUTES.has(type));
  if (!shareable) return 'no-store';
  if (!feed && RECENT_PATHS.has(input.path.split('?', 1)[0] ?? '')) return RECENT_EDGE_CACHE_VALUE;
  if (feed)
    return APP_FEED_PATH.test(input.path.split('?', 1)[0] ?? '') ? LONG_EDGE_CACHE_VALUE : SHORT_EDGE_CACHE_VALUE;
  if (LONG_EDGE_CACHE_ROUTES.has(type)) return LONG_EDGE_CACHE_VALUE;
  if (APP_EDGE_CACHE_ROUTES.has(type)) return APP_EDGE_CACHE_VALUE;
  return SHORT_EDGE_CACHE_VALUE;
}
