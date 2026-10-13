import type { SiteSettings } from '@opennavo/api';

// Copies the admin SEO settings from the SSR request context into hydrated state and adds the
// search-engine ownership tags to the root home page; DNS-verified engines leave the codes empty.
export default defineNuxtPlugin(() => {
  const settings = useSiteSettings();
  if (import.meta.server) {
    settings.value = (useRequestEvent()?.context.siteSettings as SiteSettings | undefined) ?? null;
  }
  const route = useRouter().currentRoute;
  useHead(() => {
    const codes = settings.value?.verification;
    if (!codes || route.value.path !== '/') return {};
    return {
      meta: [
        ...(codes.google ? [{ name: 'google-site-verification', content: codes.google }] : []),
        ...(codes.bing ? [{ name: 'msvalidate.01', content: codes.bing }] : [])
      ]
    };
  });
});
