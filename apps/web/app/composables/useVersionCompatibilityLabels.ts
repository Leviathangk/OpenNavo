import type { OnVersionCompatibilityLabels } from '@opennavo/ui';

/**
 * Copy for the per-version macOS compatibility row (F6) of OnVersionTimeline / OnVersionEntry in the page
 * language: pass it as compatibility-labels. The bound templates keep their {version} placeholder, which
 * the component fills for each release; a release without sourced facts reads "unknown".
 */
export function useVersionCompatibilityLabels() {
  const { t } = useI18n();
  return computed<OnVersionCompatibilityLabels>(() => ({
    title: t('package.release.compatibility'),
    unknown: t('package.release.compatibilityUnknown'),
    minimum: t('package.facts.minimum', { version: '{version}' }),
    maximum: t('package.facts.maximum', { version: '{version}' }),
    appleSilicon: {
      yes: t('package.alternatives.reasons.apple_silicon'),
      no: t('package.alternatives.limits.no_apple_silicon')
    }
  }));
}
