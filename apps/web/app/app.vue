<script setup lang="ts">
// i18n generates html lang and hreflang (05 §6.1).
// Canonical URLs remove query parameters except page (05 §6.1).
const { locale } = useI18n();
const head = useLocaleHead({ seo: { canonicalQueries: ['page'] } });
// Pages may limit alternates to their indexed languages (WP11, set by PackageDetailLayout).
const hreflangs = useState<string[] | null>('page:hreflangs', () => null);
useHead(() => ({
  htmlAttrs: { lang: head.value.htmlAttrs.lang },
  link: (head.value.link ?? []).filter(
    link => !hreflangs.value || !('hreflang' in link) || hreflangs.value.includes(String(link.hreflang))
  ),
  meta: [
    ...(head.value.meta ?? []).filter(tag => tag.property !== 'og:locale'),
    { property: 'og:locale', content: toAppLocale(locale.value).replace('-', '_') }
  ]
}));
// Titles already include the brand (05 §6.1); override Nuxt SEO's lower-priority default template to avoid appending the site name again.
useHead({ titleTemplate: '%s' }, { tagPriority: 'high' });

// Site identity (WP7): one Organization and the WebSite it publishes, with the name, alternate
// names, logo and verified profiles set in admin. Pages extend these nodes instead of adding more.
const settings = useSiteSettings();
const siteUrl = useRuntimeConfig().public.siteUrl.replace(/\/$/, '');
const siteName = computed(() => settings.value?.identity.name || 'OpenNavo');
useSeoMeta({ ogSiteName: () => siteName.value });
useSchemaOrg(
  computed(() => {
    const identity = settings.value?.identity;
    return [
      defineOrganization({
        name: siteName.value,
        url: `${siteUrl}/`,
        logo: identity?.logoUrl || `${siteUrl}/icon-512.png`,
        ...(identity?.sameAs.length ? { sameAs: identity.sameAs } : {})
      }),
      defineWebSite({
        name: siteName.value,
        ...(identity?.alternateNames.length ? { alternateName: identity.alternateNames } : {})
      })
    ];
  })
);
</script>

<template>
  <NuxtLayout>
    <NuxtPage />
  </NuxtLayout>
  <ClientOnly><AnalyticsConsent /></ClientOnly>
</template>
