import { promisify } from 'node:util';
import { gzip, gunzip } from 'node:zlib';
import type { Driver } from 'unstorage';

// Storage ceiling for cached pages. Version history stays fresh for six hours and stale for eighteen
// (21600 + 64800). App overview pages advertise only five minutes of stale (#237); this retention is
// not that shared-cache window. Releases need no flush: each build caches under its own namespace
// (cache-storage.ts), and allkeys-lru evicts the old one.
export const PAGE_CACHE_TTL_SECONDS = 86400;
export const IMAGE_CACHE_TTL_SECONDS = 6 * 3600;
export const MAX_CACHE_ENTRY_BYTES = 512 * 1024;

const compress = promisify(gzip);
const decompress = promisify(gunzip);
const GZIP_PREFIX = 'gz1:';
const compressible = (key: string) => key.startsWith('nitro:routes:') || key.startsWith('nitro:handlers:');

// Nitro 2.x uses staleMaxAge for headers, but does not stop SWR reads at that age.
// Enforce explicit response lifetimes here for both Redis and the memory fallback.
function responseDeadline(key: string, value: unknown): number | undefined {
  if (!compressible(key) || typeof value !== 'string') return;
  try {
    const entry = JSON.parse(value) as {
      mtime?: number;
      value?: { headers?: Record<string, unknown> };
    } | null;
    const control = entry?.value?.headers?.['cache-control'];
    if (typeof control !== 'string' || typeof entry?.mtime !== 'number' || !Number.isFinite(entry.mtime)) return;
    const fresh = /(?:^|,)\s*s-maxage=(\d+)\s*(?=,|$)/i.exec(control);
    const stale = /(?:^|,)\s*stale-while-revalidate=(\d+)\s*(?=,|$)/i.exec(control);
    if (!fresh || !stale) return;
    const deadline = entry.mtime + (Number(fresh[1]) + Number(stale[1])) * 1000;
    return Number.isFinite(deadline) ? deadline : undefined;
  } catch {
    return;
  }
}

async function decode(key: string, value: Awaited<ReturnType<Driver['getItem']>>) {
  if (!compressible(key)) return value;
  if (typeof value === 'string' && value.startsWith(GZIP_PREFIX)) {
    try {
      value = (
        await decompress(Buffer.from(value.slice(GZIP_PREFIX.length), 'base64'), {
          maxOutputLength: MAX_CACHE_ENTRY_BYTES
        })
      ).toString('utf8');
    } catch {
      // A damaged/oversized compressed value is a miss; Nitro can repopulate the same key.
      return null;
    }
  }
  const deadline = responseDeadline(key, value);
  return deadline !== undefined && Date.now() >= deadline ? null : value;
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
        const deadline = responseDeadline(namespace + key, value);
        if (deadline !== undefined && Date.now() >= deadline) {
          await driver.removeItem?.(key, options);
          return;
        }
        const ceiling = (namespace + key).startsWith('nuxt-og-image:')
          ? IMAGE_CACHE_TTL_SECONDS
          : PAGE_CACHE_TTL_SECONDS;
        const requested = options?.ttl;
        const requestedTtl =
          typeof requested === 'number' && Number.isFinite(requested) && requested > 0
            ? Math.max(1, Math.min(Math.floor(requested), ceiling))
            : ceiling;
        const ttl =
          deadline === undefined ? requestedTtl : Math.min(requestedTtl, Math.ceil((deadline - Date.now()) / 1000));
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
