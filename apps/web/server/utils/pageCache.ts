import { timingSafeEqual } from 'node:crypto';
import type { H3Event } from 'h3';

/** Namespace of Nitro's route caches in the cache storage. */
export const PAGE_CACHE_BASE = 'nitro:routes:_';

/** Answers that mean the page itself is gone; outages (5xx) keep serving the cached copy. */
export const GONE_STATUSES: ReadonlySet<number> = new Set([404, 410]);

interface RouteCacheOptions {
  group?: string;
  getKey?: (event: H3Event) => string | Promise<string>;
}

/**
 * Key, in the cache storage, of the route cache entry a request was answered from. Nitro's cached
 * route handler leaves its options on event.context.cache, including the getKey it stores entries
 * under; requests outside a route cache have none.
 */
export async function routeCacheKey(event: H3Event): Promise<string | undefined> {
  const options = (event.context.cache as { options?: RouteCacheOptions } | undefined)?.options;
  if (options?.group !== 'nitro/routes' || typeof options.getKey !== 'function') return undefined;
  return `${PAGE_CACHE_BASE}:${await options.getKey(event)}.json`;
}

/**
 * Nitro keys route caches as <first 16 word characters of the path>.<hash of the full URL>.json.
 * Matching on that prefix clears a page, its descendants, query variants and payloads.
 * Separators are lost in these keys, so this can also refresh unrelated paths sharing the prefix.
 * This harmless over-invalidation is necessary to avoid leaving stale child pages behind.
 */
export function pageCacheMatcher(paths: string[]): (key: string) => boolean {
  const prefixes = new Set<string>();
  for (const path of paths) {
    const pathname = path.split('?', 1)[0] || '/';
    let escaped: string;
    try {
      escaped = decodeURI(pathname).replace(/\W/g, '').slice(0, 16) || 'index';
    } catch {
      continue;
    }
    prefixes.add(escaped);
  }
  return key => {
    const name = key.startsWith(`${PAGE_CACHE_BASE}:`) ? key.slice(PAGE_CACHE_BASE.length + 1) : key;
    for (const prefix of prefixes) if (name.startsWith(prefix)) return true;
    return false;
  };
}

export function authorized(header: string | undefined, secret: string): boolean {
  if (!secret || !header?.startsWith('Bearer ')) return false;
  const given = Buffer.from(header.slice(7));
  const expected = Buffer.from(secret);
  return given.length === expected.length && timingSafeEqual(given, expected);
}
