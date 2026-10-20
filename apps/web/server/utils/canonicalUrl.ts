import { isValidToken, SITE_LOCALES, LOCALE_METADATA } from '@opennavo/shared';
import { GRID_PAGE_SIZE, isGridListPath } from '../../shared/utils/gridPage';

/** Route codes of the prefixed languages (zh, ja, …). */
const LOCALE_SEGMENTS = new Set(SITE_LOCALES.map(locale => LOCALE_METADATA[locale].prefix.slice(1)).filter(Boolean));
/** Route code of the default language, whose pages live at the root without it (nuxt.config i18n). */
const DEFAULT_SEGMENT = 'en';
/** First path segment of every page route in app/pages; all page routes are lowercase. */
export const PAGE_SECTIONS: ReadonlySet<string> = new Set([
  'about',
  'apps',
  'articles',
  'authors',
  'background-items',
  'brewfile',
  'browse',
  'categories',
  'collections',
  'discover',
  'download',
  'feedback',
  'rankings',
  'recent',
  'search',
  'security'
]);

/** Sections whose entity segment may contain dots: app tokens and background-item labels. */
const DOTTED_SECTIONS: ReadonlySet<string> = new Set(['apps', 'background-items']);

/**
 * Lowercase page path without trailing slashes or the /en prefix, or undefined for anything that is
 * not a page: assets, payloads, API routes, sitemaps, robots.txt and other files keep their URL.
 */
function pagePath(path: string): string | undefined {
  // Percent escapes keep their spelling; the token step below decodes them.
  const lower = path.replace(/%[0-9A-Fa-f]{2}|[^%]+/g, part => (part.startsWith('%') ? part : part.toLowerCase()));
  const segments = lower.split('/').slice(1);
  while (segments.length && segments.at(-1) === '') segments.pop();
  const first = segments[0] ?? '';
  const language = first === DEFAULT_SEGMENT || LOCALE_SEGMENTS.has(first) ? first : undefined;
  const rest = language ? segments.slice(1) : segments;
  const section = rest[0];
  // LaunchAgent/daemon labels are case-sensitive identifiers, unlike route names.
  if (section === 'background-items' && rest[1] !== undefined) {
    rest[1] = path.split('/')[language ? 3 : 2]!;
  }
  if (section !== undefined && !PAGE_SECTIONS.has(section)) return undefined;
  // Payloads (_payload.json) and files such as articles/feed.xml or apps/{token}/feed.xml. App tokens and
  // background-item labels (com.vendor.agent) contain dots, so in those sections only a dot past the
  // entity segment marks a file.
  const last = segments.at(-1) ?? '';
  const dotted = DOTTED_SECTIONS.has(section ?? '') && rest.length <= 2;
  if (last.startsWith('_') || (!dotted && last.includes('.'))) return undefined;
  return `/${[...(language && language !== DEFAULT_SEGMENT ? [language] : []), ...rest].join('/')}`;
}

/** Whether a request path is a page (not an asset, payload, API route, sitemap or other file). */
export const isPagePath = (path: string) => pagePath(path) !== undefined;

/**
 * Homebrew tokens use URL-safe ASCII, including literal + and @. Escaped spellings are decoded before
 * Nuxt's SSR renderer encodes event.path again; otherwise %2B becomes %252B and the package lookup
 * receives a percent sign. This also covers localized tabs and Nuxt payloads.
 */
function tokenPath(path: string): string {
  const parts = path.split('/');
  const appAt = LOCALE_SEGMENTS.has(parts[1] ?? '') ? 2 : 1;
  const escaped = parts[appAt + 1];
  if (parts[appAt] !== 'apps' || !escaped?.includes('%')) return path;
  let token: string;
  try {
    token = decodeURIComponent(escaped);
  } catch {
    return path;
  }
  // Decode exactly once. Encoded slashes, whitespace and nested escapes are not valid tokens.
  if (!isValidToken(token)) return path;
  parts[appAt + 1] = token;
  return parts.join('/');
}

/**
 * Query parameters that only ever produced duplicate URLs: returnTo (S3; the return position now
 * travels in history.state) and a grid list's size (S2; the web has one page size). Both answer in the
 * same hop as the path; a sized page moves to the default-size page that holds the same first app.
 * Every other parameter is kept exactly as written.
 */
function canonicalQuery(path: string, query: string): string {
  if (!/(?:^|&)(?:returnTo|size)=/.test(query)) return query;
  const params = new URLSearchParams(query);
  params.delete('returnTo');
  if (isGridListPath(path) && params.has('size')) {
    const size = Number(params.get('size'));
    const page = Number(params.get('page') ?? '1');
    params.delete('size');
    if (Number.isInteger(size) && size >= 1 && size <= 100 && size !== GRID_PAGE_SIZE) {
      const first = (Number.isInteger(page) && page > 1 ? page - 1 : 0) * size;
      const target = Math.floor(first / GRID_PAGE_SIZE) + 1;
      if (target > 1) params.set('page', String(target));
      else params.delete('page');
    }
  }
  return params.toString();
}

/**
 * Where a request for a page should go in one 301, or undefined when its URL is already the one
 * pages canonicalize to: lowercase, no trailing slash (only the root keeps its slash), no /en prefix
 * for the default language, and package tokens spelled out. The query string is kept as it is,
 * apart from returnTo and grid list sizes (canonicalQuery).
 * Page paths never need escapes once tokens are spelled out, so a path that keeps one is not a page
 * and answers 404 where it is, rather than after a redirect.
 */
export function canonicalRequestUrl(url: string): string | undefined {
  const queryAt = url.indexOf('?');
  const path = queryAt < 0 ? url : url.slice(0, queryAt);
  if (!path.startsWith('/') || path.startsWith('//')) return undefined;
  const target = tokenPath(pagePath(path) ?? path);
  if (target.includes('%')) return undefined;
  const query = queryAt < 0 ? undefined : url.slice(queryAt + 1);
  const clean = query === undefined || pagePath(path) === undefined ? query : canonicalQuery(target, query);
  if (target === path && clean === query) return undefined;
  // A query left empty by the cleanup goes; one that was already empty stays as written (/apps/? → /apps?).
  const suffix = clean === undefined || (clean === '' && query !== '') ? '' : `?${clean}`;
  return target + suffix;
}
