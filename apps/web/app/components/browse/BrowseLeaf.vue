<script setup lang="ts">
import { ApiError, unwrap } from '@opennavo/api';
import { LOCALES, formatCount } from '@opennavo/shared';
import { OnBreadcrumb, OnCategoryChips, OnErrorState, OnPagination } from '@opennavo/ui';
import { BROWSE_PAGE_SIZE, browsePath, bucketHeading, bucketLabel, isBrowseBucket } from '~/utils/browse';
import type { BrowseKind } from '~/utils/browse';
import { packagePath } from '~/utils/packageView';
import { brandlessTitle, withVariantNames } from '~/utils/seoTitles';
import { hreflangsFor } from '~~/shared/utils/hreflang';

// A–Z leaf page (T1, plan 4.2): one bucket of the directory, at most 300 plain links per page (no icons or
// cards, so the page stays light), sorted by the name a visitor reads. An unknown bucket, an empty bucket
// and a page past the end are all 404s; ?page= follows the route query contract (/browse/:bucket).
const props = defineProps<{ kind: BrowseKind }>();

const { t, locale } = useI18n();
const appLocale = computed(() => toAppLocale(locale.value));
const api = useApi();
const route = useRoute();
const localePath = useLocalePath();
const NuxtLink = resolveComponent('NuxtLink');
const { hrefWith } = useCatalogQuery({ page: 1 });

const notFound = () => createError({ statusCode: 404, statusMessage: 'Page not found', fatal: true });
const bucket = computed(() => String(route.params.bucket ?? ''));
if (!isBrowseBucket(bucket.value)) throw notFound();
watch(bucket, value => {
  if (!isBrowseBucket(value)) showError(notFound());
});
const { page, assertExists } = useListPage();

const { data, error, refresh, status } = await useAsyncData(
  () => `browse:${props.kind}:${bucket.value}:${page.value}:${locale.value}`,
  () =>
    unwrap(
      api.GET('/browse', {
        params: { query: { kind: props.kind, bucket: bucket.value, page: page.value, locale: appLocale.value } }
      })
    ).catch((reason: unknown) => {
      if (reason instanceof ApiError && (reason.isNotFound || reason.status === 404))
        throw createError({ statusCode: 404, statusMessage: 'Page not found' });
      throw reason;
    }),
  { transform: withVariantNames }
);
if (error.value?.statusCode === 404) throw notFound();
watch(
  () => error.value?.statusCode,
  code => {
    if (code === 404) showError(notFound());
  }
);

// Names sort into buckets by the language they are shown in, and de, fr, it and zh-Hant list translated apps only,
// so this leaf page exists only in the open languages whose directory has the same bucket and page. The hreflang
// alternates and the language menu offer just those (page:hreflangs), as browse.xml does. A language closed for
// indexing leaves page:hreflangs unset, so app.vue keeps marking it noindex. Unknown (an API error): no limit.
const settings = useSiteSettings();
const openLocales = computed(() => settings.value?.pages.openLocales ?? [...LOCALES]);
const { data: leafLocales } = await useAsyncData(
  () => `browse-locales:${props.kind}:${bucket.value}:${page.value}`,
  async () => {
    const roots = await Promise.all(
      openLocales.value.map(code =>
        unwrap(api.GET('/browse', { params: { query: { kind: props.kind, locale: code } } })).catch(() => null)
      )
    );
    if (roots.some(root => !root)) return null;
    return openLocales.value.filter((_, index) =>
      roots[index]!.buckets.some(item => item.key === bucket.value && page.value <= item.pages)
    );
  }
);
const pageHreflangs = useState<string[] | null>('page:hreflangs', () => null);
let ownedHreflangs: string[] | null = null;
const applyLeafLocales = () => {
  if (leafLocales.value && openLocales.value.includes(appLocale.value)) {
    pageHreflangs.value = hreflangsFor(leafLocales.value);
    ownedHreflangs = pageHreflangs.value;
    return;
  }
  // Unknown or closed: hand the default back, so an earlier leaf's languages do not stay behind.
  if (ownedHreflangs && pageHreflangs.value === ownedHreflangs) pageHreflangs.value = null;
  ownedHreflangs = null;
};
applyLeafLocales();
watch([leafLocales, openLocales, appLocale], applyLeafLocales);
onUnmounted(() => {
  // The next page can finish its setup before this one unmounts.
  if (pageHreflangs.value === ownedHreflangs) pageHreflangs.value = null;
});

