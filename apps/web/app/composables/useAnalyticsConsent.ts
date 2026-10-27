import { CONSENT_STORAGE_KEY, analyticsPlan, loadGtag, stopGtag, trackPageView } from '~/utils/analytics';
import type { ConsentChoice } from '~/utils/analytics';
import { geoinflowConfig, startGeoinflow, stopGeoinflow } from '~/utils/geoinflow';

// Both providers share the banner, footer settings and stored choice.
export function useAnalyticsConsent() {
  const settings = useSiteSettings();
  const banner = useState('analytics:banner', () => false);
  const active = useState('analytics:active', () => false);
  const decision = useState('analytics:decision', () => 0);
  const runtime = useRuntimeConfig();
  const geoinflow = computed(() =>
    geoinflowConfig(runtime.public.geoinflowScriptUrl, runtime.public.geoinflowWebsiteId)
  );
  const measurementId = computed(() => {
    const analytics = settings.value?.analytics;
    return analytics?.ga4Enabled && analytics.ga4MeasurementId ? analytics.ga4MeasurementId : '';
  });

  const enabled = computed(() => Boolean(measurementId.value || geoinflow.value));

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
    if (geoinflow.value) startGeoinflow(geoinflow.value);
    active.value = true;
    trackPageView();
  }

  async function start() {
    if (!enabled.value) return;
    const revision = decision.value;
    const choice = storedChoice();
    // Pages are cached and shared, so the visitor's region is asked for separately.
    const requireConsent = choice
      ? true
      : await $fetch<{ requireConsent: boolean }>('/api/consent-region').then(
          result => result.requireConsent,
          () => true
        );
    // A choice made while the region request was in flight must not be overwritten.
    if (revision !== decision.value) return;
    const plan = analyticsPlan(requireConsent, storedChoice());
    if (plan === 'load') activate();
    else if (plan === 'ask') banner.value = true;
  }

  function accept() {
    decision.value++;
    remember('granted');
    banner.value = false;
    activate();
  }

  function decline() {
    decision.value++;
    remember('denied');
    banner.value = false;
    active.value = false;
    stopGeoinflow();
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
