<script setup lang="ts">
import { unwrap } from '@opennavo/api';
import { formatCount } from '@opennavo/shared';
import { collectionIcons, OnCollectionsView, OnEmpty, OnErrorState, OnPagination, OnSegmented } from '@opennavo/ui';
import type { OnCollectionsItem } from '@opennavo/ui';
import BrewfilePage from '~/pages/brewfile.vue';
import { pageCount as countPages } from '~/utils/catalogQuery';
import { brandlessTitle, withVariantNames } from '~/utils/seoTitles';

const { locale: useI18nLocale } = useI18n();
const formattingLocale = computed(() => toAppLocale(useI18nLocale.value));

// Collections (05 §4): shared OnCollectionsView (ADR-017), with web pagination.
const PAGE_SIZE = 24;

const { t, te, locale } = useI18n();
const api = useApi();
const localePath = useLocalePath();
const NuxtLink = resolveComponent('NuxtLink');
const { hrefWith } = useCatalogQuery({ page: 1 });
const { page, assertExists } = useListPage();
const route = useRoute();
const router = useRouter();
const brewfile = useBrewfileList();
type Tab = 'collections' | 'list';
const tab = ref<Tab>(route.hash === '#list' ? 'list' : 'collections');
watch(
  () => route.hash,
  hash => {
    tab.value = hash === '#list' ? 'list' : 'collections';
  }
);
function setTab(value: Tab) {
  tab.value = value;
  void router.replace({ hash: value === 'list' ? '#list' : '' });
}

const { data, error, refresh, status } = await useAsyncData(
  () => `collections:${page.value}:${locale.value}`,
  () => unwrap(api.GET('/collections', { params: { query: { current: page.value, size: PAGE_SIZE } } })),
  { transform: withVariantNames }
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
const visibleItems = computed(() => (tab.value === 'list' ? [] : items.value));
const subtitle = computed(() =>
  tab.value === 'list'
    ? t('brewfile.description')
    : data.value
      ? `${t('collections.description')} · ${t('catalog.total', { total: formatCount(data.value.total, { locale: formattingLocale.value }) }, { plural: data.value.total })}`
      : t('collections.description')
);
const tabs = computed<{ value: Tab; label: string; count?: number }[]>(() => [
  { value: 'collections', label: t('collections.title'), count: data.value?.total },
  { value: 'list', label: t('nav.lists'), count: brewfile.count.value }
]);
// Search snippet only. The visible subtitle stays on the short collections.description line.
const metaDescription = computed(() =>
  te('collections.metaDescription') ? t('collections.metaDescription') : t('collections.description')
);

usePageSeo({ title: () => t('collections.metaTitle'), page, description: metaDescription });
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
  <OnCollectionsView :title="t('collections.title')" :subtitle="subtitle" :items="visibleItems" :link-as="NuxtLink">
    <OnSegmented
      class="mb-16px self-start"
      size="sm"
      :model-value="tab"
      :options="tabs"
      :aria-label="t('collections.title')"
      @update:model-value="setTab"
    />
    <OnErrorState v-if="tab === 'collections' && error" @retry="refresh()" />
    <template #empty>
      <BrewfilePage v-if="tab === 'list'" embedded />
      <OnEmpty v-else-if="!error" icon="lucide:boxes" :title="t('collections.emptyTitle')" />
    </template>
    <template #footer>
      <OnPagination
        v-if="tab === 'collections' && pageCount > 1"
        class="mt-24px self-center"
        :page="page"
        :page-count="pageCount"
        :href-for="target => hrefWith({ page: target })"
        :link-as="NuxtLink"
      />
    </template>
  </OnCollectionsView>
</template>
