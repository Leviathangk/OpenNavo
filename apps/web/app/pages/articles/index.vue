<script setup lang="ts">
import { unwrap } from '@opennavo/api';
import { OnEmpty, OnErrorState, OnIcon, OnPageHeader, OnPagination, OnTabs } from '@opennavo/ui';
import { pageCount as countPages } from '~/utils/catalogQuery';
import { ARTICLE_PAGE_SIZE, ARTICLE_TYPES, isArticleType } from '~/utils/article';

// Articles (WP17): newest first with real page links. Page 2 and later are their own canonical pages;
// type filters are browsing aids, so they keep their own canonical but stay out of the index. Pages
// past the end answer 404.
const { t, locale } = useI18n();
const api = useApi();
const route = useRoute();
const localePath = useLocalePath();
const NuxtLink = resolveComponent('NuxtLink');
const siteUrl = useRuntimeConfig().public.siteUrl.replace(/\/$/, '');
const { hrefWith } = useCatalogQuery({ page: 1 });
const { page, assertExists } = useListPage();
useKnownQueryValue('type', ARTICLE_TYPES);
const type = computed(() => (isArticleType(route.query.type) ? route.query.type : undefined));

const { data, error, refresh, status } = await useAsyncData(
  () => `articles:${type.value ?? 'all'}:${page.value}:${locale.value}`,
  () =>
    unwrap(
      api.GET('/articles', { params: { query: { current: page.value, size: ARTICLE_PAGE_SIZE, type: type.value } } })
    )
);

assertExists(() => data.value?.total, ARTICLE_PAGE_SIZE, status);
const pageCount = computed(() => countPages(data.value?.total ?? 0, ARTICLE_PAGE_SIZE));

const records = computed(() => (error.value ? [] : (data.value?.records ?? [])));
const featured = computed(() => (page.value === 1 && !type.value ? records.value[0] : undefined));
const rest = computed(() => (featured.value ? records.value.slice(1) : records.value));
const tabs = computed(() => [
  { value: 'all', label: t('articles.all'), href: hrefWith({ type: undefined, page: undefined }) },
  ...ARTICLE_TYPES.map(value => ({
    value,
    label: t(`articles.types.${value}`),
    href: hrefWith({ type: value, page: undefined })
  }))
]);
const feedPath = useArticleFeed();

useItemListSchema(
  () => records.value.map(card => ({ name: card.title, path: localePath(`/articles/${card.slug}`) })),
  () => (page.value - 1) * ARTICLE_PAGE_SIZE + 1,
  () => data.value?.total
);
usePageSeo({
  title: () =>
    type.value ? t('articles.typeMetaTitle', { type: t(`articles.types.${type.value}`) }) : t('articles.metaTitle'),
  page,
  description: () => t('articles.description')
});
// Filters and empty lists have nothing of their own to index.
useSeoMeta({ robots: () => (type.value || (data.value && !data.value.total) ? 'noindex, follow' : undefined) });
useHead(() =>
  type.value
    ? {
        link: [
          {
            rel: 'canonical',
            href: `${siteUrl}${localePath('/articles')}?type=${type.value}${page.value > 1 ? `&page=${page.value}` : ''}`
          }
        ]
      }
    : {}
);
defineOgImage('OgListSatori', {
  locale: toAppLocale(locale.value),
  label: t('nav.articles'),
  title: t('articles.ogTitle'),
  description: t('articles.subtitle'),
  ...useOgFrame()
});
</script>

<template>
  <div class="flex flex-col gap-16px">
    <OnPageHeader :title="t('articles.title')" :subtitle="t('articles.subtitle')">
      <template #actions>
        <a
          :href="feedPath"
          class="inline-flex items-center gap-6px rounded-default px-8px py-6px text-13px text-ink-secondary no-underline hover:text-ink-primary focus-visible:shadow-focus-ring"
        >
          <OnIcon name="rss" :size="15" />
          {{ t('articles.rss') }}
        </a>
      </template>
    </OnPageHeader>
    <OnTabs :model-value="type ?? 'all'" :items="tabs" :aria-label="t('articles.typesLabel')" :link-as="NuxtLink" />
    <OnErrorState v-if="error" @retry="refresh()" />
    <OnEmpty
      v-else-if="data && !records.length"
      icon="lucide:newspaper"
      :title="t('articles.emptyTitle')"
      :description="t('articles.emptyDescription')"
    />
    <div v-else class="flex flex-col gap-12px" data-testid="article-list">
      <ArticleCard v-if="featured" :card="featured" featured />
      <ArticleCard v-for="card in rest" :key="card.slug" :card="card" />
    </div>
    <OnPagination
      v-if="pageCount > 1"
      class="mt-12px self-center"
      :page="page"
      :page-count="pageCount"
      :href-for="target => hrefWith({ page: target })"
      :link-as="NuxtLink"
    />
  </div>
</template>
