<script setup lang="ts">
import { unwrap } from '@opennavo/api';
import { formatCount } from '@opennavo/shared';
import { OnEmpty, OnErrorState, OnPackageListView, OnPagination } from '@opennavo/ui';
import { CATALOG_SORTS, readEnum, readFlag, readPage } from '~/utils/catalogQuery';
import { findCategoryPath } from '~/utils/categories';
import { packagePath } from '~/utils/packageView';

const { locale: useI18nLocale } = useI18n();
const formattingLocale = computed(() => toAppLocale(useI18nLocale.value));

// Category page (08 §10.2): shared OnPackageListView (ADR-017), breadcrumbs, title/description, sort and Show disabled,
// card grid, pagination. Cask-only (ADR-018).
const { pageSize, fitPage } = useGridPageSize();

const { t, locale } = useI18n();
const api = useApi();
const route = useRoute();
const localePath = useLocalePath();
const NuxtLink = resolveComponent('NuxtLink');
const { query, hrefWith, update } = useCatalogQuery({ page: 1, sort: 'popular', disabled: false });

const slug = computed(() => String(route.params.slug ?? ''));
const page = computed(() => readPage(query.value.page));
const sort = computed(() => readEnum(query.value.sort, CATALOG_SORTS, 'popular'));
const includeDisabled = computed(() => readFlag(query.value.disabled));

const { data: tree } = await useAsyncData(
  () => `categories:${locale.value}`,
  () => unwrap(api.GET('/categories'))
);

const path = computed(() => findCategoryPath(tree.value ?? [], slug.value));
const category = computed(() => path.value.at(-1));

if (tree.value && !category.value) {
  throw createError({ statusCode: 404, statusMessage: 'Category not found', fatal: true });
}

const { data, error, refresh } = await useAsyncData(
  () =>
    `packages:category:${slug.value}:${sort.value}:${page.value}:${pageSize.value}:${includeDisabled.value}:${locale.value}`,
  () =>
    unwrap(
      api.GET('/packages', {
        params: {
          query: {
            category: slug.value,
            kind: 'cask',
            includeFonts: true,
            sort: sort.value,
            current: page.value,
            size: pageSize.value,
            includeDisabled: includeDisabled.value
          }
        }
      })
    )
);

const pageCount = computed(() => Math.max(1, Math.ceil((data.value?.total ?? 0) / pageSize.value)));
const sorts = useCatalogSorts();
// Description and count; desktop has only count.
const subtitle = computed(() => {
  const total = data.value
    ? t(
        'catalog.total',
        { total: formatCount(data.value.total, { locale: formattingLocale.value }) },
        { plural: data.value.total }
      )
    : null;
  return [category.value?.description, total].filter(Boolean).join(' · ') || undefined;
});
const crumbs = computed(() => [
  { label: t('categories.title'), href: localePath('/categories') },
  ...path.value.map((node, index) =>
    index === path.value.length - 1
      ? { label: node.name }
      : { label: node.name, href: localePath(`/categories/${node.slug}`) }
  )
]);
useBreadcrumbSchema(crumbs);
// Share image (WP13): the category beside the first apps it lists.
if (category.value)
  defineOgImage('OgListSatori', {
    locale: formattingLocale.value,
    label: t('og.category'),
    title: category.value.name,
    description: category.value.description ?? undefined,
    icons: (data.value?.records ?? []).slice(0, 4).map(pkg => ({
      name: pkg.displayName,
      token: pkg.token,
      src: pkg.iconUrl,
      accent: pkg.accentColor
    })),
    ...useOgFrame()
  });
useItemListSchema(
  () =>
    error.value
      ? null
      : data.value?.records.map(pkg => ({ name: pkg.displayName, path: localePath(packagePath(pkg.token)) })),
  () => (page.value - 1) * pageSize.value + 1
);

// WP1: no "best" in titles; the legacy title is the rollout fallback selected in admin.
const legacyTitles = useLegacyTitles();
usePageSeo({
  title: () =>
    t(legacyTitles.value ? 'categories.itemMetaTitle' : 'categories.seo.itemTitle', {
      name: category.value?.name ?? ''
    }),
  page,
  description: () => category.value?.description ?? t('categories.description')
});
</script>

<template>
  <OnPackageListView
    v-if="category"
    :breadcrumb="crumbs"
    :title="category.name"
    :subtitle="subtitle"
    :sorts="sorts"
    :sort="sort"
    :sort-label="t('catalog.sortLabel')"
    :items="error ? [] : (data?.records ?? null)"
    :link-as="NuxtLink"
    @page-size="fitPage"
    @update:sort="update({ sort: $event })"
  >
    <template #filters>
      <DisabledToggle :model-value="includeDisabled" @update:model-value="update({ disabled: $event })" />
    </template>
    <OnErrorState v-if="error" @retry="refresh()" />
    <template #card="{ pkg }">
      <AppCard :pkg="pkg" />
    </template>
    <template #empty>
      <OnEmpty v-if="!error" :title="t('catalog.emptyTitle')" :description="t('catalog.emptyDescription')" />
    </template>
    <template #footer>
      <OnPagination
        v-if="pageCount > 1"
        class="mt-24px self-center"
        :page="page"
        :page-count="pageCount"
        :href-for="target => hrefWith({ page: target })"
        :link-as="NuxtLink"
      />
    </template>
  </OnPackageListView>
</template>
