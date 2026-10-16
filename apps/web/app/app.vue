<script setup lang="ts">
import { canonicalPageUrl } from '~/utils/catalogQuery';

// i18n generates html lang and hreflang (05 §6.1).
// Canonical URLs retain page, size and the disabled filter that change list bounds; page only from page 2 on:
// ?page=1 is the list itself. Values that are not page numbers never get here (useListPage: 404).
const { locale } = useI18n();
const head = useLocaleHead({ seo: { canonicalQueries: ['page', 'disabled', 'size'] } });
// Pages may limit alternates to their indexed languages (WP11, set by PackageDetailLayout).
const hreflangs = useState<string[] | null>('page:hreflangs', () => null);
useHead(() => ({
  htmlAttrs: { lang: head.value.htmlAttrs.lang },
  link: (head.value.link ?? [])
    .filter(link => !hreflangs.value || !('hreflang' in link) || hreflangs.value.includes(String(link.hreflang)))
    .map(link =>
      typeof link.href === 'string' && (link.rel === 'canonical' || link.rel === 'alternate')
        ? { ...link, href: canonicalPageUrl(link.href) }
        : link
    ),
  meta: [
    ...(head.value.meta ?? [])
      .filter(tag => tag.property !== 'og:locale')
      .map(tag =>
        tag.property === 'og:url' && typeof tag.content === 'string'
          ? { ...tag, content: canonicalPageUrl(tag.content) }
          : tag
      ),
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
