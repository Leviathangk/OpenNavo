import { webRouteType } from '@opennavo/shared';

type Payload = Record<string, unknown>;
type Tracker = {
  track: (payload: Payload) => Promise<void>;
  getSession: () => { website: string };
};
export type GeoinflowConfig = { scriptUrl: string; websiteId: string };

declare global {
  interface Window {
    umami?: Tracker;
    opennavoGeoinflowBeforeSend?: (type: string, payload: Payload) => Payload | null;
  }
}

/** Deployment opt-in only. IDs in copied snippets are public collection IDs, not credentials. */
export function geoinflowConfig(scriptUrl: string, websiteId: string): GeoinflowConfig | null {
  try {
    const url = new URL(scriptUrl);
    if (url.protocol !== 'https:' || url.username || url.password || url.search || url.hash) return null;
    if (!/^[a-f\d]{8}-[a-f\d]{4}-[1-8][a-f\d]{3}-[89ab][a-f\d]{3}-[a-f\d]{12}$/i.test(websiteId)) return null;
    return { scriptUrl: url.href, websiteId };
  } catch {
    return null;
  }
}

function cleanUrl(value: string): string {
  if (!value) return '';
  try {
    const url = new URL(value, location.origin);
    if (!['https:', 'http:'].includes(url.protocol)) return '';
    return `${url.origin}${url.pathname}`;
  } catch {
    return '';
  }
}

let config: GeoinflowConfig | null = null;
let allowed = false;
let ready = false;
let script: HTMLScriptElement | undefined;
let pending: Payload[] = [];
let lastPath = '';
let referrer = '';

function flush() {
  const tracker = window.umami;
  if (!allowed || !ready || !tracker || tracker.getSession().website !== config?.websiteId) return;
  for (const payload of pending.splice(0)) void tracker.track(payload).catch(() => {});
}

export function startGeoinflow(next: GeoinflowConfig) {
  // A tracker instance is tied to its first script. Configuration changes take effect on reload.
  if (config && (config.scriptUrl !== next.scriptUrl || config.websiteId !== next.websiteId)) return;
  config = next;
  allowed = true;
  window.opennavoGeoinflowBeforeSend = (type, payload) =>
    allowed && type === 'event' && payload.website === config?.websiteId
      ? {
          ...payload,
          url: cleanUrl(String(payload.url ?? '')),
          referrer: cleanUrl(String(payload.referrer ?? ''))
        }
      : null;
  if (!script) {
    referrer = cleanUrl(document.referrer);
    script = document.createElement('script');
    script.async = true;
    script.src = next.scriptUrl;
    script.dataset.websiteId = next.websiteId;
    // Nuxt owns page views and explicit events. Do not install automatic history/click observers.
    script.dataset.autoTrack = 'false';
    script.dataset.excludeSearch = 'true';
    script.dataset.excludeHash = 'true';
    script.dataset.doNotTrack = 'true';
    script.dataset.beforeSend = 'opennavoGeoinflowBeforeSend';
    script.addEventListener('load', () => {
      ready = true;
      flush();
    });
    script.addEventListener('error', () => {
      script?.remove();
      script = undefined;
      pending = [];
      lastPath = '';
      ready = false;
    });
    document.head.append(script);
  }
  flush();
}

export function stopGeoinflow() {
  allowed = false;
  pending = [];
  lastPath = '';
  referrer = '';
}

export function trackGeoinflow(name?: string) {
  if (!allowed || !config) return;
  const path = location.pathname;
  if (!name && lastPath === path) return;
  const url = `${location.origin}${path}`;
  const payload: Payload = {
    website: config.websiteId,
    hostname: location.hostname,
    language: document.documentElement.lang,
    screen: `${window.screen.width}x${window.screen.height}`,
    title: document.title,
    url,
    referrer,
    ...(name
      ? {
          name,
          data: {
            page_type: webRouteType(path),
            content_language: document.documentElement.lang
          }
        }
      : {})
  };
  if (!name) {
    lastPath = path;
    referrer = url;
  }
  // Keep navigation usable during a blocked/slow download; discard the oldest pending events.
  pending.push(payload);
  if (pending.length > 20) pending.shift();
  flush();
}
