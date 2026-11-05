import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { dirname, join } from 'node:path';
import { runInNewContext } from 'node:vm';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createStorage } from 'unstorage';
import memoryDriver from 'unstorage/drivers/memory';
import { boundedCache } from '../server/utils/boundedCache';
import { APP_PAGE_CACHE_CONTROL } from '../server/utils/edgeCache';

// Execute the installed Nitro cache implementation, replacing only its runtime dependencies.
// This catches differences between the response header and actual SWR behavior on upgrades.
const require = createRequire(import.meta.url);
const nuxtRequire = createRequire(require.resolve('nuxt/package.json'));
const nitroRoot = dirname(nuxtRequire.resolve('nitropack/package.json'));
const source = readFileSync(join(nitroRoot, 'dist/runtime/internal/cache.mjs'), 'utf8');
const functionSource = source
  .slice(source.indexOf('function defaultCacheOptions'), source.indexOf('export function cachedFunction'))
  .replace('export function defineCachedFunction', 'function defineCachedFunction');
const epoch = 1800000000000;
const page = { code: 200, headers: { 'cache-control': APP_PAGE_CACHE_CONTROL }, body: 'old HTML' };

function fixture() {
  const storage = createStorage();
  storage.mount('cache', boundedCache(memoryDriver()));
  const errors: unknown[] = [];
  const defineCachedFunction = runInNewContext(`${functionSource}; defineCachedFunction`, {
    Date,
    Promise,
    console: { error: () => {} },
    hash: () => 'fixture-integrity',
    isEvent: () => false,
    useStorage: () => storage,
    useNitroApp: () => ({ captureError: (error: unknown) => errors.push(error) })
  }) as (fn: () => Promise<typeof page>, options: Record<string, unknown>) => () => Promise<typeof page>;
  const render = vi.fn(async () => page);
  const cached = defineCachedFunction(render, {
    base: 'cache',
    group: 'nitro/routes',
    getKey: () => 'app',
    maxAge: 21600,
    staleMaxAge: 300,
    swr: true
  });
  return { cached, render, errors };
}

afterEach(() => vi.useRealTimers());

describe('origin SWR hard deadline', () => {
  it('serves fresh and permitted stale pages, then propagates render failures at the deadline', async () => {
    vi.useFakeTimers({ toFake: ['Date'] });
    vi.setSystemTime(epoch);
    const { cached, render } = fixture();
    expect(await cached()).toEqual(page);
    await new Promise(resolve => setImmediate(resolve));
    render.mockRejectedValue(new Error('origin unavailable'));
    vi.setSystemTime(epoch + 21900 * 1000);
    await expect(cached()).rejects.toThrow('origin unavailable');
    for (const seconds of [43200, 86399]) {
      vi.setSystemTime(epoch + seconds * 1000);
      await expect(cached()).rejects.toThrow('origin unavailable');
    }
    render.mockResolvedValue({ ...page, body: 'new HTML' });
    expect((await cached()).body).toBe('new HTML');
    expect((await cached()).body).toBe('new HTML');
  });

  it('bounds writes and both read paths while preserving the version-history window', async () => {
    vi.useFakeTimers({ toFake: ['Date'] });
    vi.setSystemTime(epoch);
    const inner = memoryDriver();
    const writes = vi.spyOn(inner, 'setItem');
    const cache = boundedCache({
      ...inner,
      getItems: async items => Promise.all(items.map(async ({ key }) => ({ key, value: await inner.getItem(key) })))
    });
    const appKey = 'nitro:routes:_:app.json';
    const versionKey = 'nitro:routes:_:versions.json';
    const entry = JSON.stringify({ mtime: epoch, value: page });
    await cache.setItem!(appKey, entry, {});
    expect(writes.mock.calls[0]?.[2]?.ttl).toBe(21900);
    await cache.setItem!(versionKey, entry.replace('stale-while-revalidate=300', 'stale-while-revalidate=64800'), {});
    expect(writes.mock.calls[1]?.[2]?.ttl).toBe(86400);
    // Legacy uncompressed entries must obey the same deadline, even if the driver ignores TTL.
    await inner.setItem!('nitro:routes:_:legacy.json', entry, {});
    vi.setSystemTime(epoch + 21900 * 1000);
    expect(await cache.getItem(appKey)).toBeNull();
    expect(await cache.getItem('nitro:routes:_:legacy.json')).toBeNull();
    expect(await cache.getItem(versionKey)).not.toBeNull();
    const batch = await cache.getItems!([{ key: appKey }, { key: versionKey }], {});
    expect(batch[0]?.value).toBeNull();
    expect(batch[1]?.value).not.toBeNull();
    vi.setSystemTime(epoch + 86400 * 1000);
    expect(await cache.getItem(versionKey)).toBeNull();
  });
});
