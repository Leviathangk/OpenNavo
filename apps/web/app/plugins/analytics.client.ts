import {
  CONSENT_STORAGE_KEY,
  rememberInitialReferral,
  trackGithubOutboundClick,
  trackPageView
} from '~/utils/analytics';

// Starts configured analytics after the page is idle so it never competes with rendering; later navigations
// send their own page views.
export default defineNuxtPlugin(nuxtApp => {
  rememberInitialReferral(document.referrer, location.pathname);
  const consent = useAnalyticsConsent();
  const changed = (event: StorageEvent) => {
    if (event.key === CONSENT_STORAGE_KEY && event.newValue === 'denied') consent.decline();
  };
  const clicked = (event: MouseEvent) => trackGithubOutboundClick(event.target);
  window.addEventListener('storage', changed);
  document.addEventListener('click', clicked);
  nuxtApp.vueApp.onUnmount(() => {
    window.removeEventListener('storage', changed);
    document.removeEventListener('click', clicked);
  });
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
