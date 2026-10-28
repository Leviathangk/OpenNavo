import { createHash } from 'node:crypto';
import { afterEach, expect, it, vi } from 'vitest';
import redisDriver from 'unstorage/drivers/redis';

vi.mock('unstorage/drivers/redis', () => ({ default: vi.fn(() => ({ name: 'redis' })) }));
vi.mock('../server/utils/boundedCache', () => ({
  boundedCache: (driver: unknown) => driver,
  PAGE_CACHE_TTL_SECONDS: 86400,
  IMAGE_CACHE_TTL_SECONDS: 21600
}));
vi.stubGlobal('defineNitroPlugin', (plugin: unknown) => plugin);
const { cacheBuildIdentity, default: mountCache } = await import('../server/plugins/cache-storage');
afterEach(() => {
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});

const config = (builtIn = 'test-build-secret-a', override?: unknown) => ({
  app: { buildId: 'test-web-inputs' },
  ogImage: { secret: override },
  'nuxt-og-image': { security: { secret: builtIn } }
});
const fingerprint = (secret: string) => createHash('sha256').update(secret).digest('hex').slice(0, 12);

it('uses the effective runtime override and never exposes either secret', () => {
  const override = 'test-only-stable-runtime-secret';
  const identity = cacheBuildIdentity(config('test-build-secret-a', override));
  expect(identity).toEqual({
    build: `test-web-inputs.${fingerprint(override)}`,
    namespace: `opennavo:web:cache:test-web-inputs:${fingerprint(override)}`
  });
  expect(cacheBuildIdentity(config('test-build-secret-b', override))).toEqual(identity);
  expect(JSON.stringify(identity)).not.toContain(override);
  expect(JSON.stringify(identity)).not.toContain('test-build-secret-a');
  expect(cacheBuildIdentity(config('test-build-secret-a', 'test-rotated-secret'))).not.toEqual(identity);
  expect(
    cacheBuildIdentity({ ...config('test-build-secret-a', override), app: { buildId: 'new-inputs' } })
  ).not.toEqual(identity);
});

it('falls back to the generated build key just like the OG renderer', () => {
  const a = cacheBuildIdentity(config());
  expect(cacheBuildIdentity(config('test-build-secret-b'))).not.toEqual(a);
  for (const override of ['', false, null, 42])
    expect(cacheBuildIdentity(config('test-build-secret-a', override))).toEqual(a);
  expect(a.build).toBe(`test-web-inputs.${fingerprint('test-build-secret-a')}`);
});

it('mounts Redis under the same key identity and preserves bounded-cache settings', () => {
  const runtime = { ...config('test-build-secret-a', 'test-runtime-secret'), redisUrl: 'redis://cache.example/0' };
  const mount = vi.fn();
  vi.stubGlobal('useRuntimeConfig', () => runtime);
  vi.stubGlobal('useStorage', () => ({ mount }));
  mountCache({} as never);
  expect(redisDriver).toHaveBeenCalledWith(
    expect.objectContaining({
      base: cacheBuildIdentity(runtime).namespace,
      url: runtime.redisUrl,
      ttl: 86400,
      maxRetriesPerRequest: 1,
      connectTimeout: 1000,
      commandTimeout: 1000
    })
  );
  expect(mount).toHaveBeenCalledWith('cache', { name: 'redis' });
});

it('mounts image buffers in a separate Redis process while retaining the build identity', () => {
  const runtime = {
    ...config(),
    redisUrl: 'redis://pages.example/0',
    imageRedisUrl: 'redis://images.example/0'
  };
  const mount = vi.fn();
  vi.stubGlobal('useRuntimeConfig', () => runtime);
  vi.stubGlobal('useStorage', () => ({ mount }));
  mountCache({} as never);
  expect(mount.mock.calls.map(([path]) => path)).toEqual(['cache', 'cache:nuxt-og-image']);
  expect(redisDriver).toHaveBeenLastCalledWith(
    expect.objectContaining({
      url: runtime.imageRedisUrl,
      ttl: 21600,
      base: `${cacheBuildIdentity(runtime).namespace}:nuxt-og-image`
    })
  );
});

it('uses bounded LRU pools when Redis is not configured', () => {
  const runtime = config();
  const mount = vi.fn();
  vi.stubGlobal('useRuntimeConfig', () => runtime);
  vi.stubGlobal('useStorage', () => ({ mount }));
  mountCache({} as never);
  const page = mount.mock.calls.find(([path]) => path === 'cache')?.[1] as { options?: Record<string, unknown> };
  const images = mount.mock.calls.find(([path]) => path === 'cache:nuxt-og-image')?.[1] as {
    options?: Record<string, unknown>;
  };
  expect(page.options).toMatchObject({ max: 128, maxSize: 32 * 1024 * 1024, ttl: 86400 * 1000, ttlAutopurge: true });
  expect(images.options).toMatchObject({ max: 32, maxSize: 16 * 1024 * 1024, ttl: 21600 * 1000, ttlAutopurge: true });
});
