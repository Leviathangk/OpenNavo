/**
 * Header shared by every share image (08 §14): tagline and platform label in the page language,
 * plus this site's domain (NUXT_PUBLIC_SITE_URL in production), so self-hosted instances show their own.
 */
export function useOgFrame() {
  const { t } = useI18n();
  let site = '';
  try {
    site = new URL(useRuntimeConfig().public.siteUrl).host;
  } catch {
    // An invalid site URL leaves the domain out instead of failing the image.
  }
  return { tagline: t('og.tagline'), platform: t('og.platform'), site };
}
