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
