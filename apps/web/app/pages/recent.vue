<script setup lang="ts">
import { unwrap } from '@opennavo/api';
import { OnEmpty, OnErrorState, OnPackageListView, OnPagination } from '@opennavo/ui';
import type { OnAppCardStat } from '@opennavo/ui';
import { readPage } from '~/utils/catalogQuery';
import { packagePath } from '~/utils/packageView';

// Recently Updated: Discover's section in full, newest version first, with real page links. Shared
// OnPackageListView (ADR-017), Cask-only (ADR-018). Pages past the end answer 404.
const PAGE_SIZE = 24;
const STATS: readonly OnAppCardStat[] = ['version', 'installs30d'];

const { t, locale } = useI18n();
const api = useApi();
const localePath = useLocalePath();
const NuxtLink = resolveComponent('NuxtLink');
const { query, hrefWith } = useCatalogQuery({ page: 1 });
const page = computed(() => readPage(query.value.page));

const { data, error, refresh } = await useAsyncData(
  () => `recent:${page.value}:${locale.value}`,
  () =>
    unwrap(
      api.GET('/packages', {
        params: {
          query: { kind: 'cask', includeFonts: true, sort: 'updated', current: page.value, size: PAGE_SIZE }
        }
      })
    )
);

const pageCount = computed(() => Math.max(1, Math.ceil((data.value?.total ?? 0) / PAGE_SIZE)));
if (data.value && page.value > pageCount.value)
  throw createError({ statusCode: 404, statusMessage: 'Page not found', fatal: true });

useItemListSchema(
  () =>
    error.value
      ? null
      : data.value?.records.map(pkg => ({ name: pkg.displayName, path: localePath(packagePath(pkg.token)) })),
  () => (page.value - 1) * PAGE_SIZE + 1
);

usePageSeo({ title: () => t('recent.metaTitle'), page, description: () => t('recent.description') });
</script>

<template>
  <OnPackageListView
    :title="t('recent.title')"
    :subtitle="t('recent.description')"
    :items="error ? [] : (data?.records ?? null)"
    :link-as="NuxtLink"
  >
    <OnErrorState v-if="error" @retry="refresh()" />
    <template #card="{ pkg }">
      <AppCard :pkg="pkg" :stats="STATS" />
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
