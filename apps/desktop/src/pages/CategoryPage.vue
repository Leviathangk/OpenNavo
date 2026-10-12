<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { OnEmpty, OnPackageListView, OnPagination } from '@opennavo/ui';
import { RouterLink } from 'vue-router';
import PackageCard from '@/components/package/PackageCard.vue';
import type { ListSort } from '@/ipc/bindings';
import { commands, unwrap } from '@/ipc/client';
import { useAppLocale } from '@/composables/useAppLocale';
import { useCatalogLoader } from '@/composables/useCatalogLoader';
import { usePackageSummary } from '@/composables/usePackageSummary';
import { useCatalogStore } from '@/stores';

// Category details (local catalog): sorting and pagination, apps only (ADR-018); retain font packages in their own category. Shared web view (ADR-017).
const props = defineProps<{ slug: string }>();
const PAGE_SIZE = 24;

const { t } = useI18n();
const { appLocale } = useAppLocale();
const { toSummary } = usePackageSummary();
const catalog = useCatalogStore();

const sort = ref<Extract<ListSort, 'popular' | 'updated' | 'name'>>('popular');
const page = ref(1);

const category = computed(() => catalog.categories.find(item => item.slug === props.slug));
const parent = computed(() => catalog.categories.find(item => item.slug === category.value?.parent));

watch([() => props.slug, sort], () => {
  page.value = 1;
});

const { data, error } = useCatalogLoader(
  async () => {
    const result = await unwrap(
      commands.catalogList({
        kind: 'cask',
        category: props.slug,
        sort: sort.value,
        includeFonts: true,
        includeLibraries: false,
        includeDisabled: false,
        limit: PAGE_SIZE,
        offset: (page.value - 1) * PAGE_SIZE
      })
    );
    return result;
  },
  [() => props.slug, sort, page, appLocale],
  result => result.items
);

const pageCount = computed(() => Math.max(1, Math.ceil((data.value?.total ?? 0) / PAGE_SIZE)));
const sorts = computed(() =>
  (['popular', 'updated', 'name'] as const).map(value => ({
    value,
    label: t(`categories.sorts.${value}`)
  }))
);
const items = computed(() => (data.value ? data.value.items.map(toSummary) : error.value ? [] : null));
const breadcrumb = computed(() => [
  { label: t('categories.title'), href: '/categories' },
  ...(parent.value ? [{ label: parent.value.name, href: `/categories/${parent.value.slug}` }] : []),
  { label: category.value?.name ?? props.slug }
]);
</script>

<template>
  <OnPackageListView
    v-model:sort="sort"
    :breadcrumb="breadcrumb"
    :title="category?.name ?? slug"
    :subtitle="data ? t('categories.packages', { count: data.total }, { plural: data.total }) : undefined"
    :sorts="sorts"
    :sort-label="t('categories.sortLabel')"
    :items="items"
    :link-as="RouterLink"
  >
    <template #card="{ pkg }">
      <PackageCard :pkg="pkg" />
    </template>
    <template #empty>
      <OnEmpty v-if="!error" :title="t('categories.empty')" icon="lucide:layout-grid" />
    </template>
    <template #footer>
      <OnPagination v-if="pageCount > 1" v-model:page="page" class="mt-24px" :page-count="pageCount" />
    </template>
  </OnPackageListView>
</template>
