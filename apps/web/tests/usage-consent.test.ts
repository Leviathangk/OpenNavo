// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { computed, ref } from 'vue';
import { useAnalyticsConsent } from '../app/composables/useAnalyticsConsent';

let states: Map<string, ReturnType<typeof ref>>;
let settings: ReturnType<typeof ref>;
beforeEach(() => {
  localStorage.clear();
  states = new Map();
  settings = ref({ analytics: { usageEnabled: true, ga4Enabled: false, ga4MeasurementId: '' } });
  vi.stubGlobal('computed', computed);
  vi.stubGlobal('useSiteSettings', () => settings);
  vi.stubGlobal('useState', (key: string, initialize: () => unknown) => {
    if (!states.has(key)) states.set(key, ref(initialize()));
    return states.get(key);
  });
  vi.stubGlobal('$fetch', vi.fn().mockResolvedValue({ requireConsent: true }));
});
afterEach(() => vi.unstubAllGlobals());
it('offers website usage consent independently of whether GA4 is enabled', async () => {
  const consent = useAnalyticsConsent();
  expect(consent.enabled.value).toBe(true);
  await consent.start();
  expect(consent.banner.value).toBe(true);
  expect(consent.active.value).toBe(false);
  consent.accept();
  expect(consent.active.value).toBe(true);
  expect(window.gtag).toBeUndefined();
  consent.decline();
  expect(consent.active.value).toBe(false);
  expect(localStorage.getItem('onv:analytics-consent')).toBe('denied');
});
it('honors a previously denied choice and asks when the region lookup fails', async () => {
  localStorage.setItem('onv:analytics-consent', 'denied');
  const consent = useAnalyticsConsent();
  await consent.start();
  expect(consent.active.value).toBe(false);
  localStorage.clear();
  vi.mocked($fetch).mockRejectedValueOnce(new Error('offline'));
  await consent.start();
  expect(consent.active.value).toBe(false);
  expect(consent.banner.value).toBe(true);
});
it('does not enable collection when both collection switches are off', async () => {
  settings.value.analytics.usageEnabled = false;
  const consent = useAnalyticsConsent();
  expect(consent.enabled.value).toBe(false);
  await consent.start();
  expect($fetch).not.toHaveBeenCalled();
  expect(consent.active.value).toBe(false);
});
