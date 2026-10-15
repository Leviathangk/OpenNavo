import { afterEach, describe, expect, it, vi } from 'vitest';
import { createStorage } from 'unstorage';
import memoryDriver from 'unstorage/drivers/memory';
import { LOCALES } from '@opennavo/shared';
import { boundedCache, MAX_CACHE_ENTRY_BYTES } from '../server/utils/boundedCache';
import { packageSitemaps } from '../shared/utils/sitemapBuckets';

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.resetModules();
});

type HookContext = {
  sitemaps: { _sitemapName: string }[];
  sitemapName: string;
  urls: { loc: string }[];
};
type Hook = (ctx: HookContext) => Promise<void>;
async function setup(records: Record<string, unknown>[]) {
  const sizes: number[] = [];
  const inner = memoryDriver();
  const write = inner.setItem!;
  inner.setItem = (key, value, options) => {
    sizes.push(Buffer.byteLength(value, 'utf8'));
    return write(key, value, options);
  };
  const storage = createStorage({ driver: boundedCache(inner) });
  vi.stubGlobal('useStorage', () => storage);
  const hooks: Record<string, Hook> = {};
  const fetch = vi.fn(async (url: string, options?: { query: { group: string; current: number; size: number } }) => {
    if (url.endsWith('/sitemap/articles')) return { data: { articles: [], authors: [] } };
    const { group, current, size } = options!.query;
    const source = group === 'apps' ? records : [];
    return {
      data: { total: source.length, records: source.slice((current - 1) * size, current * size) }
    };
  });
  vi.stubGlobal(
    'defineNitroPlugin',
    (plugin: (app: { hooks: { hook: (name: string, handler: Hook) => void } }) => void) =>
      plugin({
        hooks: {
          hook: (name: string, handler: Hook) => {
            hooks[name] = handler;
          }
        }
      })
  );
  vi.stubGlobal('useRuntimeConfig', () => ({
    apiBaseInternal: 'https://example.test',
    public: {}
  }));
  vi.stubGlobal('getSiteSettings', async () => ({ pages: { versionsSitemap: true } }));
  vi.stubGlobal('$fetch', fetch);
  await import('../server/plugins/sitemap-sources');
  const index = async () => {
    const ctx = {
      sitemaps: Object.keys(packageSitemaps).map(_sitemapName => ({ _sitemapName }))
    };
    await hooks['sitemap:index-resolved']!(ctx);
    return ctx.sitemaps;
  };
  const bucket = async (name: string) => {
    const ctx = { sitemaps: [], sitemapName: name, urls: [] as { loc: string }[] };
    await hooks['sitemap:input']!(ctx);
    return ctx.urls;
  };
  return { index, bucket, sizes, fetch, storage };
}

