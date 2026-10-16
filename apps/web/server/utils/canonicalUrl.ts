import { isValidToken, LOCALES, LOCALE_METADATA } from '@opennavo/shared';

/** Route codes of the prefixed languages (zh, ja, …). */
const LOCALE_SEGMENTS = new Set(LOCALES.map(locale => LOCALE_METADATA[locale].prefix.slice(1)).filter(Boolean));
/** Route code of the default language, whose pages live at the root without it (nuxt.config i18n). */
const DEFAULT_SEGMENT = 'en';
/** First path segment of every page route in app/pages; all page routes are lowercase. */
export const PAGE_SECTIONS: ReadonlySet<string> = new Set([
  'about',
  'apps',
  'articles',
  'authors',
  'brewfile',
  'categories',
  'collections',
  'discover',
  'download',
  'feedback',
  'rankings',
  'recent',
  'search'
]);

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
  if (section !== undefined && !PAGE_SECTIONS.has(section)) return undefined;
  // Payloads (_payload.json) and files such as articles/feed.xml; tokens may contain dots.
  const last = segments.at(-1) ?? '';
  if (last.startsWith('_') || (section !== 'apps' && last.includes('.'))) return undefined;
  return `/${[...(language && language !== DEFAULT_SEGMENT ? [language] : []), ...rest].join('/')}`;
}

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
 * Where a request for a page should go in one 301, or undefined when its URL is already the one
 * pages canonicalize to: lowercase, no trailing slash (only the root keeps its slash), no /en prefix
 * for the default language, and package tokens spelled out. The query string is kept as it is.
 * Page paths never need escapes once tokens are spelled out, so a path that keeps one is not a page
 * and answers 404 where it is, rather than after a redirect.
 */
export function canonicalRequestUrl(url: string): string | undefined {
  const queryAt = url.indexOf('?');
  const path = queryAt < 0 ? url : url.slice(0, queryAt);
  if (!path.startsWith('/') || path.startsWith('//')) return undefined;
  const target = tokenPath(pagePath(path) ?? path);
  if (target === path || target.includes('%')) return undefined;
  return target + (queryAt < 0 ? '' : url.slice(queryAt));
}
