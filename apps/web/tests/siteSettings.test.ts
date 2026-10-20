import { describe, expect, it, vi } from 'vitest';
import type { SiteSettings } from '@opennavo/api';
import { DEFAULT_CRAWLER_POLICY } from '@opennavo/shared';
import {
  DEFAULT_SITE_SETTINGS,
  SITE_SETTINGS_RETRY_MS,
  SITE_SETTINGS_TTL_MS,
  createSiteSettingsCache
} from '../server/utils/siteSettings';

function settings(revision: number): SiteSettings {
  return { ...DEFAULT_SITE_SETTINGS, revision };
}

describe('site settings cache', () => {
  it('defaults keep the crawler policy that predates the settings page', () => {
    expect(DEFAULT_SITE_SETTINGS.crawlers).toEqual(DEFAULT_CRAWLER_POLICY);
    expect(DEFAULT_SITE_SETTINGS.analytics.ga4Enabled).toBe(false);
  });

  it('loads once per TTL and serves the stale copy while refreshing', async () => {
    let clock = 0;
    const load = vi.fn().mockResolvedValueOnce(settings(1)).mockResolvedValueOnce(settings(2));
    const get = createSiteSettingsCache(load, () => clock);
    expect((await get()).revision).toBe(1);
    clock = SITE_SETTINGS_TTL_MS - 1;
    expect((await get()).revision).toBe(1);
    expect(load).toHaveBeenCalledTimes(1);
    clock = SITE_SETTINGS_TTL_MS;
    expect((await get()).revision).toBe(1);
    await vi.waitFor(() => expect(load).toHaveBeenCalledTimes(2));
    await Promise.resolve();
    expect((await get()).revision).toBe(2);
  });

  it('falls back to defaults or the last good copy and pauses retries after a failure', async () => {
    let clock = 0;
    const load = vi.fn().mockRejectedValueOnce(new Error('offline'));
    const get = createSiteSettingsCache(load, () => clock);
    expect(await get()).toBe(DEFAULT_SITE_SETTINGS);
    expect(await get()).toBe(DEFAULT_SITE_SETTINGS);
    expect(load).toHaveBeenCalledTimes(1);
    clock = SITE_SETTINGS_RETRY_MS;
    load.mockResolvedValueOnce(settings(3));
    expect((await get()).revision).toBe(3);
    clock += SITE_SETTINGS_TTL_MS;
    load.mockRejectedValueOnce(new Error('offline'));
    expect((await get()).revision).toBe(3);
    await vi.waitFor(() => expect(load).toHaveBeenCalledTimes(3));
    expect((await get()).revision).toBe(3);
  });

  it('reloads at once on a settings invalidation and reports a failed reload (S6)', async () => {
    let clock = 0;
    const load = vi.fn().mockResolvedValueOnce(settings(5)).mockResolvedValueOnce(settings(6));
    const get = createSiteSettingsCache(load, () => clock);
    expect((await get()).revision).toBe(5);
    expect(await get.reload()).toBe(true);
    expect((await get()).revision).toBe(6);
    load.mockRejectedValueOnce(new Error('offline'));
    expect(await get.reload()).toBe(false);
    // The last good copy stays in use, and the next request tries again.
    load.mockResolvedValueOnce(settings(7));
    expect((await get()).revision).toBe(6);
    await vi.waitFor(() => expect(load).toHaveBeenCalledTimes(4));
  });

  it('opens only the six app languages for non-entity pages by default (L1)', () => {
    expect(DEFAULT_SITE_SETTINGS.pages.openLocales).toEqual(['en-US', 'zh-CN', 'ja-JP', 'es-ES', 'pt-BR', 'ru-RU']);
    expect(DEFAULT_SITE_SETTINGS.pages.detailsRedirect).toBe(false);
  });

  it('shares one request between concurrent first callers', async () => {
    let resolve!: (value: SiteSettings) => void;
    const load = vi.fn(() => new Promise<SiteSettings>(done => (resolve = done)));
    const get = createSiteSettingsCache(load, () => 0);
    const pending = Promise.all([get(), get()]);
    resolve(settings(4));
    expect((await pending).map(value => value.revision)).toEqual([4, 4]);
    expect(load).toHaveBeenCalledTimes(1);
  });
});

it('does not let an older refresh or reload overwrite an acknowledged revision', async () => {
  const resolvers: ((value: SiteSettings) => void)[] = [];
  const get = createSiteSettingsCache(
    () => new Promise(resolve => resolvers.push(resolve)),
    () => 0
  );
  const initial = get();
  const firstReload = get.reload();
  const secondReload = get.reload();
  resolvers[2]!(settings(9));
  expect(await secondReload).toBe(true);
  resolvers[1]!(settings(8));
  expect(await firstReload).toBe(true);
  resolvers[0]!(settings(7));
  expect((await initial).revision).toBe(9);
  expect((await get()).revision).toBe(9);
});

it('cold invalidation does not acknowledge a failed API load and retries', async () => {
  vi.resetModules();
  vi.stubGlobal('useRuntimeConfig', () => ({
    apiBaseInternal: 'http://api.test',
    public: { apiBase: 'http://api.test' }
  }));
  const fetch = vi
    .fn()
    .mockRejectedValueOnce(new Error('503'))
    .mockResolvedValueOnce({ data: settings(11) });
  vi.stubGlobal('$fetch', fetch);
  try {
    const { reloadSiteSettings, getSiteSettings } = await import('../server/utils/siteSettings');
    expect(await reloadSiteSettings()).toBe(false);
    expect(await reloadSiteSettings()).toBe(true);
    expect((await getSiteSettings()).revision).toBe(11);
    expect(fetch).toHaveBeenCalledTimes(2);
  } finally {
    vi.unstubAllGlobals();
  }
});
