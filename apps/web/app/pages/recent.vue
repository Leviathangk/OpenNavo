<script setup lang="ts">
import { unwrap } from '@opennavo/api';
import { OnEmpty, OnErrorState, OnPackageListView, OnPagination } from '@opennavo/ui';
import type { OnAppCardStat } from '@opennavo/ui';
import { GRID_PAGE_SIZE, pageCount as countPages } from '~/utils/catalogQuery';
import { packagePath } from '~/utils/packageView';
import { brandlessTitle } from '~/utils/seoTitles';

// Recently Updated: Discover's section in full, newest version first, with real page links. Shared
// OnPackageListView (ADR-017), Cask-only (ADR-018). Pages past the end answer 404.
const pageSize = GRID_PAGE_SIZE;
const STATS: readonly OnAppCardStat[] = ['version', 'installs30d'];

const { t, locale } = useI18n();
const api = useApi();
const localePath = useLocalePath();
const NuxtLink = resolveComponent('NuxtLink');
const { hrefWith } = useCatalogQuery({ page: 1 });
const { page, assertExists } = useListPage();

const { data, error, refresh, status } = await useAsyncData(
  () => `recent:${page.value}:${pageSize}:${locale.value}`,
  () =>
    unwrap(
      api.GET('/packages', {
        params: {
          query: { kind: 'cask', includeFonts: true, sort: 'updated', current: page.value, size: pageSize }
        }
      })
    )
);

assertExists(() => data.value?.total, pageSize, status);
const pageCount = computed(() => countPages(data.value?.total ?? 0, pageSize));

useItemListSchema(
  () =>
    error.value
      ? null
      : data.value?.records.map(pkg => ({ name: pkg.displayName, path: localePath(packagePath(pkg.token)) })),
  () => (page.value - 1) * pageSize + 1,
  () => data.value?.total
);

usePageSeo({ title: () => t('recent.metaTitle'), page, description: () => t('recent.description') });
// Share image (WP13): the list beside the latest updated apps.
defineOgImage('OgListSatori', {
  locale: toAppLocale(locale.value),
  label: t('recent.title'),
  title: brandlessTitle(t('recent.metaTitle')),
  description: t('recent.description'),
  icons: ogAppIcons(data.value?.records),
  ...useOgFrame()
});
</script>

<template>
  <OnPackageListView
    :title="t('recent.title')"
    :subtitle="t('recent.description')"
    :items="error ? [] : (data?.records ?? null)"
    :link-as="NuxtLink"
    :fit-to-width="false"
  >
    <OnErrorState v-if="error" @retry="refresh()" />
    <template #card="{ pkg }">
      <AppCard
        :pkg="pkg"
        :stats="STATS"
        :href="localePath(packagePath(pkg.token))"
        :link-state="{ onvReturn: `/recent?page=${page}` }"
      />
    </template>
    <template #empty>
      <OnEmpty v-if="!error" :title="t('catalog.emptyTitle')" />
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
