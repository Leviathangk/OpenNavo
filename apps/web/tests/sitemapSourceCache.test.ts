import { afterEach, describe, expect, it, vi } from 'vitest';
import { createStorage } from 'unstorage';
import memoryDriver from 'unstorage/drivers/memory';
import { warmSitemaps } from '../server/utils/sitemapWarmup';
import { labelPath } from '../shared/utils/backgroundLabels';
import { LOCALES, LOCALE_METADATA } from '@opennavo/shared';
import { boundedCache, MAX_CACHE_ENTRY_BYTES } from '../server/utils/boundedCache';
import { packageSitemapName, packageSitemaps } from '../shared/utils/sitemapBuckets';

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  vi.resetModules();
});

type HookContext = {
  sitemaps: { _sitemapName: string; lastmod?: string }[];
  sitemapName: string;
  urls: { loc: string }[];
};
type Hook = (ctx: HookContext) => Promise<void>;
async function setup(records: Record<string, unknown>[], fontsSitemap = true, fonts: Record<string, unknown>[] = []) {
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
  const fetch = vi.fn(
    async (url: string, options?: { query: { group: string; current: number; size: number; kind?: string } }) => {
      if (url.endsWith('/browse')) return { data: { buckets: [{ key: 'a', pages: 2 }] } };
      if (url.endsWith('/sitemap/articles')) return { data: { articles: [], authors: [] } };
      const { group, current, size } = options!.query;
      const source = group === 'apps' ? records : fonts;
      return {
        data: { total: source.length, records: source.slice((current - 1) * size, current * size) }
      };
    }
  );
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
  vi.stubGlobal('useRuntimeConfig', () => ({ apiBaseInternal: 'https://example.test', public: {} }));
  const settings = { revision: 1, pages: { versionsSitemap: true, fontsSitemap } };
  vi.stubGlobal('getSiteSettings', async () => settings);
  vi.stubGlobal('$fetch', fetch);
  const plugin = await import('../server/plugins/sitemap-sources');
  const index = async (sitemaps = Object.keys(packageSitemaps).map(_sitemapName => ({ _sitemapName }))) => {
    const ctx = { sitemaps };

    await hooks['sitemap:index-resolved']!(ctx);
    return ctx.sitemaps;
  };
  const bucket = async (name: string) => {
    const ctx = { sitemaps: [], sitemapName: name, urls: [] as { loc: string }[] };
    await hooks['sitemap:input']!(ctx);
    return ctx.urls;
  };
  const packageCalls = () => fetch.mock.calls.filter(([url]) => url.endsWith('/sitemap/packages')).length;
  return {
    index,
    bucket,
    sizes,
    fetch,
    storage,
    packageCalls,
    settings,
    idle: plugin.sitemapSourcesIdle,
    // Past these the snapshot is stale, or a failed refresh may be retried.
    stale: () => vi.setSystemTime(Date.now() + plugin.SNAPSHOT_FRESH_MS + 1000),
    retry: () => vi.setSystemTime(Date.now() + plugin.SNAPSHOT_RETRY_MS + 1000)
  };
}

const appBuckets = Object.keys(packageSitemaps).filter(name => name.startsWith('apps-'));
const catalog = (count: number) =>
  Array.from({ length: count }, (_, i) => ({ kind: 'cask', token: `app-${i}`, locales: [{ locale: 'en-US' }] }));

