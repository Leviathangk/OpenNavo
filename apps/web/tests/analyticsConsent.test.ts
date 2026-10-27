import { computed, ref } from 'vue';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { useAnalyticsConsent } from '../app/composables/useAnalyticsConsent';

const providers = vi.hoisted(() => ({
  start: vi.fn(),
  stop: vi.fn(),
  load: vi.fn(),
  stopGtag: vi.fn(),
  page: vi.fn()
}));
vi.mock('../app/utils/geoinflow', () => ({
  geoinflowConfig: (url: string, id: string) => (url && id ? { scriptUrl: url, websiteId: id } : null),
  startGeoinflow: providers.start,
  stopGeoinflow: providers.stop
}));
vi.mock('../app/utils/analytics', () => ({
  CONSENT_STORAGE_KEY: 'consent',
  analyticsPlan: (choice: string) => (choice === 'denied' ? 'off' : 'load'),
  loadGtag: providers.load,
  stopGtag: providers.stopGtag,
  trackPageView: providers.page
}));

beforeEach(() => {
  vi.clearAllMocks();
  const state = new Map();
  const storage = new Map();
  vi.stubGlobal('computed', computed);
  vi.stubGlobal('useState', (key: string, init: () => unknown) => {
    if (!state.has(key)) state.set(key, ref(init()));
    return state.get(key);
  });
  vi.stubGlobal('useSiteSettings', () => ref({ analytics: { ga4Enabled: false } }));
  vi.stubGlobal('useRuntimeConfig', () => ({
    public: {
      geoinflowScriptUrl: 'https://stats.example/script.js',
      geoinflowWebsiteId: 'test'
    }
  }));
  vi.stubGlobal('localStorage', {
    getItem: (key: string) => storage.get(key),
    setItem: (key: string, value: string) => storage.set(key, value)
  });
});
afterEach(() => vi.unstubAllGlobals());

it.each([true, false])('starts GEOInflow without querying region (legacy region=%s)', async region => {
  vi.stubGlobal('$fetch', vi.fn().mockResolvedValue({ requireConsent: region }));
  const consent = useAnalyticsConsent();
  expect(consent.enabled.value).toBe(true);
  await consent.start();
  expect(consent.banner.value).toBe(false);
  expect(providers.start).toHaveBeenCalledOnce();
  expect(providers.load).not.toHaveBeenCalled();
  expect($fetch).not.toHaveBeenCalled();
  consent.decline();
  expect(providers.stop).toHaveBeenCalledOnce();
  expect(consent.active.value).toBe(false);
  await consent.start();
  expect(providers.start).toHaveBeenCalledOnce();
});

it('starts both configured providers even when storage and region lookup are unavailable', async () => {
  vi.stubGlobal('useSiteSettings', () => ref({ analytics: { ga4Enabled: true, ga4MeasurementId: 'G-TEST' } }));
  vi.stubGlobal('$fetch', vi.fn().mockRejectedValue(new Error('offline')));
  vi.stubGlobal('localStorage', {
    getItem: () => {
      throw Error();
    }
  });
  const consent = useAnalyticsConsent();
  await consent.start();
  expect(consent.active.value).toBe(true);
  expect(consent.banner.value).toBe(false);
  expect(providers.start).toHaveBeenCalledOnce();
  expect(providers.load).toHaveBeenCalledWith('G-TEST');
  expect($fetch).not.toHaveBeenCalled();
});
