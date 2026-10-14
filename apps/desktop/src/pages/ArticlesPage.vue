<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { RouterLink, useRoute, useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { unwrap } from '@opennavo/api';
import { OnEmpty, OnPageHeader, OnPagination, OnSkeleton, OnTabs } from '@opennavo/ui';
import ArticleCard from '@/components/article/ArticleCard.vue';
import RequestError from '@/components/common/RequestError.vue';
import { api } from '@/api';
import { useAppLocale } from '@/composables/useAppLocale';
import { useLoader } from '@/composables/useLoader';
import { ARTICLE_PAGE_SIZE, ARTICLE_TYPES, articlePage, articleType } from '@/utils/article';

const { t } = useI18n();
const { appLocale } = useAppLocale();
const route = useRoute();
const root = ref<HTMLElement>();
const router = useRouter();
const type = computed(() => articleType(route.query.type));
const page = computed(() => articlePage(route.query.page));
watch([type, page], () => root.value?.scrollIntoView?.({ block: 'start' }), { flush: 'post' });
const tabs = computed(() => [
  { value: 'all', label: t('articles.all'), href: '/articles' },
  ...ARTICLE_TYPES.map(value => ({
    value,
    label: t(`articles.types.${value}`),
    href: `/articles?type=${value}`
  }))
]);
const { data, error, loading } = useLoader(
  () =>
    unwrap(
      api.GET('/articles', {
        params: { query: { current: page.value, size: ARTICLE_PAGE_SIZE, type: type.value } }
      })
    ),
  [type, page, appLocale]
);
const pageCount = computed(() => Math.max(1, Math.ceil((data.value?.total ?? 0) / ARTICLE_PAGE_SIZE)));
function goPage(value: number) {
  void router.push({
    path: '/articles',
    query: { ...(type.value ? { type: type.value } : {}), ...(value > 1 ? { page: value } : {}) }
  });
}
</script>

<template>
  <div ref="root" class="mx-auto min-w-0 max-w-960px">
    <OnPageHeader :title="t('articles.title')" :subtitle="t('articles.subtitle')" />
    <OnTabs :model-value="type ?? 'all'" :items="tabs" :aria-label="t('articles.typesLabel')" :link-as="RouterLink" />
    <div :aria-busy="loading" class="min-w-0">
      <RequestError v-if="error && !data" :error="error" class="mt-24px" />
      <div v-else-if="!data" class="flex flex-col gap-20px py-24px" role="status" :aria-label="t('common.loading')">
        <OnSkeleton v-for="index in 4" :key="index" class="h-100px w-full rounded-default" />
      </div>
      <OnEmpty
        v-else-if="!data.records.length"
        :title="t('articles.emptyTitle')"
        :description="t('articles.emptyDescription')"
        icon="lucide:newspaper"
      />
      <div v-else data-testid="article-list">
        <ArticleCard v-for="card in data.records" :key="card.slug" :card="card" />
      </div>
    </div>
    <OnPagination
      v-if="data && pageCount > 1"
      :page="page"
      :page-count="pageCount"
      class="mt-24px"
      @update:page="goPage"
    />
  </div>
</template>
