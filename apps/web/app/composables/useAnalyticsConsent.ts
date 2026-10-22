import { CONSENT_STORAGE_KEY, analyticsPlan, loadGtag, stopGtag, trackPageView } from '~/utils/analytics';
import type { ConsentChoice } from '~/utils/analytics';

// Consent state for GA4 (WP8): the banner, the footer "Analytics settings" link and the stored choice.
export function useAnalyticsConsent() {
  const settings = useSiteSettings();
  const banner = useState('analytics:banner', () => false);
  const active = useState('analytics:active', () => false);
  const measurementId = computed(() => {
    const analytics = settings.value?.analytics;
    return analytics?.ga4Enabled && analytics.ga4MeasurementId ? analytics.ga4MeasurementId : '';
  });

  const enabled = computed(() => Boolean(measurementId.value));

  function storedChoice(): ConsentChoice | null {
    try {
      const value = localStorage.getItem(CONSENT_STORAGE_KEY);
      return value === 'granted' || value === 'denied' ? value : null;
    } catch {
      return null;
    }
  }

  function remember(choice: ConsentChoice) {
    try {
      localStorage.setItem(CONSENT_STORAGE_KEY, choice);
    } catch {
      // Private browsing may refuse storage; the choice then lasts for this page only.
    }
  }

  function activate() {
    if (!enabled.value) return;
    if (measurementId.value) loadGtag(measurementId.value);
    active.value = true;
    trackPageView();
  }

  async function start() {
    if (!enabled.value) return;
    const choice = storedChoice();
    // Pages are cached and shared, so the visitor's region is asked for separately.
    const requireConsent = choice
      ? true
      : await $fetch<{ requireConsent: boolean }>('/api/consent-region').then(
          result => result.requireConsent,
          () => true
        );
    const plan = analyticsPlan(requireConsent, choice);
    if (plan === 'load') activate();
    else if (plan === 'ask') banner.value = true;
  }

  function accept() {
    remember('granted');
    banner.value = false;
    activate();
  }

  function decline() {
    remember('denied');
    banner.value = false;
    active.value = false;
    if (measurementId.value) stopGtag(measurementId.value);
  }

  return {
    banner,
    active,
    enabled,
    start,
    accept,
    decline,
    openSettings: () => (banner.value = true)
  };
}
