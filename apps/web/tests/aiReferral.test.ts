// @vitest-environment jsdom
import { computed, ref } from 'vue';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

type Analytics = typeof import('../app/utils/analytics');
let analytics: Analytics;
let useAnalyticsConsent: typeof import('../app/composables/useAnalyticsConsent').useAnalyticsConsent;
let settings: ReturnType<typeof ref>;
let referrer = '';

const calls = () => (window.dataLayer ?? []).map(entry => [...(entry as IArguments)]);

beforeEach(async () => {
  vi.resetModules();
  localStorage.clear();
  document.head.innerHTML = '';
  document.title = 'Visual Studio Code · OpenNavo';
  document.documentElement.lang = 'en-US';
  window.history.replaceState({}, '', '/apps/visual-studio-code?utm_source=ai#install');
  referrer = '';
  Object.defineProperty(document, 'referrer', { configurable: true, get: () => referrer });
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
  referrer = '';
  delete window.gtag;
  delete window.dataLayer;
});

function startGtag() {
  analytics.loadGtag('G-TEST');
  window.dataLayer!.length = 0;
}

describe('AI referral classification', () => {
  it.each([
    ['https://chatgpt.com/c/123', 'chatgpt.com'],
    ['https://www.chatgpt.com/', 'chatgpt.com'],
    ['https://chat.openai.com/chat', 'chat.openai.com'],
    ['https://perplexity.ai/search?q=mac', 'perplexity.ai'],
    ['https://claude.ai/new', 'claude.ai'],
    ['https://gemini.google.com/app', 'gemini.google.com'],
    ['https://copilot.microsoft.com/', 'copilot.microsoft.com'],
    ['https://deepseek.com/', 'deepseek.com'],
    ['https://grok.com/', 'grok.com'],
    ['https://x.ai/', 'x.ai'],
    ['https://you.com/search', 'you.com'],
    ['https://phind.com/search', 'phind.com']
  ])('classifies %s as %s', (url, source) => {
    expect(analytics.classifyAiReferrer(url)).toBe(source);
  });

  it('ignores non-AI, malformed and lookalike referrers', () => {
    for (const url of [
      '',
      'not a url',
      'https://example.com/chatgpt.com',
      'https://notchatgpt.com/',
      'https://chatgpt.com.example.com/',
      'https://opennavo.com/'
    ])
      expect(analytics.classifyAiReferrer(url), url).toBeNull();
  });
});

