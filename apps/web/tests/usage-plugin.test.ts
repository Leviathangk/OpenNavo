// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { nextTick, ref, watch } from 'vue';
import { observeUsage } from '../app/utils/usage';

vi.mock('../app/utils/usage', () => ({ observeUsage: vi.fn(() => () => {}) }));
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  localStorage.clear();
});

async function start(afterStart?: () => void) {
  const settings = ref({ analytics: { usageEnabled: true } });
  let lock = Promise.resolve();
  vi.stubGlobal('navigator', {
    locks: {
      request: (_name: string, action: () => void) => {
        const next = lock.then(action);
        lock = next.catch(() => {});
        return next;
      }
    }
  });
  vi.stubGlobal('defineNuxtPlugin', (fn: unknown) => fn);
  vi.stubGlobal('useSiteSettings', () => settings);
  vi.stubGlobal('watch', watch);
  const post = vi.fn().mockResolvedValue({
    response: new Response(null, {
      status: 204,
      headers: { 'X-Usage-Day': new Date(Date.now() + 8 * 3_600_000).toISOString().slice(0, 10) }
    })
  });
  vi.stubGlobal('useApi', () => ({ POST: post }));
  const unmount = vi.fn();
  const { default: plugin } = await import('../app/plugins/usage.client');
  await plugin({ $i18n: { locale: ref('en-US') }, vueApp: { onUnmount: unmount } } as never);
  afterStart?.();
  return { settings, post, unmount };
}

it('starts first-party usage without consent, honors the operator switch and cleans up', async () => {
  localStorage.setItem('onv:analytics-consent', 'denied');
  const { settings, post, unmount } = await start();
  expect(observeUsage).toHaveBeenCalledTimes(1);
  const tracker = vi.mocked(observeUsage).mock.calls[0]![0];
  await tracker.activity();
  expect(post).toHaveBeenCalledTimes(1);
  settings.value.analytics.usageEnabled = false;
  await nextTick();
  await tracker.activity();
  expect(post).toHaveBeenCalledTimes(1);
  unmount.mock.calls[0]![0]();
});

// Safari 15.4 has navigator.locks but neither AbortSignal.timeout (16) nor AbortSignal.any (17.4).
// A visit must still be reported, with the 15 s deadline as the only bound.
it('reports when the newer abort APIs are missing', async () => {
  const { AbortSignal: Original } = globalThis;
  // Own statics shadow the inherited ones, so this class really lacks both methods.
  class LegacyAbortSignal extends Original {
    static any = undefined;
    static timeout = undefined;
  }
  vi.stubGlobal('AbortSignal', LegacyAbortSignal);
  const { post } = await start();
  expect(observeUsage).toHaveBeenCalledTimes(1);
  await vi.mocked(observeUsage).mock.calls[0]![0].activity();
  expect(post).toHaveBeenCalledTimes(1);
  expect(post.mock.calls[0]![1].signal).toBeInstanceOf(Original);
});

it('does not report a response the server did not acknowledge', async () => {
  const { post } = await start();
  post.mockResolvedValueOnce({ response: new Response(null, { status: 202 }) });
  await vi.mocked(observeUsage).mock.calls[0]![0].activity();
  expect(post).toHaveBeenCalledTimes(1);
  expect(JSON.parse(localStorage.getItem('onv:usage:v1:web')!).pending).toHaveLength(1);
});
