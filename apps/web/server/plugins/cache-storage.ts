import { createHash } from 'node:crypto';
import lruCacheDriver from 'unstorage/drivers/lru-cache';
import redisDriver from 'unstorage/drivers/redis';

import { boundedCache, IMAGE_CACHE_TTL_SECONDS, PAGE_CACHE_TTL_SECONDS } from '../utils/boundedCache';

interface CacheBuildConfig {
  app: { buildId: string };
  ogImage?: { secret?: unknown };
  'nuxt-og-image'?: { security?: { secret?: unknown } };
}

// Production normally uses Redis. Keep the local fallback bounded as well: Nitro's default memory
// driver ignores TTL, so a crawler can otherwise turn every distinct route/query into a retained object.
// The page and image pools are separate because a single rendered PNG can be much larger than an HTML entry.
export const MEMORY_PAGE_CACHE_OPTIONS = {
  max: 128,
  maxSize: 32 * 1024 * 1024,
  ttl: PAGE_CACHE_TTL_SECONDS * 1000,
  ttlAutopurge: true
} as const;
export const MEMORY_IMAGE_CACHE_OPTIONS = {
  max: 32,
  maxSize: 16 * 1024 * 1024,
  ttl: IMAGE_CACHE_TTL_SECONDS * 1000,
  ttlAutopurge: true
} as const;

// Match nuxt-og-image's Node runtime override: a nonempty runtime secret wins over the generated
// build secret. A deployment without a stable override must retire HTML containing old signatures.
export function cacheBuildIdentity(config: CacheBuildConfig) {
  const override = config.ogImage?.secret;
  const secret = typeof override === 'string' && override ? override : config['nuxt-og-image']?.security?.secret;
  const fingerprint = createHash('sha256')
    .update(typeof secret === 'string' ? secret : '')
    .digest('hex')
    .slice(0, 12);
  return {
    build: `${config.app.buildId}.${fingerprint}`,
    namespace: `opennavo:web:cache:${config.app.buildId}:${fingerprint}`
  };
}

// Store Nitro SWR cache in Redis for shared instances (05 §5); use bounded LRU pools when Redis is absent.
// Route caching is production-only; development already mounts cache, and remounting causes 500s (CI E2E injects NUXT_REDIS_URL).
export default defineNitroPlugin(() => {
  const config = useRuntimeConfig();
  const { redisUrl } = config;
  if (import.meta.dev) return;
  const storage = useStorage();
  // Namespace by build: cached pages reference hashed /_nuxt assets that may not exist in replacement containers.
  storage.mount(
    'cache',
    boundedCache(
      redisUrl
        ? redisDriver({
            url: redisUrl,
            base: cacheBuildIdentity(config).namespace,
            ttl: PAGE_CACHE_TTL_SECONDS,
            maxRetriesPerRequest: 1,
            connectTimeout: 1000,
            commandTimeout: 1000
          })
        : lruCacheDriver(MEMORY_PAGE_CACHE_OPTIONS)
    )
  );
  // A more-specific mount keeps large image buffers out of the page/API cache pool.
  // Empty preserves compatibility for local installations until an image cache is provisioned.
  storage.mount(
    'cache:nuxt-og-image',
    boundedCache(
      config.imageRedisUrl
        ? redisDriver({
            url: config.imageRedisUrl,
            base: `${cacheBuildIdentity(config).namespace}:nuxt-og-image`,
            ttl: IMAGE_CACHE_TTL_SECONDS,
            maxRetriesPerRequest: 1,
            connectTimeout: 1000,
            commandTimeout: 1000
          })
        : lruCacheDriver(MEMORY_IMAGE_CACHE_OPTIONS),
      'nuxt-og-image:'
    )
  );
});
