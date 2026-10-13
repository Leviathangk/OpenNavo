import { afterAll, describe, expect, it, vi } from 'vitest';
import { analyticsPlan, cookieDomains, loadGtag, stopGtag, trackEvent, trackPageView } from '../app/utils/analytics';
import { requiresConsent } from '../server/utils/consent';

const REGIONS = ['DE', 'FR', 'GB', 'CH'];

describe('analytics consent rules', () => {
  it('Asks consent regions, loads elsewhere, and always honours an explicit choice', () => {
    expect(analyticsPlan(true, null)).toBe('ask');
    expect(analyticsPlan(false, null)).toBe('load');
    expect(analyticsPlan(true, 'granted')).toBe('load');
    expect(analyticsPlan(false, 'denied')).toBe('off');
  });

  it('Treats unknown, Tor and listed countries as consent regions', () => {
    expect(requiresConsent('DE', REGIONS)).toBe(true);
    expect(requiresConsent('gb', REGIONS)).toBe(true);
    expect(requiresConsent(' US ', REGIONS)).toBe(false);
    for (const code of [undefined, '', 'XX', 'T1', 'EUR'])
      expect(requiresConsent(code, REGIONS), String(code)).toBe(true);
  });

  it('Finds every domain that may hold _ga cookies', () => {
    expect(cookieDomains('www.opennavo.com')).toEqual(['www.opennavo.com', 'opennavo.com']);
    expect(cookieDomains('localhost')).toEqual([]);
  });
});

describe('gtag loader', () => {
  const cookies: string[] = [];
  const appended: { src: string; async: boolean }[] = [];
  const page = {
    pathname: '/zh/apps/visual-studio-code',
    origin: 'https://opennavo.example',
    hostname: 'opennavo.example'
  };
  vi.stubGlobal('window', globalThis);
  vi.stubGlobal('location', page);
  vi.stubGlobal('document', {
    title: 'Visual Studio Code · OpenNavo',
    documentElement: { lang: 'zh-CN' },
    createElement: () => ({ src: '', async: false }),
    head: { append: (node: { src: string; async: boolean }) => appended.push(node) },
    get cookie() {
      return '_ga=GA1.1.1; theme=dark; _ga_ABC=GS1.1';
    },
    set cookie(value: string) {
      cookies.push(value);
    }
  });
  afterAll(() => vi.unstubAllGlobals());
  const calls = () => (window.dataLayer ?? []).map(entry => [...(entry as IArguments)]);

  it('Sends nothing before consent and denies ad signals once loaded', () => {
    trackEvent('get_app');
    trackPageView();
    expect(window.dataLayer).toBeUndefined();
    loadGtag('G-TEST1');
    expect(appended).toEqual([{ src: 'https://www.googletagmanager.com/gtag/js?id=G-TEST1', async: true }]);
    expect(calls()[0]).toEqual([
      'consent',
      'default',
      { ad_storage: 'denied', ad_user_data: 'denied', ad_personalization: 'denied', analytics_storage: 'granted' }
    ]);
    expect(calls()[2]).toEqual(['config', 'G-TEST1', { send_page_view: false }]);
  });

  it('Sends page views without query strings and events with the page type only', () => {
    trackPageView();
    trackPageView();
    trackEvent('copy_install_command');
    const sent = calls().slice(3);
    expect(sent).toEqual([
      [
        'event',
        'page_view',
        {
          page_location: 'https://opennavo.example/zh/apps/visual-studio-code',
          page_title: 'Visual Studio Code · OpenNavo',
          page_type: 'app',
          content_language: 'zh-CN'
        }
      ],
      ['event', 'copy_install_command', { page_type: 'app', content_language: 'zh-CN' }]
    ]);
  });

  it('Stops sending and clears _ga cookies after Don’t allow', () => {
    stopGtag('G-TEST1');
    expect((window as unknown as Record<string, unknown>)['ga-disable-G-TEST1']).toBe(true);
    expect(calls().at(-1)).toEqual(['consent', 'update', { analytics_storage: 'denied' }]);
    expect(cookies).toEqual([
      '_ga=; Max-Age=0; path=/',
      '_ga=; Max-Age=0; path=/; domain=.opennavo.example',
      '_ga_ABC=; Max-Age=0; path=/',
      '_ga_ABC=; Max-Age=0; path=/; domain=.opennavo.example'
    ]);
    const before = calls().length;
    trackEvent('open_in_app');
    expect(calls().length).toBe(before);
    loadGtag('G-TEST1');
    expect(appended.length).toBe(1);
    expect(calls().at(-1)).toEqual(['consent', 'update', { analytics_storage: 'granted' }]);
  });
});
