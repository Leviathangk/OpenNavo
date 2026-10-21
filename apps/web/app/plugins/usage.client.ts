import { createUsageTracker } from '@opennavo/api';
import { observeUsage } from '~/utils/usage';

export default defineNuxtPlugin(nuxtApp => {
  if (import.meta.dev) return;
  const consent = useAnalyticsConsent();
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
    canCollect: () => storage.getItem('onv:analytics-consent') !== 'denied',
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
    () => consent.active.value && settings.value?.analytics.usageEnabled,
    active => {
      stopObserve?.();
      stopObserve = undefined;
      if (active) {
        tracker.setEnabled(true);
        stopObserve = observeUsage(tracker);
      } else {
        try {
          if (storage.getItem('onv:analytics-consent') === 'denied') tracker.revoke();
        } catch {
          tracker.dispose();
        }
      }
    },
    { immediate: true }
  );
  // Revoking consent in another tab also stops this tab's collector and GA4.
  const changed = (event: StorageEvent) => {
    if (event.key === 'onv:analytics-consent' && event.newValue === 'denied') consent.decline();
  };
  window.addEventListener('storage', changed);
  nuxtApp.vueApp.onUnmount(() => {
    stopWatch();
    stopObserve?.();
    tracker.dispose();
    window.removeEventListener('storage', changed);
  });
});
