import { performance } from 'node:perf_hooks';
import { gzipSync } from 'node:zlib';
import { describe, expect, it, vi } from 'vitest';
import { createStorage } from 'unstorage';
import memoryDriver from 'unstorage/drivers/memory';
import { boundedCache, MAX_CACHE_ENTRY_BYTES } from '../server/utils/boundedCache';

// A synthetic SSR entry with varied links, localized text and serialized payload, about 111KiB.
const page = JSON.stringify({
  value: {
    code: 200,
    headers: { 'content-type': 'text/html' },
    body: Array.from(
      { length: 650 },
      (_, i) =>
        `<article class="package-card"><a href="/apps/package-${i}">Application ${i}</a><p>应用目录: install and update this application safely.</p><span>Version ${i}.2</span></article>`
    ).join('')
  },
  expires: 1800000000000
});

describe('compressed bounded page cache', () => {
  it('round-trips route and handler entries, including Unicode, below one fifth of serialized size', async () => {
    const inner = memoryDriver();
    const cache = boundedCache(inner);
    for (const key of ['nitro:routes:_:page.json', 'nitro:handlers:feed.json']) {
      await cache.setItem!(key, page, {});
      const stored = (await inner.getItem(key)) as string;
      expect(stored.startsWith('gz1:')).toBe(true);
      expect(Buffer.byteLength(stored)).toBeLessThan(Buffer.byteLength(page) / 5);
      expect(await cache.getItem(key)).toBe(page);
    }
    const storage = createStorage({ driver: cache });
    expect(await storage.getItem('nitro:routes:_:page.json')).toEqual(JSON.parse(page));
    expect(await storage.getKeys('nitro:routes')).toEqual(['nitro:routes:_:page.json']);
    await storage.removeItem('nitro:routes:_:page.json');
    expect(await cache.getItem('nitro:routes:_:page.json')).toBeNull();
  });

  it('reads legacy uncompressed entries and leaves other namespaces and tiny values intact', async () => {
    const inner = memoryDriver();
    const cache = boundedCache(inner);
    await inner.setItem!('nitro:routes:old', page, {});
    expect(await cache.getItem('nitro:routes:old')).toBe(page);
    for (const [key, value] of [
      ['nitro:routes:tiny', '{}'],
      ['sitemap:input', page],
      ['nuxt-og-image:image', 'gz1:literal']
    ]) {
      await cache.setItem!(key!, value!, {});
      expect(await inner.getItem(key!)).toBe(value);
      expect(await cache.getItem(key!)).toBe(value);
    }
    expect(await cache.getItem('nitro:routes:missing')).toBeNull();
  });

  it('bounds the uncompressed UTF-8 size and removes the old value even when the oversized value compresses well', async () => {
    const inner = memoryDriver();
    const cache = boundedCache(inner);
    const key = 'nitro:routes:page';
    await cache.setItem!(key, 'x'.repeat(MAX_CACHE_ENTRY_BYTES), {});
    expect(await cache.getItem(key)).toHaveLength(MAX_CACHE_ENTRY_BYTES);
    await cache.setItem!(key, '中'.repeat(Math.ceil(MAX_CACHE_ENTRY_BYTES / 3)), {});
    expect(await inner.getItem(key)).toBeNull();
    await inner.setItem!(key, 'gz1:' + gzipSync('x'.repeat(MAX_CACHE_ENTRY_BYTES + 1)).toString('base64'), {});
    expect(await cache.getItem(key)).toBeNull();
    await inner.setItem!(key, 'gz1:not-a-gzip-entry', {});
    expect(await cache.getItem(key)).toBeNull();
  });

  it('preserves TTL ceilings, expiry defaults and transaction options through compression', async () => {
    const inner = memoryDriver();
    const write = vi.spyOn(inner, 'setItem');
    const cache = boundedCache(inner);
    const cases = [
      [60.9, 60],
      [0, 3600],
      [-1, 3600],
      [NaN, 3600],
      [Infinity, 3600],
      [undefined, 3600],
      [86400, 3600]
    ];
    for (const [ttl, expected] of cases) {
      await cache.setItem!('nitro:routes:page', page, { ttl, marker: 'kept' });
      expect(write).toHaveBeenLastCalledWith('nitro:routes:page', expect.stringMatching(/^gz1:/), {
        ttl: expected,
        marker: 'kept'
      });
    }
    await cache.setItem!('nuxt-og-image:image', page, { ttl: 86400 });
    expect(write).toHaveBeenLastCalledWith('nuxt-og-image:image', page, { ttl: 21600 });
  });

  it('decodes Redis-style batch reads and routes batch writes through the same bounds', async () => {
    const inner = memoryDriver();
    inner.getItems = async items =>
      Promise.all(items.map(async ({ key }) => ({ key, value: await inner.getItem(key) })));
    inner.setItems = vi.fn();
    const storage = createStorage({ driver: boundedCache(inner) });
    await storage.setItems([
      { key: 'nitro:routes:compressed', value: JSON.parse(page) },
      { key: 'nitro:routes:oversized', value: 'x'.repeat(MAX_CACHE_ENTRY_BYTES + 1) }
    ]);
    await inner.setItem!('nitro:routes:legacy', page, {});
    expect(inner.setItems).not.toHaveBeenCalled();
    expect(
      await storage.getItems(['nitro:routes:compressed', 'nitro:routes:legacy', 'nitro:routes:oversized'])
    ).toEqual([
      { key: 'nitro:routes:compressed', value: JSON.parse(page) },
      { key: 'nitro:routes:legacy', value: JSON.parse(page) },
      { key: 'nitro:routes:oversized', value: null }
    ]);
  });

  it('reports local compression size and elapsed cost without a machine-dependent performance gate', async () => {
    const inner = memoryDriver();
    const cache = boundedCache(inner);
    const rounds = 50;
    const start = performance.now();
    for (let i = 0; i < rounds; i++) await cache.setItem!('nitro:routes:bench', page, {});
    const writesMs = (performance.now() - start) / rounds;
    const readStart = performance.now();
    for (let i = 0; i < rounds; i++) expect(await cache.getItem('nitro:routes:bench')).toBe(page);
    const readsMs = (performance.now() - readStart) / rounds;
    console.info('cache compression synthetic sample', {
      rawBytes: Buffer.byteLength(page),
      storedBytes: Buffer.byteLength((await inner.getItem('nitro:routes:bench')) as string),
      writesMs,
      readsMs
    });
  });
});

it('isolates image eviction from page entries and retains the image TTL after submounting', async () => {
  const pages = memoryDriver();
  const images = memoryDriver();
  const writes = vi.spyOn(images, 'setItem');
  const storage = createStorage();
  storage.mount('cache', boundedCache(pages));
  storage.mount('cache:nuxt-og-image', boundedCache(images, 'nuxt-og-image:'));
  await storage.setItem('cache:nitro:routes:page', { body: 'page' });
  await storage.setItem('cache:nuxt-og-image:6:image', { value: 'image' });
  expect(writes).toHaveBeenCalledWith('6:image', expect.any(String), expect.objectContaining({ ttl: 21600 }));
  expect(await pages.getKeys()).toEqual(['nitro:routes:page']);
  await images.clear!(); // Represents eviction/expiry in the independent image cache.
  expect(await storage.getItem('cache:nitro:routes:page')).toEqual({ body: 'page' });
  expect(await storage.getItem('cache:nuxt-og-image:6:image')).toBeNull();
});
