<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { RouterLink, useRoute, useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { unwrap } from '@opennavo/api';
import { collectionIcons, OnCollectionsView, OnEmpty, OnSegmented } from '@opennavo/ui';
import type { OnCollectionsItem } from '@opennavo/ui';
import RequestError from '@/components/common/RequestError.vue';
import BrewfilePage from '@/pages/BrewfilePage.vue';
import { api } from '@/api';
import { useAppLocale } from '@/composables/useAppLocale';
import { useRemoteLoader } from '@/composables/useRemoteLoader';
import { useBrewfileStore } from '@/stores/brewfile';

// Collections and My List share one destination. The hash keeps the list view linkable without adding
// a second sidebar entry; both views keep their own data source.
type Tab = 'collections' | 'list';

const { t } = useI18n();
const { appLocale } = useAppLocale();
const route = useRoute();
const router = useRouter();
const brewfile = useBrewfileStore();

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

const { data, error } = useRemoteLoader(
  'collections',
  () => unwrap(api.GET('/collections', { params: { query: { current: 1, size: 48 } } })),
  [appLocale]
);

const offline = computed(() => Boolean(error.value && !data.value));
const items = computed<OnCollectionsItem[] | null>(() => {
  if (offline.value) return [];
  return data.value
    ? data.value.records.map(collection => ({
        key: collection.slug,
        title: collection.title,
        subtitle: collection.subtitle,
        count: collection.itemCount,
        icons: collectionIcons(collection),
        href: `/collections/${collection.slug}`
      }))
    : null;
});
const visibleItems = computed(() => (tab.value === 'list' ? [] : items.value));
const subtitle = computed(() => (tab.value === 'list' ? t('brewfile.description') : t('collections.subtitle')));
const tabs = computed<{ value: Tab; label: string; count?: number }[]>(() => [
  { value: 'collections', label: t('collections.title'), count: data.value?.total },
  { value: 'list', label: t('nav.lists'), count: brewfile.items.length }
]);
</script>

<template>
  <OnCollectionsView :title="t('collections.title')" :subtitle="subtitle" :items="visibleItems" :link-as="RouterLink">
    <OnSegmented
      class="mb-16px self-start"
      size="sm"
      :model-value="tab"
      :options="tabs"
      :aria-label="t('collections.title')"
      @update:model-value="setTab"
    />
    <RequestError v-if="tab === 'collections' && offline" :error="error" />
    <template #empty>
      <BrewfilePage v-if="tab === 'list'" embedded />
      <OnEmpty v-else-if="!offline" :title="t('collections.empty')" icon="lucide:library" />
    </template>
  </OnCollectionsView>
</template>
