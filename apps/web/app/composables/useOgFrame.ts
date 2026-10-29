/**
 * Header shared by every share image (08 §14): tagline and platform label in the page language,
 * plus this site's domain (NUXT_PUBLIC_SITE_URL in production), so self-hosted instances show their own.
 *
 * Callers name the template in full (defineOgImage('OgListSatori', …)): only then does nuxt-og-image
 * put the template file's hash in the image URL, so an edited template gets new URLs past the
 * immutable CDN and social-site caches. The build ID also versions shared templates, fonts and tokens.
 */
export function useOgFrame() {
  const { t } = useI18n();
  const config = useRuntimeConfig();
  let site = '';
  try {
    site = new URL(config.public.siteUrl).host;
  } catch {
    // An invalid site URL leaves the domain out instead of failing the image.
  }
  return { tagline: t('og.tagline'), platform: t('og.platform'), site, build: config.app.buildId };
}

/** Up to four listed apps for a share image's icon column; without them the brand icon stands in. */
export function ogAppIcons(
  packages: readonly { displayName: string; token: string; iconUrl?: string | null; accentColor?: string | null }[] = []
) {
  return packages.slice(0, 4).map(pkg => ({
    name: pkg.displayName,
    token: pkg.token,
    src: pkg.iconUrl,
    accent: pkg.accentColor
  }));
}
