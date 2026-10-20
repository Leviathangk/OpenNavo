import type { SiteSettings } from '@opennavo/api';
import { BRAND, DEFAULT_CRAWLER_POLICY, LOCALES } from '@opennavo/shared';

export const SITE_SETTINGS_TTL_MS = 60_000;
export const SITE_SETTINGS_RETRY_MS = 10_000;

// Same values as store.DefaultSEOSettings in apps/server. Used until the API first answers and
// whenever it is unreachable, so robots.txt and page metadata keep serving the previous policy.
export const DEFAULT_SITE_SETTINGS: SiteSettings = {
  revision: 0,
  identity: {
    name: 'OpenNavo',
    alternateNames: [],
    logoUrl: '',
    sameAs: [BRAND.repository, 'https://x.com/opennavo']
  },
  verification: { google: '', bing: '' },
  analytics: {
    ga4Enabled: false,
    ga4MeasurementId: '',
    // EU/EEA, the UK and Switzerland: analytics cookies wait for consent.
    consentRegions:
      'AT BE BG HR CY CZ DK EE FI FR DE GR HU IE IT LV LT LU MT NL PL PT RO SK SI ES SE IS LI NO GB CH'.split(' ')
  },
  edgeCache: { enabled: false },
  crawlers: DEFAULT_CRAWLER_POLICY,
  pages: {
    titleTemplate: 'new',
    indexPolicy: { mode: 'shadow', scope: 'new', since: '' },
    translationMode: 'shadow',
    versionsMinNotesChars: 300,
    // Non-entity pages open for indexing (L1): the six app languages until the new ones are ready.
    openLocales: [...LOCALES],
    intentPagesEnabled: false,
    detailsRedirect: false,
    versionsSitemap: false
  }
};

// Keeps one copy per process. A stale copy is served while it refreshes in the background, so
// only the very first request waits; failures retry after a short pause instead of on every hit.
export function createSiteSettingsCache(load: () => Promise<SiteSettings>, now: () => number = Date.now) {
  let value: SiteSettings | undefined;
  let expires = 0;
  let pending: Promise<SiteSettings> | undefined;
  const accept = (next: SiteSettings) => {
    if (!value || next.revision >= value.revision) value = next;
    expires = now() + SITE_SETTINGS_TTL_MS;
    return value;
  };
  /**
   * Drop the copy and load a fresh one now (S6: the API's settings invalidation). True once the new
   * settings are in; false when the reload failed, so the caller keeps its notification and retries.
   */
  const reload = async (): Promise<boolean> => {
    expires = 0;
    try {
      accept(await load());
      return true;
    } catch {
      return false;
    }
  };
  const refresh = () =>
    (pending ??= load()
      .then(
        next => {
          return accept(next);
        },
        () => {
          expires = now() + SITE_SETTINGS_RETRY_MS;
          return value ?? DEFAULT_SITE_SETTINGS;
        }
      )
      .finally(() => {
        pending = undefined;
      }));
  const get = async (): Promise<SiteSettings> => {
    if (expires > now()) return value ?? DEFAULT_SITE_SETTINGS;
    if (!value) return refresh();
    void refresh();
    return value;
  };
  return Object.assign(get, { reload });
}

let cached: ReturnType<typeof createSiteSettingsCache> | undefined;

function settingsCache() {
  cached ??= createSiteSettingsCache(async () => {
    const config = useRuntimeConfig();
    const base = (config.apiBaseInternal || config.public.apiBase).replace(/\/$/, '');
    const response = await $fetch<{ data: SiteSettings }>(`${base}/site/settings`, {
      timeout: 3000
    });
    if (!response?.data?.crawlers) throw new Error('invalid site settings response');
    return response.data;
  });
  return cached;
}

export function getSiteSettings(): Promise<SiteSettings> {
  return settingsCache()();
}

/** Acknowledges invalidation only after a successful API load, including cold starts. */
export function reloadSiteSettings(): Promise<boolean> {
  return settingsCache().reload();
}
