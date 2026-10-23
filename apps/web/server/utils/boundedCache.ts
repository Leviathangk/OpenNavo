import { promisify } from 'node:util';
import { gzip, gunzip } from 'node:zlib';
import type { Driver } from 'unstorage';

export const PAGE_CACHE_TTL_SECONDS = 3600;
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
  return {
    ...driver,
    async getItem(key, options) {
      return decode(namespace + key, await driver.getItem(key, options));
    },
    // Redis exposes MGET; it must decode the same way as individual reads.
    getItems: driver.getItems
      ? async (items, options) =>
          Promise.all(
            (await driver.getItems!(items, options)).map(async ({ key, value }) => ({
              key,
              value: await decode(namespace + key, value)
            }))
          )
      : undefined,
    // Let unstorage dispatch batch writes through the bounded/compressed setItem below.
    setItems: undefined,
    async setItem(key, value, options) {
      if (Buffer.byteLength(value, 'utf8') > MAX_CACHE_ENTRY_BYTES) {
        await driver.removeItem?.(key, options);
        return;
      }
      const ceiling = (namespace + key).startsWith('nuxt-og-image:') ? IMAGE_CACHE_TTL_SECONDS : PAGE_CACHE_TTL_SECONDS;
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
    }
  };
}
