// @vitest-environment jsdom
import { computed, ref } from 'vue';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';

let analytics: typeof import('../app/utils/analytics');
let useAnalyticsConsent: typeof import('../app/composables/useAnalyticsConsent').useAnalyticsConsent;
let settings: ReturnType<typeof ref>;

const calls = () => (window.dataLayer ?? []).map(entry => [...(entry as IArguments)]);
const scripts = () => document.querySelectorAll('script[src^="https://www.googletagmanager.com/gtag/js"]');

beforeEach(async () => {
  vi.resetModules();
  localStorage.clear();
  document.head.innerHTML = '';
  document.title = 'Visual Studio Code · OpenNavo';
  document.documentElement.lang = 'en-US';
  window.history.replaceState({}, '', '/apps/visual-studio-code?source=private#install');
  delete window.gtag;
  delete window.dataLayer;
  delete (window as unknown as Record<string, unknown>)['ga-disable-G-TEST'];
  settings = ref({ analytics: { ga4Enabled: true, ga4MeasurementId: 'G-TEST' } });
  const state = new Map();
  vi.stubGlobal('computed', computed);
  vi.stubGlobal('useState', (key: string, init: () => unknown) => {
    if (!state.has(key)) state.set(key, ref(init()));
    return state.get(key);
  });
  vi.stubGlobal('useSiteSettings', () => settings);
  vi.stubGlobal('$fetch', vi.fn());
  analytics = await import('../app/utils/analytics');
  ({ useAnalyticsConsent } = await import('../app/composables/useAnalyticsConsent'));
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  localStorage.clear();
  for (const cookie of document.cookie.split(';'))
    document.cookie = `${cookie.split('=')[0]?.trim()}=; Max-Age=0; path=/`;
  delete window.gtag;
  delete window.dataLayer;
});

it.each([null, 'granted', 'invalid', ''])('starts GA4 without a prompt for stored choice %s', async choice => {
  if (choice !== null) localStorage.setItem(analytics.CONSENT_STORAGE_KEY, choice);
  const consent = useAnalyticsConsent();
  expect(consent.enabled.value).toBe(true);
  await consent.start();
  expect(consent.active.value).toBe(true);
  expect(consent.banner.value).toBe(false);
  expect(scripts()).toHaveLength(1);
  expect(calls()).toEqual([
    [
      'consent',
      'default',
      {
        ad_storage: 'denied',
        ad_user_data: 'denied',
        ad_personalization: 'denied',
        analytics_storage: 'granted'
      }
    ],
    ['js', expect.any(Date)],
    ['config', 'G-TEST', { send_page_view: false }],
    [
      'event',
      'page_view',
      {
        page_location: `${location.origin}/apps/visual-studio-code`,
        page_title: document.title,
        page_type: 'app',
        content_language: 'en-US'
      }
    ]
  ]);
  expect(Object.prototype.toString.call(window.dataLayer![0])).toBe('[object Arguments]');
  expect($fetch).not.toHaveBeenCalled();
});

it.each([true, false])('starts GA4 without querying region (legacy region=%s)', async region => {
  vi.mocked($fetch).mockResolvedValue({ requireConsent: region });
  const consent = useAnalyticsConsent();
  await consent.start();
  expect(consent.active.value).toBe(true);
  expect(consent.banner.value).toBe(false);
  expect(scripts()).toHaveLength(1);
  expect($fetch).not.toHaveBeenCalled();
});

it('honors a saved opt-out until the visitor accepts in footer settings', async () => {
  localStorage.setItem(analytics.CONSENT_STORAGE_KEY, 'denied');
  const consent = useAnalyticsConsent();
  await consent.start();
  analytics.trackPageView();
  analytics.trackEvent('get_app');
  expect(consent.active.value).toBe(false);
  expect(consent.banner.value).toBe(false);
  expect(scripts()).toHaveLength(0);
  expect(calls()).toEqual([]);

  consent.openSettings();
  expect(consent.banner.value).toBe(true);
  consent.accept();
  expect(localStorage.getItem(analytics.CONSENT_STORAGE_KEY)).toBe('granted');
  expect(consent.active.value).toBe(true);
  expect(consent.banner.value).toBe(false);
  expect(scripts()).toHaveLength(1);
  expect(calls().at(-1)?.slice(0, 2)).toEqual(['event', 'page_view']);
});

