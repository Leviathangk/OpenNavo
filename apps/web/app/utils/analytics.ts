import { webRouteType } from '@opennavo/shared';

// Configured analytics starts in every region after the page is idle, unless
// the visitor has opted out. Advertising signals remain disabled.

export type ConsentChoice = 'granted' | 'denied';
export type AnalyticsEvent = 'get_app' | 'copy_install_command' | 'open_in_app' | 'download_client';

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

/** Install-intent events (marked as key events in GA4). A no-op until analytics is active. */
export function trackEvent(event: AnalyticsEvent) {
  if (!activeId || !window.gtag) return;
  window.gtag('event', event, params());
}
