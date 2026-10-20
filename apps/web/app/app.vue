<script setup lang="ts">
import { canonicalPageUrl } from '~/utils/catalogQuery';
import { LOCALES, LOCALE_METADATA, SITE_LOCALES } from '@opennavo/shared';
import { BRAND_FACTS } from '~~/shared/utils/brandFacts';
import { hreflangAliases, hreflangsFor } from '~~/shared/utils/hreflang';
import { queryNoindex, ROUTE_QUERY } from '~~/shared/utils/routeQuery';

// i18n generates html lang, canonical, hreflang and og:url (05 §6.1). Which query parameters they keep
// comes from the route's query contract (shared/utils/routeQuery.ts, S4): canonicalPageUrl drops the
// rest, defaults and page 1. Values that are not valid never get here (the pages answer 404).
const CANONICAL_QUERIES = [...new Set(Object.values(ROUTE_QUERY).flatMap(rule => rule.canonical))];
const { t, locale } = useI18n();
const route = useRoute();
const head = useLocaleHead({ seo: { canonicalQueries: CANONICAL_QUERIES } });
// Pages about one app or article limit alternates to their indexed languages (S5, usePageDecision).
const hreflangs = useState<string[] | null>('page:hreflangs', () => null);
// Every other page (lists, categories, static pages) is published in the languages open for indexing
// (L1, site settings pages.openLocales); in any other language it renders but stays out of the index.
const settings = useSiteSettings();
const openLocales = computed(() => settings.value?.pages.openLocales ?? [...LOCALES]);
const pageHreflangs = computed(() => hreflangs.value ?? hreflangsFor(openLocales.value));
const closedLocale = computed(() => hreflangs.value === null && !openLocales.value.includes(toAppLocale(locale.value)));
// The canonical (and og:url) comes from useCanonicalUrl, the same value the structured data uses as its
// @id base (T4). A page may name another path of this site as its canonical (install details point at the
// app's overview through the server's decision).
const canonical = useCanonicalUrl();
// zh-Hant also answers as zh-TW and zh-HK (L1); the sitemap writes the same set (shared/utils/hreflang.ts).
function withAliases<T extends object>(link: T): T[] {
  const hreflang = (link as { hreflang?: unknown }).hreflang;
  if (typeof hreflang !== 'string') return [link];
  return [
    link,
    ...hreflangAliases(hreflang).map(alias => ({ ...link, hreflang: alias, key: `i18n-alt-${alias}` }) as T)
  ];
}
// Sorted, filtered and prefilled variants keep their own canonical and stay out of the index (S4).
const variantNoindex = computed(() =>
  queryNoindex(route.path, new URL(route.fullPath, 'https://contract.invalid').searchParams)
);
useHead(() =>
  variantNoindex.value || closedLocale.value
    ? { meta: [{ name: 'robots', content: 'noindex, follow', tagPriority: 'high' as const }] }
    : {}
);
useHead(() => ({
  htmlAttrs: { lang: head.value.htmlAttrs.lang },
  link: (head.value.link ?? [])
    .filter(link => !('hreflang' in link) || pageHreflangs.value.includes(String(link.hreflang)))
    .flatMap(link => withAliases(link))
    .map(link =>
      typeof link.href === 'string' && (link.rel === 'canonical' || link.rel === 'alternate')
        ? {
            ...link,
            href: link.rel === 'canonical' ? canonical.value : canonicalPageUrl(link.href)
          }
        : link
    ),
  meta: [
    ...(head.value.meta ?? [])
      .filter(tag => tag.property !== 'og:locale' && tag.property !== 'og:locale:alternate')
      .map(tag =>
        tag.property === 'og:url' && typeof tag.content === 'string' ? { ...tag, content: canonical.value } : tag
      ),
    // og:locale is language_TERRITORY (zh-Hant → zh_TW), so it comes from the formatting locale.
    { property: 'og:locale', content: LOCALE_METADATA[toAppLocale(locale.value)].formatLocale.replace('-', '_') },
    // The other languages this page is published in, the same set as its hreflang alternates (T4, C1).
    ...SITE_LOCALES.filter(
      code => code !== toAppLocale(locale.value) && pageHreflangs.value.includes(LOCALE_METADATA[code].hreflang)
    ).map(code => ({ property: 'og:locale:alternate', content: LOCALE_METADATA[code].formatLocale.replace('-', '_') }))
  ]
}));
// Titles already include the brand (05 §6.1); override Nuxt SEO's lower-priority default template to avoid appending the site name again.
useHead({ titleTemplate: '%s' }, { tagPriority: 'high' });

// Site identity (WP7, T4): one Organization and the one WebSite it publishes, for the whole domain in
// every language (Google reads site names from the root), with the name, alternate names, logo and
// verified profiles set in admin. nuxt-schema-org's per-language defaults are off (nuxt.config), so these
// explicit nodes and each page's WebPage (usePageSchema, merged by @id) are the whole graph. No
// SearchAction: the sitelinks search box is gone. The description is the brand definition (G3) that the
// homepage, README and /llms.txt state too.
const siteUrl = useRuntimeConfig().public.siteUrl.replace(/\/$/, '');
const siteName = computed(() => settings.value?.identity.name || 'OpenNavo');
useSeoMeta({ ogSiteName: () => siteName.value });
useSchemaOrg(
  computed(() => {
    const identity = settings.value?.identity;
    return [
      defineOrganization({
        '@id': `${siteUrl}/#identity`,
        name: siteName.value,
        url: `${siteUrl}/`,
        logo: {
          '@type': 'ImageObject',
          '@id': `${siteUrl}/#logo`,
          url: identity?.logoUrl || `${siteUrl}/icon-512.png`
        },
        ...(identity?.sameAs.length ? { sameAs: identity.sameAs } : {})
      }),
      defineWebSite({
        '@id': `${siteUrl}/#website`,
        url: `${siteUrl}/`,
        name: siteName.value,
        description: t(BRAND_FACTS.definitionKey),
        inLanguage: SITE_LOCALES.map(code => LOCALE_METADATA[code].hreflang),
        publisher: { '@id': `${siteUrl}/#identity` },
        ...(identity?.alternateNames.length ? { alternateName: identity.alternateNames } : {})
      })
    ];
  })
);
// Every page's WebPage; pages add their type, main entity and image through usePageSchema.
usePageSchema();
</script>

<template>
  <NuxtLayout>
    <NuxtPage />
  </NuxtLayout>
  <ClientOnly><AnalyticsConsent /></ClientOnly>
</template>