it('stops GA4, clears its cookies and resumes without a second script when accepted again', async () => {
  const consent = useAnalyticsConsent();
  await consent.start();
  document.cookie = '_ga=visitor; path=/';
  document.cookie = '_ga_TEST=session; path=/';
  document.cookie = 'theme=dark; path=/';
  consent.openSettings();
  consent.decline();
  expect(localStorage.getItem(analytics.CONSENT_STORAGE_KEY)).toBe('denied');
  expect(consent.active.value).toBe(false);
  expect(consent.banner.value).toBe(false);
  expect((window as unknown as Record<string, unknown>)['ga-disable-G-TEST']).toBe(true);
  expect(calls().at(-1)).toEqual(['consent', 'update', { analytics_storage: 'denied' }]);
  expect(document.cookie).toBe('theme=dark');
  const stopped = calls();
  analytics.trackPageView();
  analytics.trackEvent('download_client');
  await useAnalyticsConsent().start();
  expect(calls()).toEqual(stopped);

  consent.accept();
  expect((window as unknown as Record<string, unknown>)['ga-disable-G-TEST']).toBe(false);
  expect(scripts()).toHaveLength(1);
  expect(calls().slice(stopped.length)).toEqual([
    ['consent', 'update', { analytics_storage: 'granted' }],
    ['event', 'page_view', expect.objectContaining({ page_type: 'app' })]
  ]);
  analytics.trackPageView();
  analytics.trackEvent('download_client');
  expect(calls().at(-1)).toEqual(['event', 'download_client', { page_type: 'app', content_language: 'en-US' }]);
  expect(calls().filter(call => call[1] === 'page_view')).toHaveLength(2);
});

it('keeps GA4 and footer controls working when storage is unavailable', async () => {
  vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
    throw new Error('storage unavailable');
  });
  vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
    throw new Error('storage unavailable');
  });
  vi.mocked($fetch).mockRejectedValue(new Error('offline'));
  const consent = useAnalyticsConsent();
  await consent.start();
  expect(consent.active.value).toBe(true);
  expect(consent.banner.value).toBe(false);
  consent.decline();
  expect(consent.active.value).toBe(false);
  expect(calls().at(-1)).toEqual(['consent', 'update', { analytics_storage: 'denied' }]);
  consent.accept();
  expect(consent.active.value).toBe(true);
  expect(scripts()).toHaveLength(1);
  expect($fetch).not.toHaveBeenCalled();
});

it.each([
  { ga4Enabled: false, ga4MeasurementId: 'G-TEST' },
  { ga4Enabled: true, ga4MeasurementId: '' }
])('does not load GA4 for incomplete configuration %j', async config => {
  settings.value.analytics = config;
  const consent = useAnalyticsConsent();
  expect(consent.enabled.value).toBe(false);
  await consent.start();
  consent.accept();
  expect(consent.active.value).toBe(false);
  expect(scripts()).toHaveLength(0);
  expect(calls()).toEqual([]);
  expect($fetch).not.toHaveBeenCalled();
});

it('starts GA4 when idle, tracks navigations and honors opt-out in another tab', async () => {
  const consent = useAnalyticsConsent();
  const hooks = new Map<string, () => void>();
  const unmount = vi.fn();
  const idle = vi.fn((_callback: () => void, _options: { timeout: number }) => 1);
  vi.stubGlobal('requestIdleCallback', idle);
  vi.stubGlobal('defineNuxtPlugin', (plugin: unknown) => plugin);
  vi.stubGlobal('useAnalyticsConsent', () => consent);
  const { default: plugin } = await import('../app/plugins/analytics.client');
  await plugin({
    hook: (name: string, callback: () => void) => hooks.set(name, callback),
    vueApp: { onUnmount: unmount }
  } as never);
  hooks.get('app:mounted')!();
  expect(scripts()).toHaveLength(0);
  expect(idle).toHaveBeenCalledWith(expect.any(Function), { timeout: 4000 });
  idle.mock.calls[0]![0]();
  expect(consent.active.value).toBe(true);

  window.history.replaceState({}, '', '/download?source=private');
  hooks.get('page:finish')!();
  expect(calls().at(-1)).toEqual([
    'event',
    'page_view',
    expect.objectContaining({
      page_location: `${location.origin}/download`,
      page_type: 'download'
    })
  ]);
  window.dispatchEvent(new StorageEvent('storage', { key: analytics.CONSENT_STORAGE_KEY, newValue: 'denied' }));
  expect(consent.active.value).toBe(false);
  expect(localStorage.getItem(analytics.CONSENT_STORAGE_KEY)).toBe('denied');
  expect(calls().at(-1)).toEqual(['consent', 'update', { analytics_storage: 'denied' }]);
  const stopped = calls();
  window.history.replaceState({}, '', '/apps');
  hooks.get('page:finish')!();
  expect(calls()).toEqual(stopped);

  unmount.mock.calls[0]![0]();
  localStorage.clear();
  window.dispatchEvent(new StorageEvent('storage', { key: analytics.CONSENT_STORAGE_KEY, newValue: 'denied' }));
  expect(localStorage.getItem(analytics.CONSENT_STORAGE_KEY)).toBeNull();
});
