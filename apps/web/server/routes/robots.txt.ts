import { LOCALES, buildRobotsTxt } from '@opennavo/shared';

// robots.txt follows the admin crawler policy at runtime (WP6/WP24); the admin preview calls the
// same builder. @nuxtjs/robots still applies the default-group rules to page meta and headers.
export default defineEventHandler(async event => {
  const settings = await getSiteSettings();
  setHeader(event, 'Content-Type', 'text/plain; charset=utf-8');
  setHeader(event, 'Cache-Control', import.meta.dev ? 'no-store' : 'public, max-age=300');
  return buildRobotsTxt({
    crawlers: settings.crawlers,
    indexable: getSiteIndexable(event),
    // Same fallback as the sitemaps: the six app languages until settings name the open ones.
    openLocales: settings.pages.openLocales ?? LOCALES,
    // NUXT_PUBLIC_SITE_URL, the same origin the sitemap module writes into its URLs.
    sitemap: `${useRuntimeConfig(event).public.siteUrl.replace(/\/$/, '')}/sitemap_index.xml`
  });
});
