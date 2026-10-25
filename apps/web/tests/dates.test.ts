// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createSSRApp, defineComponent, h, nextTick, ref } from 'vue';
import type { Ref } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { formatDate, formatRelativeTime, formatTime, setDateDefaults } from '@opennavo/shared';

type FakeApp = { state: Record<string, Ref<unknown>> };

vi.stubGlobal('defineNuxtPlugin', (plugin: unknown) => plugin);
const { default: plugin } = (await import('../app/plugins/dates')) as unknown as {
  default: (app: FakeApp) => void;
};
vi.unstubAllGlobals();

// Nuxt as the plugin sees it: useState and tryUseNuxtApp answer for the app being set up or rendered.
let current: FakeApp | null = null;
let ready: Array<() => void> = [];

function start(payload: Record<string, unknown> = {}): FakeApp {
  const app: FakeApp = { state: Object.fromEntries(Object.entries(payload).map(([key, value]) => [key, ref(value)])) };
  current = app;
  plugin(app);
  return app;
}

const published = '2026-10-24T20:00:00Z';
const text = () => `${formatRelativeTime(published)} · ${formatDate(published)} ${formatTime(published)}`;
const Release = defineComponent(() => () => h('p', text()));

beforeEach(() => {
  vi.useFakeTimers({ toFake: ['Date'] });
  vi.stubGlobal('useState', (key: string, init: () => unknown) => (current!.state[key] ??= ref(init())));
  vi.stubGlobal('tryUseNuxtApp', () => current);
  vi.stubGlobal('onNuxtReady', (callback: () => void) => ready.push(callback));
});

afterEach(() => {
  setDateDefaults({});
  current = null;
  ready = [];
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.unstubAllEnvs();
  vi.restoreAllMocks();
});

describe('Dates on cached pages', () => {
  it('hydrate as the server rendered them, then catch up with the visitor’s clock', async () => {
    // The server renders in UTC; the visitor opens the cached page six hours later in UTC+8.
    vi.stubEnv('TZ', 'UTC');
    vi.setSystemTime(new Date('2026-10-24T23:30:00Z'));
    const server = start();
    const html = await renderToString(createSSRApp(Release));
    expect(html).toBe('<p>3 hours ago · Oct 24 20:00 UTC</p>');

    vi.stubEnv('TZ', 'Asia/Shanghai');
    vi.setSystemTime(new Date('2026-10-25T05:30:00Z'));
    start({ 'onv:rendered-at': server.state['onv:rendered-at']!.value });
    const container = document.createElement('div');
    container.innerHTML = html;
    const warn = vi.spyOn(console, 'warn');
    const error = vi.spyOn(console, 'error');
    createSSRApp(Release).mount(container);
    const messages = [...warn.mock.calls, ...error.mock.calls].flat().join('\n');
    expect(messages).not.toMatch(/hydration|mismatch/i);
    expect(container.textContent).toBe('3 hours ago · Oct 24 20:00 UTC');

    // Relative times catch up with the clock; dates and times stay in UTC.
    ready.forEach(callback => callback());
    await nextTick();
    expect(container.textContent).toBe('9 hours ago · Oct 24 20:00 UTC');
    // Without the defaults the browser alone would have written a different text.
    setDateDefaults({});
    expect(text()).toBe('9 hours ago · Oct 25 04:00');
  });

  it('measure each request from its own render time, and anything else from the clock', () => {
    vi.setSystemTime(new Date('2026-10-24T21:00:00Z'));
    const first = start();
    vi.setSystemTime(new Date('2026-10-24T22:00:00Z'));
    const second = start();
    vi.setSystemTime(new Date('2026-10-24T23:00:00Z'));
    current = first;
    expect(formatRelativeTime(published)).toBe('1 hour ago');
    current = second;
    expect(formatRelativeTime(published)).toBe('2 hours ago');
    current = null;
    expect(formatRelativeTime(published)).toBe('3 hours ago');
  });

  it('leave pages the browser renders itself on its clock', () => {
    vi.setSystemTime(new Date('2026-10-24T21:00:00Z'));
    start();
    vi.setSystemTime(new Date('2026-10-24T23:00:00Z'));
    ready.forEach(callback => callback());
    expect(formatRelativeTime(published)).toBe('3 hours ago');
  });
});
