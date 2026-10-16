<script setup lang="ts">
import { unwrap } from '@opennavo/api';
import { formatCount } from '@opennavo/shared';
import { collectionIcons, OnCollectionsView, OnEmpty, OnErrorState, OnPagination } from '@opennavo/ui';
import type { OnCollectionsItem } from '@opennavo/ui';
import { pageCount as countPages } from '~/utils/catalogQuery';
import { brandlessTitle } from '~/utils/seoTitles';

const { locale: useI18nLocale } = useI18n();
const formattingLocale = computed(() => toAppLocale(useI18nLocale.value));

// Collections (05 §4): shared OnCollectionsView (ADR-017), with web pagination.
const PAGE_SIZE = 24;

const { t, locale } = useI18n();
const api = useApi();
const localePath = useLocalePath();
const NuxtLink = resolveComponent('NuxtLink');
const { hrefWith } = useCatalogQuery({ page: 1 });
const { page, assertExists } = useListPage();

const { data, error, refresh, status } = await useAsyncData(
  () => `collections:${page.value}:${locale.value}`,
  () => unwrap(api.GET('/collections', { params: { query: { current: page.value, size: PAGE_SIZE } } }))
);

assertExists(() => data.value?.total, PAGE_SIZE, status);
const pageCount = computed(() => countPages(data.value?.total ?? 0, PAGE_SIZE));
const items = computed<OnCollectionsItem[] | null>(() => {
  if (error.value) return [];
  return data.value
    ? data.value.records.map(collection => ({
        key: collection.slug,
        title: collection.title,
        subtitle: collection.subtitle,
        count: collection.itemCount,
        icons: collectionIcons(collection),
        href: localePath(`/collections/${collection.slug}`)
      }))
    : null;
});
const subtitle = computed(() =>
  data.value
    ? `${t('collections.description')} · ${t('catalog.total', { total: formatCount(data.value.total, { locale: formattingLocale.value }) }, { plural: data.value.total })}`
    : t('collections.description')
);

usePageSeo({ title: () => t('collections.metaTitle'), page, description: () => t('collections.description') });
// Share image (WP13): the brand icon stands beside the text; collections show their own apps.
defineOgImage('OgListSatori', {
  locale: formattingLocale.value,
  label: t('collections.title'),
  title: brandlessTitle(t('collections.metaTitle')),
  description: t('collections.description'),
  ...useOgFrame()
});
</script>

<template>
  <OnCollectionsView :title="t('collections.title')" :subtitle="subtitle" :items="items" :link-as="NuxtLink">
    <OnErrorState v-if="error" @retry="refresh()" />
    <template #empty>
      <OnEmpty v-if="!error" icon="lucide:boxes" :title="t('collections.emptyTitle')" />
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
  </OnCollectionsView>
</template>