const current = computed(() => data.value?.buckets.find(item => item.key === bucket.value));
assertExists(() => current.value?.count, BROWSE_PAGE_SIZE, status);
const pageCount = computed(() => current.value?.pages ?? 1);
const items = computed(() =>
  error.value
    ? []
    : (data.value?.items ?? []).map(item => ({
        key: item.token,
        name: item.displayName,
        href: localePath(packagePath(item.token))
      }))
);

const heading = computed(() => {
  const value = bucketHeading(props.kind, bucket.value);
  return t(value.key, value.params);
});
const isFonts = computed(() => props.kind === 'fonts');
const meta = computed(() => {
  const value = current.value;
  if (!value) return undefined;
  const count = t(
    isFonts.value ? 'browse.fontCount' : 'browse.appCount',
    { count: formatCount(value.count, { locale: appLocale.value }) },
    { plural: value.count }
  );
  return value.pages > 1 ? `${count} · ${t('browse.pageOf', { page: page.value, pages: value.pages })}` : count;
});

// The other buckets of the same directory, so every leaf links its siblings.
const letters = computed(() =>
  (data.value?.buckets ?? []).map(item => ({
    value: item.key,
    label: bucketLabel(item.key, t),
    href: localePath(browsePath(props.kind, item.key))
  }))
);

const breadcrumb = computed(() => [
  { label: t('site.name'), href: localePath('/') },
  { label: t('browse.crumb'), href: localePath('/browse') },
  ...(isFonts.value ? [{ label: t('browse.fonts'), href: localePath('/browse/fonts') }] : []),
  { label: bucketLabel(bucket.value, t) }
]);
useBreadcrumbSchema(breadcrumb);
useItemListSchema(
  () => items.value.map(item => ({ name: item.name, path: item.href })),
  () => (page.value - 1) * BROWSE_PAGE_SIZE + 1,
  () => current.value?.count
);

const description = computed(() =>
  t('browse.leafDescription', {
    title: heading.value,
    count: formatCount(current.value?.count ?? 0, { locale: appLocale.value })
  })
);
usePageSeo({
  title: () => t('browse.leafMetaTitle', { title: heading.value }),
  page,
  description: () => description.value
});
defineOgImage('OgListSatori', {
  locale: appLocale.value,
  label: t('browse.crumb'),
  title: brandlessTitle(t('browse.leafMetaTitle', { title: heading.value })),
  description: description.value,
  ...useOgFrame()
});
</script>

<template>
  <div class="box-border flex flex-col gap-20px pt-6px">
    <OnBreadcrumb :items="breadcrumb" :link-as="NuxtLink" />
    <PageHeader :title="heading" :meta="meta" />
    <OnCategoryChips
      v-if="letters.length > 1"
      :model-value="bucket"
      :items="letters"
      :aria-label="t('browse.bucketsLabel')"
      :link-as="NuxtLink"
    />
    <OnErrorState v-if="error" @retry="refresh()" />
    <ul v-else-if="items.length" class="browse-list m-0 list-none p-0">
      <li v-for="item in items" :key="item.key" class="browse-item py-4px">
        <NuxtLink
          :to="item.href"
          class="text-14px text-ink-secondary no-underline underline-offset-3 hover:text-ink-primary hover:underline"
          >{{ item.name }}</NuxtLink
        >
      </li>
    </ul>
    <OnPagination
      v-if="pageCount > 1 && !error"
      class="mt-8px self-center"
      :page="page"
      :page-count="pageCount"
      :href-for="target => hrefWith({ page: target })"
      :link-as="NuxtLink"
    />
  </div>
</template>

<style>
/* Plain names in as many columns as fit; a name never splits across two columns. */
.browse-list {
  columns: 15rem;
  column-gap: 32px;
}

.browse-item {
  break-inside: avoid;
}
</style>
