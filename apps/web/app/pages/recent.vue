<script setup lang="ts">
import { withRecentReturn } from '@opennavo/shared';
import { unwrap } from '@opennavo/api';
import { OnEmpty, OnErrorState, OnPackageListView, OnPagination } from '@opennavo/ui';
import type { OnAppCardStat } from '@opennavo/ui';
import { pageCount as countPages } from '~/utils/catalogQuery';
import { packagePath } from '~/utils/packageView';
import { brandlessTitle } from '~/utils/seoTitles';

// Recently Updated: Discover's section in full, newest version first, with real page links. Shared
// OnPackageListView (ADR-017), Cask-only (ADR-018). Pages past the end answer 404.
const { pageSize, customSize, fitPage } = useGridPageSize();
const STATS: readonly OnAppCardStat[] = ['version', 'installs30d'];

const { t, locale } = useI18n();
const api = useApi();
const localePath = useLocalePath();
const NuxtLink = resolveComponent('NuxtLink');
const { hrefWith } = useCatalogQuery({ page: 1 });
const { page, assertExists } = useListPage({ canonical: ['size'] });

const { data, error, refresh, status } = await useAsyncData(
  () => `recent:${page.value}:${pageSize.value}:${locale.value}`,
  () =>
    unwrap(
      api.GET('/packages', {
        params: {
          query: { kind: 'cask', includeFonts: true, sort: 'updated', current: page.value, size: pageSize.value }
        }
      })
    )
);

assertExists(() => data.value?.total, pageSize, status);
const pageCount = computed(() => countPages(data.value?.total ?? 0, pageSize.value));

useItemListSchema(
  () =>
    error.value
      ? null
      : data.value?.records.map(pkg => ({ name: pkg.displayName, path: localePath(packagePath(pkg.token)) })),
  () => (page.value - 1) * pageSize.value + 1
);

// Other page sizes retain their own canonical but stay out of the index.
useSeoMeta({ robots: () => (customSize.value ? 'noindex, follow' : undefined) });
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
    @page-size="fitPage"
  >
    <OnErrorState v-if="error" @retry="refresh()" />
    <template #card="{ pkg }">
      <AppCard
        :pkg="pkg"
        :stats="STATS"
        :href="withRecentReturn(localePath(packagePath(pkg.token)), `/recent?page=${page}&size=${pageSize}`)"
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
