import { CONSENT_STORAGE_KEY, analyticsPlan, loadGtag, stopGtag, trackPageView } from '~/utils/analytics';
import type { ConsentChoice } from '~/utils/analytics';

// GA4 uses the banner, footer settings and stored choice.
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
    // Analytics starts in every region without a permission prompt. The footer
    // still lets visitors opt out, and a saved opt-out remains effective.
    if (analyticsPlan(storedChoice()) === 'load') activate();
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
