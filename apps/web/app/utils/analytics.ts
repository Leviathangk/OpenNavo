import { webRouteType } from '@opennavo/shared';

// Configured analytics starts in every region after the page is idle, unless
// the visitor has opted out. Advertising signals remain disabled.

export type ConsentChoice = 'granted' | 'denied';
export type AnalyticsEvent =
  | 'get_app'
  | 'copy_install_command'
  | 'open_in_app'
  | 'download_client'
  | 'github_outbound_click'
  | 'ai_referral_landing';

export type AnalyticsParams = Record<string, string | number | boolean | undefined>;

/** First-party first-touch record for an AI-assistant referral; no PII and no query strings. */
export interface AiFirstTouch {
  source: string;
  landing_path: string;
  timestamp: string;
}

export const AI_FIRST_TOUCH_STORAGE_KEY = 'onv_ai_first_touch';

const AI_REFERRER_HOSTS = [
  'chatgpt.com',
  'chat.openai.com',
  'perplexity.ai',
  'claude.ai',
  'gemini.google.com',
  'copilot.microsoft.com',
  'deepseek.com',
  'grok.com',
  'x.ai',
  'you.com',
  'phind.com'
] as const;

export const CONSENT_STORAGE_KEY = 'onv:analytics-consent';

declare global {
  interface Window {
    dataLayer?: unknown[];
    gtag?: (...args: unknown[]) => void;
  }
}

/** Analytics is enabled by default; an explicit opt-out still wins. */
export function analyticsPlan(choice: ConsentChoice | null): 'off' | 'load' {
  return choice === 'denied' ? 'off' : 'load';
}

/** The host and each parent domain, where GA may have set its first-party _ga cookies. */
export function cookieDomains(hostname: string): string[] {
  const parts = hostname.split('.');
  return parts.slice(0, -1).map((_, index) => parts.slice(index).join('.'));
}

let activeId = '';
let currentAiTouch: AiFirstTouch | null = null;
let aiReferralSent = false;
let initialReferrer = '';
let initialLandingPath = '';

function safeStorage(): Storage | null {
  try {
    return typeof localStorage === 'undefined' ? null : localStorage;
  } catch {
    return null;
  }
}

function isAiFirstTouch(value: unknown): value is AiFirstTouch {
  if (!value || typeof value !== 'object') return false;
  const record = value as Partial<AiFirstTouch>;
  return (
    typeof record.source === 'string' && typeof record.landing_path === 'string' && typeof record.timestamp === 'string'
  );
}

/** Canonical AI-assistant host for an external referrer, or null when it is not one. */
export function classifyAiReferrer(referrer: string): string | null {
  let url: URL;
  try {
    url = new URL(referrer);
  } catch {
    return null;
  }
  if (url.protocol !== 'https:' && url.protocol !== 'http:') return null;
  const host = url.hostname.toLowerCase().replace(/^www\./, '');
  return AI_REFERRER_HOSTS.find(candidate => host === candidate || host.endsWith(`.${candidate}`)) ?? null;
}

/**
 * Remember the original page view before consent can run. This stays in memory only; capture()
 * persists the record once analytics is active.
 */
export function rememberInitialReferral(referrer: string, landingPath: string) {
  initialReferrer = referrer;
  initialLandingPath = landingPath;
}

/** Read the stored first touch, falling back to this page's in-memory copy. */
export function readAiFirstTouch(): AiFirstTouch | null {
  if (currentAiTouch) return currentAiTouch;
  try {
    const stored = safeStorage()?.getItem(AI_FIRST_TOUCH_STORAGE_KEY);
    if (!stored) return null;
    const parsed: unknown = JSON.parse(stored);
    if (!isAiFirstTouch(parsed)) return null;
    currentAiTouch = parsed;
    return parsed;
  } catch {
    return null;
  }
}