describe('AI first touch', () => {
  it('stores the source, path without query or fragment, and timestamp', () => {
    const result = analytics.captureAiReferral('https://perplexity.ai/search?q=mac', '/apps/vscode?utm=1#install');
    expect(result?.created).toBe(true);
    expect(result?.firstTouch).toMatchObject({
      source: 'perplexity.ai',
      landing_path: '/apps/vscode'
    });
    expect(Number.isNaN(Date.parse(result!.firstTouch.timestamp))).toBe(false);
    expect(JSON.parse(localStorage.getItem(analytics.AI_FIRST_TOUCH_STORAGE_KEY)!)).toEqual(result?.firstTouch);
  });

  it('keeps the first AI touch and does not overwrite it on a later visit', () => {
    const first = analytics.captureAiReferral('https://chatgpt.com/', '/first');
    const second = analytics.captureAiReferral('https://claude.ai/', '/second');
    expect(second).toEqual({ firstTouch: first!.firstTouch, created: false });
    expect(second?.firstTouch.landing_path).toBe('/first');
  });

  it('sets user properties and sends the landing event once', () => {
    startGtag();
    const result = analytics.captureAiReferral('https://chatgpt.com/c/1', '/apps/vscode');
    analytics.trackAiReferral(result);
    const sent = calls();
    expect(sent).toContainEqual([
      'set',
      'user_properties',
      { ai_first_touch_source: 'chatgpt.com', ai_first_touch_landing: '/apps/vscode' }
    ]);
    expect(sent).toContainEqual([
      'event',
      'ai_referral_landing',
      expect.objectContaining({
        ai_source: 'chatgpt.com',
        landing_path: '/apps/vscode',
        page_type: 'app'
      })
    ]);
    expect(sent.filter(call => call[1] === 'ai_referral_landing')).toHaveLength(1);

    analytics.trackAiReferral(result);
    expect(calls().filter(call => call[1] === 'ai_referral_landing')).toHaveLength(1);
    analytics.trackEvent('download_client');
    expect(calls().at(-1)).toEqual([
      'event',
      'download_client',
      { page_type: 'app', content_language: 'en-US', ai_source: 'chatgpt.com' }
    ]);
  });

  it('does not send a landing event for a previously stored first touch', () => {
    localStorage.setItem(
      analytics.AI_FIRST_TOUCH_STORAGE_KEY,
      JSON.stringify({ source: 'claude.ai', landing_path: '/old', timestamp: '2026-01-01T00:00:00.000Z' })
    );
    startGtag();
    const result = analytics.captureAiReferral('https://chatgpt.com/', '/new');
    analytics.trackAiReferral(result);
    expect(result?.created).toBe(false);
    expect(calls().some(call => call[1] === 'ai_referral_landing')).toBe(false);
    analytics.trackEvent('copy_install_command');
    expect(calls().at(-1)).toEqual([
      'event',
      'copy_install_command',
      { page_type: 'app', content_language: 'en-US', ai_source: 'claude.ai' }
    ]);
  });

  it('does not store anything before analytics consent is active', async () => {
    localStorage.setItem(analytics.CONSENT_STORAGE_KEY, 'denied');
    referrer = 'https://chatgpt.com/c/1';
    const consent = useAnalyticsConsent();
    await consent.start();
    expect(localStorage.getItem(analytics.AI_FIRST_TOUCH_STORAGE_KEY)).toBeNull();
    expect(calls()).toEqual([]);

    consent.accept();
    expect(localStorage.getItem(analytics.AI_FIRST_TOUCH_STORAGE_KEY)).not.toBeNull();
    expect(calls().some(call => call[1] === 'ai_referral_landing')).toBe(true);
  });

  it('uses the original landing path when consent is accepted later', async () => {
    localStorage.setItem(analytics.CONSENT_STORAGE_KEY, 'denied');
    referrer = 'https://chatgpt.com/c/1';
    analytics.rememberInitialReferral(referrer, '/apps/visual-studio-code');
    const consent = useAnalyticsConsent();
    await consent.start();
    window.history.replaceState({}, '', '/download');
    consent.accept();
    expect(JSON.parse(localStorage.getItem(analytics.AI_FIRST_TOUCH_STORAGE_KEY)!)).toMatchObject({
      source: 'chatgpt.com',
      landing_path: '/apps/visual-studio-code'
    });
    expect(calls()).toContainEqual([
      'event',
      'ai_referral_landing',
      expect.objectContaining({ landing_path: '/apps/visual-studio-code' })
    ]);
  });

  it('starts tracking the landing event after the default consent path', async () => {
    referrer = 'https://www.perplexity.ai/search?q=mac';
    const consent = useAnalyticsConsent();
    await consent.start();
    expect(consent.active.value).toBe(true);
    expect(JSON.parse(localStorage.getItem(analytics.AI_FIRST_TOUCH_STORAGE_KEY)!)).toMatchObject({
      source: 'perplexity.ai',
      landing_path: '/apps/visual-studio-code'
    });
    expect(calls()).toContainEqual([
      'event',
      'ai_referral_landing',
      expect.objectContaining({ ai_source: 'perplexity.ai', landing_path: '/apps/visual-studio-code' })
    ]);
  });
});

describe('GitHub outbound clicks', () => {
  it('matches only OpenNavo GitHub paths', () => {
    expect(analytics.isOpenNavoGithubUrl('https://github.com/opennavo/OpenNavo')).toBe(true);
    expect(analytics.isOpenNavoGithubUrl('https://github.com/opennavo')).toBe(true);
    expect(analytics.isOpenNavoGithubUrl('https://github.com/opennavo-evil/repo')).toBe(false);
    expect(analytics.isOpenNavoGithubUrl('https://github.com/other/OpenNavo')).toBe(false);
    expect(analytics.isOpenNavoGithubUrl('https://example.com/opennavo/OpenNavo')).toBe(false);
  });

  it('sends github_outbound_click with the repository URL', () => {
    startGtag();
    const link = document.createElement('a');
    link.href = 'https://github.com/opennavo/OpenNavo';
    link.addEventListener('click', event => event.preventDefault());
    document.body.append(link);
    analytics.trackGithubOutboundClick(link);
    expect(calls().at(-1)).toEqual([
      'event',
      'github_outbound_click',
      {
        page_type: 'app',
        content_language: 'en-US',
        link_url: 'https://github.com/opennavo/OpenNavo'
      }
    ]);
    link.remove();
  });
});

describe('Analytics plugin', () => {
  it('tracks a real click on an OpenNavo GitHub link and removes the listener on unmount', async () => {
    startGtag();
    const unmount = vi.fn();
    vi.stubGlobal('defineNuxtPlugin', (plugin: unknown) => plugin);
    vi.stubGlobal('useAnalyticsConsent', () => ({ active: ref(false), enabled: ref(false) }));
    const { default: plugin } = await import('../app/plugins/analytics.client');
    plugin({ hook: vi.fn(), vueApp: { onUnmount: unmount } } as never);

    const link = document.createElement('a');
    link.href = 'https://github.com/opennavo/OpenNavo';
    link.addEventListener('click', event => event.preventDefault());
    document.body.append(link);
    link.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
    expect(calls().at(-1)).toEqual([
      'event',
      'github_outbound_click',
      {
        page_type: 'app',
        content_language: 'en-US',
        link_url: 'https://github.com/opennavo/OpenNavo'
      }
    ]);

    unmount.mock.calls[0]![0]();
    const sent = calls().length;
    link.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
    expect(calls()).toHaveLength(sent);
    link.remove();
  });
});
