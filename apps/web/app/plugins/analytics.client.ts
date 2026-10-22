import { CONSENT_STORAGE_KEY, trackPageView } from '~/utils/analytics';

// Starts GA4 after the page is idle so it never competes with rendering (WP8); later navigations
// send their own page views.
export default defineNuxtPlugin(nuxtApp => {
  const consent = useAnalyticsConsent();
  const changed = (event: StorageEvent) => {
    if (event.key === CONSENT_STORAGE_KEY && event.newValue === 'denied') consent.decline();
  };
  window.addEventListener('storage', changed);
  nuxtApp.vueApp.onUnmount(() => window.removeEventListener('storage', changed));
  nuxtApp.hook('app:mounted', () => {
    if (!consent.enabled.value) return;
    const run = () => void consent.start();
    if ('requestIdleCallback' in window) window.requestIdleCallback(run, { timeout: 4000 });
    else setTimeout(run, 1500);
  });
  nuxtApp.hook('page:finish', () => {
    if (consent.active.value) trackPageView();
  });
});
