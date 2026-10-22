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

it('starts first-party usage without consent, honors the operator switch and cleans up', async () => {
  const settings = ref({ analytics: { usageEnabled: true } });
  localStorage.setItem('onv:analytics-consent', 'denied');
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