describe('sitemap source snapshot cache', () => {
  it('reuses a 6000-app catalog for repeated indexes and all buckets within the storage limit', async () => {
    const records = Array.from({ length: 6000 }, (_, i) => ({
      kind: 'cask',
      token: `app-${i}`,
      updatedAt: '2026-10-01T00:00:00Z',
      notesCount: 5,
      locales: LOCALES.map(locale => ({ locale, lastmod: '2026-10-01T00:00:00Z' }))
    }));
    expect(Buffer.byteLength(JSON.stringify(records))).toBeGreaterThan(MAX_CACHE_ENTRY_BYTES);
    const { index, bucket, fetch, sizes, storage } = await setup(records);
    const first = await index();
    const calls = fetch.mock.calls.filter(([url]) => url.endsWith('/sitemap/packages')).length;
    expect(calls).toBe(13); // Twelve app pages and one empty font page.
    expect(await index()).toEqual(first);
    const urls = (
      await Promise.all(
        first.filter(entry => entry['_sitemapName'].startsWith('apps-')).map(entry => bucket(entry['_sitemapName']))
      )
    ).flat();
    expect(urls).toHaveLength(6000 * LOCALES.length);
    expect(new Set(urls.map(url => url.loc)).size).toBe(urls.length);
    await bucket('versions-bucket-0');
    expect(fetch.mock.calls.filter(([url]) => url.endsWith('/sitemap/packages'))).toHaveLength(calls);
    expect(await storage.getKeys()).toHaveLength(15); // Thirteen chunks and two complete snapshot manifests.
    expect(Math.max(...sizes)).toBeLessThan(MAX_CACHE_ENTRY_BYTES);
  });

  it('restarts the whole catalog after a later page fails and an earlier app is hidden', async () => {
    const records = Array.from({ length: 1000 }, (_, i) => ({
      kind: 'cask',
      token: `app-${i}`,
      locales: [{ locale: 'en-US' }]
    }));
    const { bucket, fetch } = await setup(records);
    const fetchPage = fetch.getMockImplementation()!;
    fetch.mockImplementationOnce(fetchPage).mockRejectedValueOnce(new Error('page 2 failed'));
    await expect(bucket('apps-bucket-0')).rejects.toThrow('page 2 failed');
    records.shift();
    const urls = (
      await Promise.all(
        Object.keys(packageSitemaps)
          .filter(name => name.startsWith('apps-'))
          .map(name => bucket(name))
      )
    ).flat();
    expect(urls).toContainEqual(expect.objectContaining({ loc: '/apps/app-500' }));
    expect(urls).not.toContainEqual(expect.objectContaining({ loc: '/apps/app-0' }));
    expect(new Set(urls.map(url => url.loc))).toEqual(new Set(records.map(record => `/apps/${record.token}`)));
  });

  it('reloads the whole catalog if a snapshot chunk is evicted', async () => {
    const records = Array.from({ length: 1000 }, (_, i) => ({
      kind: 'cask',
      token: `app-${i}`,
      locales: [{ locale: 'en-US' }]
    }));
    const { index, bucket, storage, fetch } = await setup(records);
    await index();
    const keys = await storage.getKeys();
    const chunk = keys.find(key => key.startsWith('sitemap-package-snapshots-v2:apps:') && key.endsWith(':1'))!;
    expect(chunk).toBeDefined();
    await storage.removeItem(chunk);
    records.shift();
    const urls = (
      await Promise.all(
        Object.keys(packageSitemaps)
          .filter(name => name.startsWith('apps-'))
          .map(name => bucket(name))
      )
    ).flat();
    expect(new Set(urls.map(url => url.loc))).toEqual(new Set(records.map(record => `/apps/${record.token}`)));
    const packageCalls = () => fetch.mock.calls.filter(([url]) => url.endsWith('/sitemap/packages')).length;
    expect(packageCalls()).toBe(5); // Initial apps/fonts plus exactly one two-page rebuild.
    await bucket('apps-bucket-0');
    await bucket('apps-bucket-1');
    expect(packageCalls()).toBe(5);
    expect(await storage.getKeys()).toHaveLength(5);
  });

  it('reclaims old snapshots across five refreshes when the memory driver ignores TTL', async () => {
    vi.useFakeTimers();
    const records = Array.from({ length: 1000 }, (_, i) => ({
      kind: 'cask',
      token: `app-${i}`,
      locales: [{ locale: 'en-US' }]
    }));
    const { bucket, storage, fetch } = await setup(records);
    for (let round = 0; round < 5; round += 1) {
      await bucket('apps-bucket-0');
      expect(await storage.getKeys()).toHaveLength(3);
      vi.setSystemTime(Date.now() + 61000);
    }
    expect(fetch).toHaveBeenCalledTimes(10);
  });

  it('cleans partial writes and allows retry after a failed refresh', async () => {
    vi.useFakeTimers();
    const records = Array.from({ length: 1000 }, (_, i) => ({
      kind: 'cask',
      token: `app-${i}`,
      locales: [{ locale: 'en-US' }]
    }));
    const { bucket, storage, fetch } = await setup(records);
    await bucket('apps-bucket-0');
    const originalKeys = await storage.getKeys();
    vi.setSystemTime(Date.now() + 61000);
    const write = storage.setItem.bind(storage);
    const spy = vi.spyOn(storage, 'setItem');
    spy.mockImplementationOnce(write).mockRejectedValueOnce(new Error('storage unavailable'));
    await expect(bucket('apps-bucket-0')).rejects.toThrow('storage unavailable');
    expect(await storage.getKeys()).toEqual(originalKeys);
    spy.mockRestore();
    await Promise.all([bucket('apps-bucket-0'), bucket('apps-bucket-1')]);
    expect(await storage.getKeys()).toHaveLength(3);
    expect(fetch).toHaveBeenCalledTimes(6);
    await bucket('apps-bucket-0');
    expect(fetch).toHaveBeenCalledTimes(6);
  });

  it('continues past filtered pages and retries failed pages without caching an empty result', async () => {
    const records = Array.from({ length: 501 }, (_, i) => ({
      kind: i < 500 ? 'formula' : 'cask',
      token: `app-${i}`,
      locales: [{ locale: 'en-US' }]
    }));
    const { index, bucket, fetch } = await setup(records);
    fetch.mockRejectedValueOnce(new Error('API unavailable'));
    await expect(bucket('apps-bucket-0')).rejects.toThrow('API unavailable');
    const entries = await index();
    const apps = entries.filter(entry => entry['_sitemapName'].startsWith('apps-'));
    expect(apps).toHaveLength(1);
    expect(await bucket(apps[0]!['_sitemapName'])).toEqual([expect.objectContaining({ loc: '/apps/app-500' })]);
  });
});
