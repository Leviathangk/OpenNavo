<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import { fetchIndexReport } from '@/service/api';
import type { Schemas } from '@/typings/api/opennavo';
import { $t } from '@/locales';
import { localeName } from '@/utils/content-locale';

defineOptions({ name: 'TranslationImpact' });
const props = withDefaults(defineProps<{ revision?: number }>(), { revision: 0 });
let request = 0;
onBeforeUnmount(() => {
  request += 1;
});

// Translation gate impact (H9): per language, overview pages indexable now and once the gate is
// enforced for every language, plus the apps that would leave the index. Pages on its own, so the
// index report above keeps its place.
const SIZE = 20;
const impact = ref<Schemas['TranslationImpact']>();
const current = ref(1);
const loading = ref(false);
const failed = ref(false);
const pageCount = computed(() => Math.max(1, Math.ceil((impact.value?.total ?? 0) / SIZE)));
const losing = computed(() => impact.value?.counts.filter(item => item.losing > 0) ?? []);

const text = (key: string, values?: Record<string, unknown>) =>
  values
    ? $t(`page.system.seo.impact.${key}` as App.I18n.I18nKey, values)
    : $t(`page.system.seo.impact.${key}` as App.I18n.I18nKey);

async function load() {
  const id = ++request;
  loading.value = true;
  failed.value = false;
  try {
    const { data, error } = await fetchIndexReport({ current: current.value, size: SIZE });
    if (id !== request) return;
    if (error) failed.value = true;
    else impact.value = data.translationImpact;
  } catch {
    if (id === request) failed.value = true;
  } finally {
    if (id === request) loading.value = false;
  }
}

function turn(page: number) {
  current.value = page;
  void load();
}

watch(
  () => props.revision,
  () => {
    current.value = 1;
    void load();
  },
  { immediate: true }
);
</script>

<template>
  <div class="w-full">
    <NText depth="3" class="mb-12px block">{{ text('hint') }}</NText>
    <NAlert v-if="failed" type="error" class="mb-12px">
      {{ text('loadFailed') }}
      <NButton size="small" :loading="loading" @click="load">{{ $t('common.refresh') }}</NButton>
    </NAlert>
    <NSpin :show="loading">
      <template v-if="impact">
        <NTable size="small" :single-line="false" class="mb-12px" data-testid="translation-impact-counts">
          <thead>
            <tr>
              <th>{{ text('locale') }}</th>
              <th>{{ text('indexableNow') }}</th>
              <th>{{ text('indexableEnforced') }}</th>
              <th>{{ text('losing') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="item in impact.counts" :key="item.locale">
              <td>{{ localeName(item.locale) }}</td>
              <td>{{ item.indexableNow }}</td>
              <td>{{ item.indexableEnforced }}</td>
              <td>
                <NText :type="item.losing ? 'warning' : undefined">{{ item.losing }}</NText>
              </td>
            </tr>
          </tbody>
        </NTable>
        <NTable v-if="impact.records.length" size="small" :single-line="false">
          <thead>
            <tr>
              <th>{{ text('app') }}</th>
              <th>{{ text('locale') }}</th>
              <th>{{ text('now') }}</th>
              <th>{{ text('enforced') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="row in impact.records" :key="`${row.id}-${row.locale}`">
              <td>
                <RouterLink
                  :to="{ name: 'catalog_package-detail', params: { id: String(row.id) }, query: { tab: 'index' } }"
                  class="text-primary"
                >
                  {{ row.token }}
                </RouterLink>
              </td>
              <td>{{ localeName(row.locale) }}</td>
              <td>
                {{ row.current.robots }}
                <NText depth="3" class="block text-12px font-mono">{{ row.current.reason }}</NText>
              </td>
              <td>
                {{ row.enforced.robots }}
                <NText depth="3" class="block text-12px font-mono">{{ row.enforced.reason }}</NText>
              </td>
            </tr>
          </tbody>
        </NTable>
        <NEmpty v-else-if="!losing.length" :description="text('empty')" class="py-24px" />
      </template>
    </NSpin>
    <div v-if="pageCount > 1" class="mt-12px flex justify-end">
      <NPagination :page="current" :page-count="pageCount" @update:page="turn" />
    </div>
  </div>
</template>