function normalizeLandingPath(value: string): string {
  let path = value;
  if (/^https?:\/\//i.test(value)) {
    try {
      path = new URL(value).pathname;
    } catch {
      path = '/';
    }
  }
  const withoutQuery = (path.split(/[?#]/)[0] || '/').replace(/^([^/])/, '/$1');
  return withoutQuery || '/';
}

/**
 * Persist the first AI referral once. Call only after analytics consent is active: the record and
 * the landing event are both first-party analytics data.
 */
export function captureAiReferral(
  referrer = initialReferrer || (typeof document === 'undefined' ? '' : document.referrer),
  landingPath = initialLandingPath || (typeof location === 'undefined' ? '/' : location.pathname)
): { firstTouch: AiFirstTouch; created: boolean } | null {
  const existing = readAiFirstTouch();
  if (existing) return { firstTouch: existing, created: false };
  const source = classifyAiReferrer(referrer);
  if (!source) return null;
  const firstTouch: AiFirstTouch = {
    source,
    // Query strings can contain search terms or identifiers; store the path only.
    landing_path: normalizeLandingPath(landingPath),
    timestamp: new Date().toISOString()
  };
  currentAiTouch = firstTouch;
  try {
    safeStorage()?.setItem(AI_FIRST_TOUCH_STORAGE_KEY, JSON.stringify(firstTouch));
  } catch {
    // Private browsing may refuse storage; the record still lives for this page.
  }
  return { firstTouch, created: true };
}

/**
 * Set the first-touch user properties and send the landing event once. The event is emitted only
 * for a newly captured referral; a stored first touch is attached to later key events instead.
 */
export function trackAiReferral(result: ReturnType<typeof captureAiReferral>) {
  if (!result || !activeId || !window.gtag) return;
  const { firstTouch, created } = result;
  window.gtag('set', 'user_properties', {
    ai_first_touch_source: firstTouch.source,
    ai_first_touch_landing: firstTouch.landing_path
  });
  if (!created || aiReferralSent) return;
  aiReferralSent = true;
  window.gtag('event', 'ai_referral_landing', {
    ...params(),
    ai_source: firstTouch.source,
    landing_path: firstTouch.landing_path
  });
}

/** Whether a link points at the OpenNavo GitHub organization or one of its repositories. */
export function isOpenNavoGithubUrl(href: string): boolean {
  try {
    const url = new URL(href, location.href);
    return (
      (url.hostname === 'github.com' || url.hostname === 'www.github.com') &&
      (url.pathname.toLowerCase() === '/opennavo' || url.pathname.toLowerCase().startsWith('/opennavo/'))
    );
  } catch {
    return false;
  }
}

/** Track a click on an OpenNavo GitHub link; the plugin binds this to the document. */
export function trackGithubOutboundClick(target: EventTarget | null) {
  const element = target instanceof Element ? target.closest('a[href]') : null;
  if (!element) return;
  const href = element.getAttribute('href');
  if (!href || !isOpenNavoGithubUrl(href)) return;
  const url = new URL(href, location.href);
  trackEvent('github_outbound_click', { link_url: `${url.origin}${url.pathname}` });
}

export function loadGtag(id: string) {
  activeId = id;
  (window as unknown as Record<string, unknown>)[`ga-disable-${id}`] = false;
  if (window.gtag) {
    window.gtag('consent', 'update', { analytics_storage: 'granted' });
    return;
  }
  window.dataLayer = window.dataLayer ?? [];
  window.gtag = function gtag() {
    // gtag.js only accepts the arguments object itself, not an array copy.
    // eslint-disable-next-line prefer-rest-params
    window.dataLayer!.push(arguments);
  };
  window.gtag('consent', 'default', {
    ad_storage: 'denied',
    ad_user_data: 'denied',
    ad_personalization: 'denied',
    analytics_storage: 'granted'
  });
  window.gtag('js', new Date());
  window.gtag('config', id, { send_page_view: false });
  const script = document.createElement('script');
  script.async = true;
  script.src = `https://www.googletagmanager.com/gtag/js?id=${encodeURIComponent(id)}`;
  document.head.append(script);
}

export function stopGtag(id: string) {
  activeId = '';
  lastPageView = '';
  (window as unknown as Record<string, unknown>)[`ga-disable-${id}`] = true;
  window.gtag?.('consent', 'update', { analytics_storage: 'denied' });
  const names = document.cookie
    .split(';')
    .map(cookie => cookie.split('=')[0]?.trim() ?? '')
    .filter(name => name === '_ga' || name.startsWith('_ga_'));
  for (const name of names) {
    document.cookie = `${name}=; Max-Age=0; path=/`;
    for (const domain of cookieDomains(location.hostname))
      document.cookie = `${name}=; Max-Age=0; path=/; domain=.${domain}`;
  }
}

function params() {
  return {
    page_type: webRouteType(location.pathname),
    content_language: document.documentElement.lang
  };
}

let lastPageView = '';

/** Page views are sent manually so they carry the page type; query strings are left out. */
export function trackPageView() {
  if (!activeId || !window.gtag || lastPageView === location.pathname) return;
  lastPageView = location.pathname;
  window.gtag('event', 'page_view', {
    page_location: `${location.origin}${location.pathname}`,
    page_title: document.title,
    ...params()
  });
}

/** Key events (marked in GA4) and install-intent events. A no-op until analytics is active. */
export function trackEvent(event: AnalyticsEvent, eventParams: AnalyticsParams = {}) {
  if (!activeId || !window.gtag) return;
  const touch = readAiFirstTouch();
  window.gtag('event', event, {
    ...params(),
    ...(touch ? { ai_source: touch.source } : {}),
    ...eventParams
  });
}
