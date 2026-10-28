import { SITE_LOCALES, LOCALE_METADATA } from '@opennavo/shared';

/**
 * Per-route query contract (S4). One table decides, for each page route:
 * - canonical: parameters canonical and alternate URLs keep (page only from page 2 on, defaults dropped);
 * - cache: parameters that select a different response. The server drops every other parameter from the
 *   request before rendering (server/middleware/query-contract.ts), so utm_*, fbclid and the like never
 *   split the route cache or reach a page;
 * - noindexIf: parameters whose non-default value keeps the page out of the index (noindex, follow; the
 *   canonical stays on the page itself).
 * Routes that are not listed take no parameters. The Cloudflare HTML cache key
 * (ops/cloudflare/html-cache-key.json) mirrors the union of `cache`, checked by tests/routeQuery.test.ts,
 * and the SSR tests walk this table.
 */
export interface RouteQueryRule {
  readonly canonical: readonly string[];
  readonly cache: readonly string[];
  readonly noindexIf: readonly string[];
  /** Values that mean "not set": canonical URLs drop them and they never count for noindexIf. */
  readonly defaults?: Readonly<Record<string, string>>;
}

const LIST = { canonical: ['page'], cache: ['page'], noindexIf: [] } as const;
const GRID_FILTERED = {
  canonical: ['page', 'sort', 'disabled'],
  cache: ['page', 'sort', 'disabled'],
  noindexIf: ['sort', 'disabled'],
  defaults: { sort: 'popular' }
} as const;

export const ROUTE_QUERY = {
  '/apps': GRID_FILTERED,
  '/categories/:slug': GRID_FILTERED,
  '/recent': { ...LIST, noindexIf: ['page'], defaults: { page: '1' } },
  '/collections': LIST,
  '/authors/:slug': LIST,
  '/browse/:bucket': LIST,
  '/browse/fonts/:bucket': LIST,
  // F1: the encyclopedia index pages through labels and searches them; a search is a variant.
  '/background-items': { canonical: ['page'], cache: ['page', 'q'], noindexIf: ['q'] },
  '/rankings': {
    canonical: ['page', 'period', 'category'],
    cache: ['page', 'period', 'category'],
    noindexIf: ['period', 'category'],
    defaults: { period: '30d' }
  },
  '/articles': { canonical: ['page', 'type'], cache: ['page', 'type'], noindexIf: ['type'] },
  '/apps/:token/versions': {
    canonical: ['page', 'notes', 'original'],
    cache: ['page', 'notes', 'original'],
    noindexIf: ['notes', 'original']
  },
  // Search is never indexed (robots.txt and the page itself); its query only selects the results.
  '/search': { canonical: [], cache: ['q', 'page'], noindexIf: [] },
  // Prefilled report forms (?type=&kind=&token=&q=) are the same page: the canonical is the bare form.
  '/feedback': {
    canonical: [],
    cache: ['type', 'kind', 'token', 'q'],
    noindexIf: ['type', 'kind', 'token', 'q']
  }
} as const satisfies Record<string, RouteQueryRule>;

export type RouteQueryPattern = keyof typeof ROUTE_QUERY;

const NO_QUERY: RouteQueryRule = { canonical: [], cache: [], noindexIf: [] };
/** Flags are on only as 1 (mergeQuery writes true as 1); any other value means off. */
const FLAGS: ReadonlySet<string> = new Set(['disabled', 'notes', 'original']);

const PREFIXES = SITE_LOCALES.map(locale => LOCALE_METADATA[locale].prefix)
  .filter(Boolean)
  .sort((a, b) => b.length - a.length);

/** Path without its language prefix (/zh-hant/apps → /apps); the root of a language is "/". */
export function withoutLocale(path: string): string {
  const prefix = PREFIXES.find(value => path === value || path.startsWith(`${value}/`));
  return prefix ? path.slice(prefix.length) || '/' : path;
}

const MATCHERS = (Object.keys(ROUTE_QUERY) as RouteQueryPattern[]).map(pattern => ({
  pattern,
  expression: new RegExp(`^${pattern.replace(/:[a-z]+/g, '[^/]+')}$`)
}));

/** The contract of a page path (with or without a language prefix); unlisted routes take no parameters. */
export function routeQueryRule(path: string): {
  pattern: RouteQueryPattern | null;
  rule: RouteQueryRule;
} {
  const bare = withoutLocale(path.split('?', 1)[0] || '/');
  const match = MATCHERS.find(item => item.expression.test(bare));
  return match ? { pattern: match.pattern, rule: ROUTE_QUERY[match.pattern] } : { pattern: null, rule: NO_QUERY };
}

const PAGE_NUMBER = /^[1-9]\d{0,5}$/;

/**
 * The value a parameter keeps in canonical URLs, or undefined when it is absent, repeated, a default, an
 * off flag or page 1. Pages answer invalid values with 404 before a canonical is ever written.
 */
function canonicalValue(rule: RouteQueryRule, key: string, values: string[]): string | undefined {
  if (values.length !== 1) return undefined;
  const value = values[0] ?? '';
  if (key === 'page') return PAGE_NUMBER.test(value) && value !== '1' ? value : undefined;
  if (FLAGS.has(key)) return value === '1' ? '1' : undefined;
  return value && value !== rule.defaults?.[key] ? value : undefined;
}

/** Canonical query of a page, in contract order (?page=2&sort=name&disabled=1), without the "?". */
export function canonicalQuery(path: string, query: URLSearchParams): string {
  const { rule } = routeQueryRule(path);
  const out = new URLSearchParams();
  for (const key of rule.canonical) {
    const value = canonicalValue(rule, key, query.getAll(key));
    if (value !== undefined) out.set(key, value);
  }
  return out.toString();
}

/** Whether a parameter in noindexIf has a non-default value (a sorted, filtered or prefilled variant). */
export function queryNoindex(path: string, query: URLSearchParams): boolean {
  const { rule } = routeQueryRule(path);
  return rule.noindexIf.some(key => {
    const values = query.getAll(key);
    if (!values.length) return false;
    if (FLAGS.has(key)) return values.includes('1');
    return values.some(value => value !== '' && value !== rule.defaults?.[key]);
  });
}

/**
 * Raw query string a page renders and caches with: only the contract's `cache` parameters, kept exactly
 * as written (order, repeats and escapes), so invalid or repeated values still reach the page and 404.
 */
export function cacheQuery(path: string, rawQuery: string): string {
  const { rule } = routeQueryRule(path);
  if (!rawQuery) return '';
  return rawQuery
    .split('&')
    .filter(part => {
      const name = part.split('=', 1)[0] ?? '';
      let key: string;
      try {
        key = decodeURIComponent(name.replace(/\+/g, ' '));
      } catch {
        return false;
      }
      return rule.cache.includes(key);
    })
    .join('&');
}
