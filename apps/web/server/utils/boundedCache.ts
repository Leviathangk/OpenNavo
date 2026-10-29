import { promisify } from 'node:util';
import { gzip, gunzip } from 'node:zlib';
import type { Driver } from 'unstorage';

// Long enough for the six-hour app page rule to serve stale while it refreshes. Releases need no flush:
// each build caches under its own namespace (cache-storage.ts), and allkeys-lru evicts the old one.
export const PAGE_CACHE_TTL_SECONDS = 86400;
export const IMAGE_CACHE_TTL_SECONDS = 6 * 3600;
export const MAX_CACHE_ENTRY_BYTES = 512 * 1024;

const compress = promisify(gzip);
const decompress = promisify(gunzip);
const GZIP_PREFIX = 'gz1:';
const compressible = (key: string) => key.startsWith('nitro:routes:') || key.startsWith('nitro:handlers:');

async function decode(key: string, value: Awaited<ReturnType<Driver['getItem']>>) {
  if (!compressible(key) || typeof value !== 'string' || !value.startsWith(GZIP_PREFIX)) return value;
  try {
    return (
      await decompress(Buffer.from(value.slice(GZIP_PREFIX.length), 'base64'), {
        maxOutputLength: MAX_CACHE_ENTRY_BYTES
      })
    ).toString('utf8');
  } catch {
    // A damaged/oversized compressed value is a miss; Nitro can repopulate the same key.
    return null;
  }
}

// Bound actual Redis retention, including callers that supply their own TTL.
export function boundedCache(driver: Driver, namespace = ''): Driver {
  // Nitro releases render deduplication before its background storage write finishes. Register each
  // mutation before gzip can yield, so reads and purges also account for writes still being compressed.
  const pending = new Map<string, Promise<void>>();
  const mutate = (key: string, operation: () => void | Promise<void>) => {
    const result = (pending.get(key) ?? Promise.resolve()).then(operation);
    // Return write errors to the caller without poisoning later reads, purges or queued mutations.
    const settled = result.then(
      () => undefined,
      () => undefined
    );
    pending.set(key, settled);
    void settled.then(() => {
      if (pending.get(key) === settled) pending.delete(key);
    });
    return result;
  };
  return {
    ...driver,
    async hasItem(key, options) {
      await pending.get(key);
      return driver.hasItem(key, options);
    },
    async getItem(key, options) {
      await pending.get(key);
      return decode(namespace + key, await driver.getItem(key, options));
    },
    // Redis exposes MGET; it must decode the same way as individual reads.
    getItems: driver.getItems
      ? async (items, options) => {
          await Promise.all(items.map(({ key }) => pending.get(key)));
          return Promise.all(
            (await driver.getItems!(items, options)).map(async ({ key, value }) => ({
              key,
              value: await decode(namespace + key, value)
            }))
          );
        }
      : undefined,
    async getKeys(base = '', options) {
      await Promise.all([...pending].filter(([key]) => key.startsWith(base)).map(([, settled]) => settled));
      return driver.getKeys(base, options);
    },
    // Let unstorage dispatch batch writes through the bounded/compressed setItem below.
    setItems: undefined,
    setItem(key, value, options) {
      return mutate(key, async () => {
        if (Buffer.byteLength(value, 'utf8') > MAX_CACHE_ENTRY_BYTES) {
          await driver.removeItem?.(key, options);
          return;
        }
        const ceiling = (namespace + key).startsWith('nuxt-og-image:')
          ? IMAGE_CACHE_TTL_SECONDS
          : PAGE_CACHE_TTL_SECONDS;
        const requested = options?.ttl;
        const ttl =
          typeof requested === 'number' && Number.isFinite(requested) && requested > 0
            ? Math.max(1, Math.min(Math.floor(requested), ceiling))
            : ceiling;
        // Level 1 favors CPU headroom. Async zlib runs in the worker pool, not on the SSR thread.
        // Keep small/incompressible values as-is; readers accept both formats during migration.
        let stored = value;
        if (compressible(key)) {
          const encoded = GZIP_PREFIX + (await compress(value, { level: 1 })).toString('base64');
          if (Buffer.byteLength(encoded) < Buffer.byteLength(value)) stored = encoded;
        }
        await driver.setItem?.(key, stored, { ...options, ttl });
      });
    },
    removeItem(key, options) {
      // A purge (or oversized replacement above) must finish after older writes, never before them.
      return mutate(key, () => driver.removeItem?.(key, options));
    }
  };
}
