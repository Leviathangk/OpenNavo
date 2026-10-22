import { createUsageTracker } from '@opennavo/api';
import { observeUsage } from '~/utils/usage';

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
        signal: AbortSignal.any([signal, AbortSignal.timeout(15_000)])
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
