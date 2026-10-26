import { describe, expect, it } from 'vitest';
import { createStorage } from 'unstorage';
import memoryDriver from 'unstorage/drivers/memory';
import { boundedCache, MAX_CACHE_ENTRY_BYTES } from '../server/utils/boundedCache';

describe('bounded serialized page cache', () => {
  it('caps page/image TTL even when a caller requests two days or disables expiry', async () => {
    const calls: { key: string; ttl: unknown }[] = [];
    const inner = memoryDriver();
    const write = inner.setItem!;
    inner.setItem = (key, value, opts) => {
      calls.push({ key, ttl: opts?.ttl });
      return write(key, value, opts);
    };
    const storage = createStorage({ driver: boundedCache(inner) });
    await storage.setItem('nitro:routes:page', { body: 'test' }, { ttl: 172800 });
    await storage.setItem('nitro:routes:page2', { body: 'test' }, { ttl: 0 });
    await storage.setItem('nuxt-og-image:image', 'test', { ttl: 172800 });
    await storage.setItem('short', 'test', { ttl: 60 });
    // Pages keep a day, longer than the six-hour app page rule, so a stale copy can answer while it refreshes.
    expect(calls.map(call => call.ttl)).toEqual([86400, 86400, 21600, 60]);
  });
  it('measures UTF-8 bytes and removes a previously cached entry when it grows too large', async () => {
    const storage = createStorage({ driver: boundedCache(memoryDriver()) });
    await storage.setItem('page', 'small');
    expect(await storage.getItem('page')).toBe('small');
    await storage.setItem('page', '中'.repeat(Math.ceil(MAX_CACHE_ENTRY_BYTES / 3)));
    expect(await storage.getItem('page')).toBeNull();
    await storage.setItem('normal', { total: 6 });
    expect(await storage.getItem('normal')).toEqual({ total: 6 });
  });
});
