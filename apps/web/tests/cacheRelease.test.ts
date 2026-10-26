import { afterEach, expect, it, vi } from 'vitest';
import { createStorage, type Driver } from 'unstorage';

// One Redis behind every web container: keys live under the driver's `base`, as with ioredis.
const redis = vi.hoisted(() => new Map<string, string>());
vi.mock('unstorage/drivers/redis', () => ({
  default: ({ base }: { base: string }): Driver => {
    const full = (key: string) => `${base}:${key}`;
    return {
      name: 'redis-test',
      hasItem: key => redis.has(full(key)),
      getItem: key => redis.get(full(key)) ?? null,
      setItem: (key, value) => {
        redis.set(full(key), value);
      },
      removeItem: key => {
        redis.delete(full(key));
      },
      getKeys: prefix =>
        [...redis.keys()].filter(key => key.startsWith(full(prefix))).map(key => key.slice(base.length + 1)),
      clear: () => {}
    };
  }
}));
vi.stubGlobal('defineNitroPlugin', (plugin: unknown) => plugin);
const { default: mountCache } = await import('../server/plugins/cache-storage');

afterEach(() => vi.unstubAllGlobals());

/** The page cache a web container with this build mounts, as Nitro's route cache sees it. */
function pageCache(buildId: string) {
  const mount = vi.fn();
  vi.stubGlobal('useRuntimeConfig', () => ({
    app: { buildId },
    redisUrl: 'redis://cache.test/0',
    ogImage: { secret: 'test-stable-og-secret' }
  }));
  vi.stubGlobal('useStorage', () => ({ mount }));
  mountCache({} as never);
  const [, driver] = mount.mock.calls.find(([base]) => base === 'cache')!;
  return createStorage({ driver });
}

it('a release with new web inputs never answers from the previous build’s page cache', async () => {
  const key = 'nitro:routes:_:index.abc123.json';
  const entry = { value: { body: '<script src="/_nuxt/old.js"></script>' }, mtime: 1, expires: 2 };
  await pageCache('inputs-before').setItem(key, entry);
  // Home and lists are never purged on release: the new build reads none of the old entries, fresh or
  // stale, so the first request renders with the new build instead of serving old HTML even once.
  const after = pageCache('inputs-after');
  expect(await after.getItem(key)).toBeNull();
  expect(await after.getKeys('nitro:routes')).toEqual([]);
  // An unchanged web image keeps the same build ID and goes on sharing its pages.
  expect(await pageCache('inputs-before').getItem(key)).toEqual(entry);
});