describe('sitemap source snapshot cache', () => {
  it.each([false, true])('controls font buckets and font versions with fontsSitemap=%s', async enabled => {
    const font = { kind: 'cask', token: 'font-sample', notesCount: 5, locales: [{ locale: 'en-US' }] };
    const { index, bucket, fetch } = await setup(catalog(1), enabled, [font]);
    const entries = await index();
    const fontName = packageSitemapName('fonts', font.token);
    const versionsName = packageSitemapName('versions', font.token);
    expect(entries.some(entry => entry['_sitemapName'] === fontName)).toBe(Boolean(enabled));
    const versions = await bucket(versionsName);
    expect(versions.some(entry => entry.loc === '/apps/font-sample/versions')).toBe(Boolean(enabled));
    if (enabled) expect(await bucket(fontName)).toContainEqual(expect.objectContaining({ loc: '/apps/font-sample' }));
    else {
      await expect(bucket(fontName)).rejects.toMatchObject({ statusCode: 410 });
      expect(fetch.mock.calls.some(([, options]) => options?.query?.group === 'fonts')).toBe(false);
    }
  });

  it('checks source switches even with a warm populated index and restores enabled buckets', async () => {
    const record = { kind: 'cask', token: 'sample', notesCount: 5, locales: [{ locale: 'en-US' }] };
    const { index, bucket, settings, packageCalls } = await setup([record], true, [record]);
    const names = [packageSitemapName('fonts', record.token), packageSitemapName('versions', record.token)];
    const original = await index();
    const reads = packageCalls();
    for (const name of names) expect(original).toContainEqual({ _sitemapName: name });

    settings.revision += 1;
    settings.pages.fontsSitemap = false;
    settings.pages.versionsSitemap = false;
    const closed = await index();
    for (const name of names) {
      expect(closed).not.toContainEqual({ _sitemapName: name });
      await expect(bucket(name)).rejects.toMatchObject({ statusCode: 410 });
    }

    settings.revision += 1;
    settings.pages.fontsSitemap = true;
    settings.pages.versionsSitemap = true;
    expect(await index()).toEqual(original);
    expect(await bucket(names[0]!)).toContainEqual(expect.objectContaining({ loc: '/apps/sample' }));
    expect(await bucket(names[1]!)).toContainEqual(expect.objectContaining({ loc: '/apps/sample/versions' }));
    expect(packageCalls()).toBe(reads);
  });

  it('serves the first external index and every bucket from startup-warmed snapshots', async () => {
    const { index, bucket, packageCalls, fetch } = await setup(catalog(1000));
    await warmSitemaps(async path => {
      if (path === '/sitemap_index.xml') await index();
      else await bucket(path.split('/').at(-1)!.replace('.xml', ''));
      return { status: 200 };
    });
    expect(packageCalls()).toBe(3);
    const original = fetch.getMockImplementation()!;
    fetch.mockImplementation(async (url, options) => {
      if (url.endsWith('/sitemap/packages')) throw new Error('external request must use the warmed snapshot');
      return original(url, options);
    });
    expect((await index()).length).toBeGreaterThan(0);
    const urls = (await Promise.all(Object.keys(packageSitemaps).map(bucket))).flat();
    expect(urls).toHaveLength(1000);
    expect(packageCalls()).toBe(3);
  });

  it('removes inherited index dates without mutating the module cache across snapshot refreshes', async () => {
    vi.useFakeTimers();
    const records = [{ kind: 'cask', token: 'example', locales: [{ locale: 'en-US', lastmod: '2026-01-01' }] }];
    const { index, bucket, stale, idle } = await setup(records);
    const name = packageSitemapName('apps', 'example');
    const moduleEntries = [{ _sitemapName: name, lastmod: '2026-01-01T00:00:00.123456Z' }];
    expect(await index(moduleEntries)).toEqual([{ _sitemapName: name }]);
    stale();
    records[0]!.locales[0]!.lastmod = '2026-10-26';
    await bucket(name);
    await idle();
    expect((await bucket(name))[0]).toHaveProperty('lastmod', '2026-10-26T00:00:00Z');
    expect(await index(moduleEntries)).toEqual([{ _sitemapName: name }]);
    expect(moduleEntries[0]).toHaveProperty('lastmod', '2026-01-01T00:00:00.123456Z');
    await idle();
  });

  it('does not cache empty buckets after a failed startup warmup and recovers on the next request', async () => {
    vi.useFakeTimers();
    vi.spyOn(console, 'warn').mockImplementation(() => {});
    const { index, bucket, fetch, storage } = await setup(catalog(10));
    const original = fetch.getMockImplementation()!;
    fetch.mockRejectedValue(new Error('API unavailable'));
    const pending = warmSitemaps(async path => {
      if (path === '/sitemap_index.xml') await index();
      else await bucket(path.split('/').at(-1)!.replace('.xml', ''));
      return { status: 200 };
    });
    await vi.runAllTimersAsync();
    await pending;
    expect(await storage.getKeys()).toEqual([]);
    fetch.mockImplementation(original);
    expect((await index()).length).toBeGreaterThan(0);
    expect((await Promise.all(appBuckets.map(name => bucket(name)))).flat()).toHaveLength(10);
  });

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

  it('stores catalogs with every page decided in every language below the limit and lists what they allow', async () => {
    // Production records carry a full decision per page and language (about 8.7 KB each), so a page of
    // 500 is several times the cache's entry limit, which silently drops what is larger.
    const decision = (inSitemap: boolean, path: string) => ({
      robots: inSitemap ? 'index, follow' : 'noindex, follow',
      canonicalPath: inSitemap ? '' : path,
      hreflang: inSitemap ? [...LOCALES] : [],
      inSitemap,
      announce: false,
      reason: inSitemap ? '' : 'intent_disabled'
    });
    const records = Array.from({ length: 1200 }, (_, i) => ({
      kind: 'cask',
      token: `app-${i}`,
      updatedAt: '2026-10-01T00:00:00Z',
      notesCount: 9,
      locales: LOCALES.map(locale => ({
        locale,
        lastmod: '2026-10-01T00:00:00Z',
        pageLastmod: {
          overview: '2026-10-02',
          versions: '2026-10-03',
          'versions:2': '2026-10-04',
          alternatives: '2026-10-05',
          dependencies: '2026-10-06'
        },
        pages: {
          overview: decision(locale !== 'ru-RU', `/apps/app-${i}`),
          versions: decision(locale === 'en-US', `/apps/app-${i}/versions`),
          dependencies: decision(false, `/apps/app-${i}`),
          details: decision(false, `/apps/app-${i}`),
          alternatives: decision(locale === 'ja-JP' && i % 2 === 0, `/apps/app-${i}`),
          uninstall: decision(false, `/apps/app-${i}`)
        }
      }))
    }));
    expect(Buffer.byteLength(JSON.stringify(records.slice(0, 500)))).toBeGreaterThan(MAX_CACHE_ENTRY_BYTES);
    const { index, bucket, sizes } = await setup(records);
    const listed = await index();
    expect(Math.max(...sizes)).toBeLessThan(MAX_CACHE_ENTRY_BYTES);
    const urls = (
      await Promise.all(
        listed.filter(entry => entry['_sitemapName'].startsWith('apps-')).map(entry => bucket(entry['_sitemapName']))
      )
    ).flat();
    const locs = new Set(urls.map(url => url.loc));
    expect(locs.has('/apps/app-0')).toBe(true);
    expect(locs.has('/ru/apps/app-0')).toBe(false);
    expect(locs.has('/ja/apps/app-0/alternatives')).toBe(true);
    expect(locs.has('/ja/apps/app-1/alternatives')).toBe(false);
    expect(locs.has('/apps/app-0/alternatives')).toBe(false);
    expect([...locs].some(loc => loc.endsWith('/uninstall') || loc.endsWith('/troubleshooting'))).toBe(false);
    expect(urls).toHaveLength(1200 * (LOCALES.length - 1) + 600);
    expect(urls.find(url => url.loc === '/apps/app-0')).toMatchObject({ lastmod: '2026-10-02T00:00:00Z' });
    expect(urls.find(url => url.loc === '/ja/apps/app-0/alternatives')).toMatchObject({
      lastmod: '2026-10-05T00:00:00Z'
    });
    const versions = (
      await Promise.all(
        listed
          .filter(entry => entry['_sitemapName'].startsWith('versions-'))
          .map(entry => bucket(entry['_sitemapName']))
      )
    ).flat();
    expect(versions).toHaveLength(1200);
    expect(versions.every(url => /^\/apps\/app-\d+\/versions$/.test(url.loc))).toBe(true);
    expect(versions[0]).toMatchObject({ lastmod: '2026-10-03T00:00:00Z' });
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

  it('reclaims old snapshots across five background refreshes when the memory driver ignores TTL', async () => {
    vi.useFakeTimers();
    const { bucket, storage, fetch, idle, stale } = await setup(catalog(1000));
    for (let round = 0; round < 5; round += 1) {
      await bucket('apps-bucket-0');
      await idle();
      expect(await storage.getKeys()).toHaveLength(3);
      stale();
    }
    expect(fetch).toHaveBeenCalledTimes(10);
  });

  it('answers from a stale snapshot at once and rebuilds it once behind the responses', async () => {
    vi.useFakeTimers();
    const records = catalog(1000);
    const { bucket, packageCalls, idle, stale } = await setup(records);
    await bucket('apps-bucket-0');
    expect(packageCalls()).toBe(2);
    stale();
    records.shift();
    const staleUrls = (await Promise.all(appBuckets.map(name => bucket(name)))).flat();
    expect(staleUrls).toContainEqual(expect.objectContaining({ loc: '/apps/app-0' }));
    await idle();
    expect(packageCalls()).toBe(4);
    const freshUrls = (await Promise.all(appBuckets.map(name => bucket(name)))).flat();
    expect(freshUrls).not.toContainEqual(expect.objectContaining({ loc: '/apps/app-0' }));
    expect(freshUrls).toHaveLength(999);
    expect(packageCalls()).toBe(4);
  });

  it('keeps the last snapshot when a refresh fails, cleans its partial writes and retries after a pause', async () => {
    vi.useFakeTimers();
    vi.spyOn(console, 'error').mockImplementation(() => undefined);
    const { bucket, storage, fetch, idle, stale, retry } = await setup(catalog(1000));
    const first = await bucket('apps-bucket-0');
    const originalKeys = await storage.getKeys();
    stale();
    const write = storage.setItem.bind(storage);
    const spy = vi.spyOn(storage, 'setItem');
    spy.mockImplementationOnce(write).mockRejectedValueOnce(new Error('storage unavailable'));
    expect(await bucket('apps-bucket-0')).toEqual(first);
    await idle();
    expect(await storage.getKeys()).toEqual(originalKeys);
    spy.mockRestore();
    expect(fetch).toHaveBeenCalledTimes(4);
    // No retry during the pause; afterwards one rebuild serves concurrent readers.
    expect(await bucket('apps-bucket-0')).toEqual(first);
    await idle();
    expect(fetch).toHaveBeenCalledTimes(4);
    retry();
    await Promise.all([bucket('apps-bucket-0'), bucket('apps-bucket-1')]);
    await idle();
    expect(await storage.getKeys()).toHaveLength(3);
    expect(fetch).toHaveBeenCalledTimes(6);
    await bucket('apps-bucket-0');
    await idle();
    expect(fetch).toHaveBeenCalledTimes(6);
  });

  it('moves on to a snapshot published while it was reading instead of rebuilding', async () => {
    const { bucket, storage, packageCalls } = await setup(catalog(1000));
    const before = await bucket('apps-bucket-0');
    const manifestKey = (await storage.getKeys()).find(key => key.includes('manifest') && key.endsWith(':apps'))!;
    const manifest = (await storage.getItem<{ keys: string[]; expires: number }>(manifestKey))!;
    const read = storage.getItem.bind(storage);
    let swapped = false;
    vi.spyOn(storage, 'getItem').mockImplementation((async (key: string) => {
      if (!swapped && key === manifest.keys[0]) {
        swapped = true;
        // Another server publishes a new snapshot and reclaims this one mid-read.
        const next = manifest.keys.map(name => `${name}-next`);
        for (const [i, name] of manifest.keys.entries()) {
          await storage.setItem(next[i]!, await read(name));
          await storage.removeItem(name);
        }
        await storage.setItem(manifestKey, { keys: next, expires: manifest.expires });
      }
      return read(key);
    }) as typeof storage.getItem);
    expect(await bucket('apps-bucket-0')).toEqual(before);
    expect(packageCalls()).toBe(2);
  });

  it('keeps answering the index from its last result while the catalog API is down', async () => {
    vi.useFakeTimers();
    vi.spyOn(console, 'error').mockImplementation(() => undefined);
    const { index, fetch, packageCalls, idle, stale } = await setup(catalog(1000));
    const first = await index();
    expect(first.length).toBeGreaterThan(0);
    const calls = packageCalls();
    // A warm index does not touch the catalog at all.
    expect(await index()).toEqual(first);
    expect(packageCalls()).toBe(calls);
    stale();
    const fetchPage = fetch.getMockImplementation()!;
    fetch.mockImplementation(async (url, options) => {
      if (url.endsWith('/sitemap/packages')) throw new Error('API unavailable');
      return fetchPage(url, options);
    });
    expect(await index()).toEqual(first);
    await idle();
    expect(await index()).toEqual(first);
    expect(packageCalls()).toBeGreaterThan(calls);
  });

  it('fails a cold index loudly when the catalog API is down', async () => {
    const { index, fetch } = await setup(catalog(10));
    const fetchPage = fetch.getMockImplementation()!;
    fetch.mockImplementation(async (url, options) => {
      if (url.endsWith('/sitemap/packages')) throw new Error('API unavailable');
      return fetchPage(url, options);
    });
    await expect(index()).rejects.toThrow('API unavailable');
    fetch.mockImplementation(fetchPage);
    expect((await index()).length).toBeGreaterThan(0);
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

  it('lists link apps beside casks under /apps and leaves command-line tools out', async () => {
    const { index, bucket } = await setup([
      { kind: 'cask', token: 'zed', locales: [{ locale: 'en-US' }] },
      { kind: 'link', token: 'lumen-notes', locales: [{ locale: 'en-US' }] },
      { kind: 'formula', token: 'wget', locales: [{ locale: 'en-US' }] }
    ]);
    const apps = (await index()).filter(entry => entry['_sitemapName'].startsWith('apps-'));
    const locs = (await Promise.all(apps.map(entry => bucket(entry['_sitemapName'])))).flat().map(entry => entry.loc);
    expect(locs).toEqual(expect.arrayContaining(['/apps/zed', '/apps/lumen-notes']));
    expect(locs).not.toContain('/apps/wget');
  });

  it('lists a bucket only when a decision puts one of its pages in a sitemap, without an index date', async () => {
    const decision = (inSitemap: boolean) => ({ overview: { inSitemap }, versions: { inSitemap } });
    const { index, bucket } = await setup([
      {
        kind: 'cask',
        token: 'dated-app',
        locales: [
          { locale: 'en-US', lastmod: '2026-01-01', pageLastmod: { overview: '2026-03-02' }, pages: decision(true) },
          { locale: 'ja-JP', lastmod: '2026-05-05', pageLastmod: { overview: '2026-05-05' }, pages: decision(false) }
        ]
      },
      {
        kind: 'cask',
        token: 'hidden-app',
        locales: [{ locale: 'en-US', lastmod: '2026-09-09', pages: decision(false) }]
      }
    ]);
    const entries = await index();
    const listed = entries.filter(({ _sitemapName: name }) => name.startsWith('apps-'));
    const urls = (await Promise.all(listed.map(({ _sitemapName: name }) => bucket(name))))
      .flat()
      .map(entry => entry.loc);
    expect(urls).toEqual(['/apps/dated-app']);
    expect(listed).toEqual([{ _sitemapName: packageSitemapName('apps', 'dated-app') }]);
    // A versions bucket follows its own decisions instead of the release-note count.
    const versions = entries.filter(({ _sitemapName: name }) => name.startsWith('versions-'));
    const versionUrls = (await Promise.all(versions.map(({ _sitemapName: name }) => bucket(name)))).flat();
    expect(versionUrls.map(entry => entry.loc)).toEqual(['/apps/dated-app/versions']);
    expect(versionUrls[0]).not.toHaveProperty('lastmod');
  });

  it('lists indexable background item labels without the date their description was last checked', async () => {
    const { bucket, fetch } = await setup([]);
    const label = (indexable: boolean) => ({
      label: 'com.example.helper',
      indexable,
      content: { evidence: { checkedAt: '2026-10-20T08:00:00Z', status: 'verified' } }
    });
    const base = fetch.getMockImplementation()!;
    fetch.mockImplementation(async (url, options) => {
      if (!url.endsWith('/background-items')) return base(url, options);
      const { locale } = (options as unknown as { query: { locale: string } }).query;
      const records = ['en-US', 'zh-CN'].includes(locale) ? [label(true)] : [label(false)];
      return { data: { total: records.length, records } };
    });
    const urls = (await bucket('background-items')) as { loc: string; lastmod?: string }[];
    expect(urls.map(url => url.loc)).toEqual([
      '/background-items/com.example.helper',
      '/zh/background-items/com.example.helper'
    ]);
    // A re-check would move checkedAt without any change to the page, so the group carries no lastmod.
    expect(urls.some(url => 'lastmod' in url)).toBe(false);
  });
});

describe('sitemap storage availability', () => {
  it.each(['manifest', 'chunk'])('retries a %s timeout once and uses the existing snapshot', async kind => {
    const { bucket, storage, packageCalls } = await setup(catalog(5));
    const expected = await bucket('apps-bucket-0');
    const calls = packageCalls();
    const getItem = storage.getItem.bind(storage);
    let failures = 0;
    vi.spyOn(storage, 'getItem').mockImplementation(async (key, options) => {
      const match = kind === 'manifest' ? key.includes('manifest') : key.includes('snapshots');
      if (match && failures++ === 0) throw new Error('Command timed out');
      return getItem(key, options);
    });
    const log = vi.spyOn(console, 'warn').mockImplementation(() => {});
    expect(await bucket('apps-bucket-0')).toEqual(expected);
    expect(packageCalls()).toBe(calls);
    expect(log).toHaveBeenCalledTimes(1);
  });

  it.each(['manifest', 'chunk'])(
    'returns 503 after two %s failures, without live rebuilding or caching an empty result',
    async kind => {
      const { bucket, storage, packageCalls } = await setup(catalog(5));
      const expected = await bucket('apps-bucket-0');
      const calls = packageCalls();
      const getItem = storage.getItem.bind(storage);
      let failures = 0;
      const read = vi.spyOn(storage, 'getItem').mockImplementation(async (key, options) => {
        if (kind === 'manifest' ? key.includes('manifest') : key.includes('snapshots')) {
          failures++;
          throw new Error('Command timed out');
        }
        return getItem(key, options);
      });
      const log = vi.spyOn(console, 'warn').mockImplementation(() => {});
      await expect(bucket('apps-bucket-0')).rejects.toMatchObject({ statusCode: 503 });
      expect(failures).toBe(2);
      expect(packageCalls()).toBe(calls);
      expect(log).toHaveBeenCalledTimes(1);
      read.mockRestore();
      expect(await bucket('apps-bucket-0')).toEqual(expected);
    }
  );

  it('builds the background group using labelPath for each language', async () => {
    const { bucket, fetch } = await setup([]);
    const labels = ['Google Drive', 'agent.*', 'agent(helper)', 'homebrew@16', 'a+b:c', 'literal%25'];
    fetch.mockResolvedValue({
      data: { total: labels.length, records: labels.map(label => ({ label, indexable: true })) }
    });
    const urls = await bucket('background-items');
    expect(urls.map(entry => entry.loc)).toEqual(
      labels.flatMap(label => LOCALES.map(locale => `${LOCALE_METADATA[locale].prefix}${labelPath(label)}`))
    );
  });
});

it.each([false, true])('browse sitemap follows fontsSitemap=%s and preserves pagination', async enabled => {
  const { bucket, fetch } = await setup([], enabled);
  const urls = await bucket('browse');
  expect(urls.some(url => url.loc.includes('/browse/a?page=2'))).toBe(true);
  expect(urls.some(url => url.loc.includes('/browse/fonts/a?page=2'))).toBe(enabled);
  const calls = fetch.mock.calls.filter(([url]) => url.endsWith('/browse'));
  expect(calls.length).toBeGreaterThan(0);
  expect(calls.some(([, options]) => options?.query?.kind === 'fonts')).toBe(enabled);
});
