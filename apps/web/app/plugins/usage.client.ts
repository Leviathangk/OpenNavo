import { createUsageTracker } from '@opennavo/api';
import { observeUsage } from '~/utils/usage';

/** Bound one request in time without assuming either newer abort API exists. The plugin already
 * requires `navigator.locks` (Safari 15.4), but `AbortSignal.timeout` arrives in Safari 16 and
 * `AbortSignal.any` in 17.4 (Chrome 116): calling one that is missing throws before the request is
 * even made, and a visit that got this far would then never be reported. Without `any`, the deadline
 * alone decides, which is the same 15 s bound with a coarser cancellation. */
function requestSignal(signal: AbortSignal, milliseconds: number) {
  const deadline: AbortSignal =
    typeof AbortSignal.timeout === 'function'
      ? AbortSignal.timeout(milliseconds)
      : (() => {
          const controller = new AbortController();
          setTimeout(() => controller.abort(), milliseconds);
          return controller.signal;
        })();
  return typeof AbortSignal.any === 'function' ? AbortSignal.any([signal, deadline]) : deadline;
}

export default defineNuxtPlugin(nuxtApp => {
  if (import.meta.dev) return;
  const settings = useSiteSettings();
  const api = useApi();
  let storage: Storage;
  try {
    storage = localStorage;
  } catch {
    return;
  }
  // Without cross-tab locking, a shared outbox cannot safely preserve offline history.
  if (!navigator.locks) return;
  const tracker = createUsageTracker({
    platform: 'web',
    storage,
    withLock: (name, action) => navigator.locks.request(name, action),
    version: () => '',
    locale: () => nuxtApp.$i18n.locale.value,
    send: async (body, signal) => {
      const result = await api.POST('/usage/activity', {
        body,
        signal: requestSignal(signal, 15_000)
      });
      if (result.response.status !== 204) throw new Error('Usage report unavailable');
      return result.response.headers.get('X-Usage-Day') ?? '';
    }
  });
  let stopObserve: (() => void) | undefined;
  const stopWatch = watch(
    () => settings.value?.analytics.usageEnabled ?? true,
    active => {
      stopObserve?.();
      stopObserve = undefined;
      if (active) {
        tracker.setEnabled(true);
        stopObserve = observeUsage(tracker);
      } else {
        tracker.dispose();
      }
    },
    { immediate: true }
  );
  nuxtApp.vueApp.onUnmount(() => {
    stopWatch();
    stopObserve?.();
    tracker.dispose();
  });
});
