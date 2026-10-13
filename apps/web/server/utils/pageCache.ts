import { timingSafeEqual } from 'node:crypto';

/** Namespace of Nitro's route caches in the cache storage. */
export const PAGE_CACHE_BASE = 'nitro:routes:_';

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
