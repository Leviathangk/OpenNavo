const OG_CACHE_MOUNT = 'cache:nuxt-og-image:';

// unstorage normalizes both separators and repeated colons, so share-image keys are colon-separated.
function normalizeCacheName(value: string): string {
  return value
    .split('?', 1)[0]!
    .replace(/[/\\]/g, ':')
    .replace(/:+/g, ':')
    .replace(/^:|:$/g, '');
}

interface OgCacheStorage {
  getKeys: (base?: string) => Promise<string[]>;
  removeItem: (key: string) => Promise<void>;
}

/**
 * Matches share-image cache keys by page path. nuxt-og-image keys an image as
 * "<page path>:<options hash>[:<component hash>]", so a path also covers its subpages.
 */
export function ogCacheMatcher(paths: string[]): (key: string) => boolean {
  const prefixes = new Set<string>();
  for (const path of paths) {
    const pathname = path.split('?', 1)[0] || '/';
    let decoded: string;
    try {
      decoded = decodeURI(pathname);
    } catch {
      continue;
    }
    prefixes.add(normalizeCacheName(decoded) || 'index');
  }
  return key => {
    const name = normalizeCacheName(key.startsWith(OG_CACHE_MOUNT) ? key.slice(OG_CACHE_MOUNT.length) : key);
    for (const prefix of prefixes) {
      // nuxt-og-image prefixes the key with its version, so the page path can start after a colon.
      if (
        name === prefix ||
        name.startsWith(`${prefix}:`) ||
        name.includes(`:${prefix}:`) ||
        name.endsWith(`:${prefix}`)
      )
        return true;
    }
    return false;
  };
}

/** Removes the share images of the changed page paths, only when OG invalidation was requested. */
export async function clearOgImageCache(
  storage: OgCacheStorage,
  paths: string[],
  og: boolean
): Promise<number> {
  if (!og || !paths.length) return 0;
  const matches = ogCacheMatcher(paths);
  let removed = 0;
  for (const key of await storage.getKeys()) {
    if (!matches(key)) continue;
    await storage.removeItem(key);
    removed += 1;
  }
  return removed;
}
