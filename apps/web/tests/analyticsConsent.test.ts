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
  analyticsPlan: (region: boolean, choice: string) =>
    choice === 'denied' ? 'off' : choice === 'granted' || !region ? 'load' : 'ask',
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

it('supports GEOInflow without GA4 and waits for consent in consent regions', async () => {
  vi.stubGlobal('$fetch', vi.fn().mockResolvedValue({ requireConsent: true }));
  const consent = useAnalyticsConsent();
  expect(consent.enabled.value).toBe(true);
  await consent.start();
  expect(consent.banner.value).toBe(true);
  expect(providers.start).not.toHaveBeenCalled();
  consent.accept();
  expect(providers.start).toHaveBeenCalledOnce();
  expect(providers.load).not.toHaveBeenCalled();
  consent.decline();
  expect(providers.stop).toHaveBeenCalledOnce();
  expect(consent.active.value).toBe(false);
});

it('does not override a refusal while the region request is pending, even without localStorage', async () => {
  let finish!: (value: { requireConsent: boolean }) => void;
  vi.stubGlobal(
    '$fetch',
    () =>
      new Promise(resolve => {
        finish = resolve;
      })
  );
  vi.stubGlobal('localStorage', {
    getItem: () => {
      throw Error();
    },
    setItem: () => {
      throw Error();
    }
  });
  const consent = useAnalyticsConsent();
  const pending = consent.start();
  consent.decline();
  finish({ requireConsent: false });
  await pending;
  expect(consent.active.value).toBe(false);
  expect(providers.start).not.toHaveBeenCalled();
});
