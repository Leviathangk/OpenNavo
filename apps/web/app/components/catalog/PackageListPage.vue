<script setup lang="ts">
import { unwrap } from '@opennavo/api';
import { formatCount } from '@opennavo/shared';
import { OnEmpty, OnErrorState, OnPackageListView, OnPagination } from '@opennavo/ui';
import { CATALOG_SORTS, GRID_PAGE_SIZE, pageCount as countPages, readEnum, readFlag } from '~/utils/catalogQuery';
import { packagePath } from '~/utils/packageView';
import { brandlessTitle } from '~/utils/seoTitles';

const { locale: useI18nLocale } = useI18n();
const formattingLocale = computed(() => toAppLocale(useI18nLocale.value));

// App list /apps (05 §4, 08 §10.2): shared OnPackageListView (ADR-017), Cask-only (ADR-018).
const pageSize = GRID_PAGE_SIZE;

const { t, locale } = useI18n();
const api = useApi();
const localePath = useLocalePath();
const NuxtLink = resolveComponent('NuxtLink');
const { query, hrefWith, update } = useCatalogQuery({ page: 1, sort: 'popular', disabled: false });

const { page, assertExists } = useListPage();
useKnownQueryValue('sort', CATALOG_SORTS);
const sort = computed(() => readEnum(query.value.sort, CATALOG_SORTS, 'popular'));
const includeDisabled = computed(() => readFlag(query.value.disabled));

const { data, error, refresh, status } = await useAsyncData(
  () => `packages:cask:${sort.value}:${page.value}:${pageSize}:${includeDisabled.value}:${locale.value}`,
  () =>
    unwrap(
      api.GET('/packages', {
        params: {
          query: {
            includeFonts: true,
            sort: sort.value,
            current: page.value,
            size: pageSize,
            includeDisabled: includeDisabled.value
          }
        }
      })
    )
);

assertExists(() => data.value?.total, pageSize, status);
const pageCount = computed(() => countPages(data.value?.total ?? 0, pageSize));
const sorts = useCatalogSorts();
const subtitle = computed(() =>
  data.value
    ? `${t('catalog.appsDescription')} · ${t('catalog.total', { total: formatCount(data.value.total, { locale: formattingLocale.value }) }, { plural: data.value.total })}`
    : t('catalog.appsDescription')
);

useItemListSchema(
  () =>
    error.value
      ? null
      : data.value?.records.map(pkg => ({ name: pkg.displayName, path: localePath(packagePath(pkg.token)) })),
  () => (page.value - 1) * pageSize + 1,
  () => data.value?.total
);

// Sorted and filtered variants stay out of the index through the route query contract (S4).
usePageSeo({
  title: () => t('catalog.appsMetaTitle'),
  page,
  description: () => t('catalog.appsDescription')
});
// Share image (WP13): the list beside the first apps it shows.
defineOgImage('OgListSatori', {
  locale: formattingLocale.value,
  label: t('catalog.appsTitle'),
  title: brandlessTitle(t('catalog.appsMetaTitle')),
  description: t('catalog.appsDescription'),
  icons: ogAppIcons(data.value?.records),
  ...useOgFrame()
});
</script>

<template>
  <OnPackageListView
    :title="t('catalog.appsTitle')"
    :subtitle="subtitle"
    :sorts="sorts"
    :sort="sort"
    :sort-label="t('catalog.sortLabel')"
    :items="error ? [] : (data?.records ?? null)"
    :link-as="NuxtLink"
    :fit-to-width="false"
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
