<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { fetchSeoGeo } from '@/service/api';
import type { DataOf, Schemas } from '@/typings/api/opennavo';
import { formatDateTime } from '@/utils/opennavo';
import { $t } from '@/locales';
import GeoCounts from './geo-counts.vue';

const props = defineProps<{ geo: Schemas['SeoGeoSummary'] }>();
const show = ref(false);
const loading = ref(false);
const failed = ref(false);
const detail = ref<DataOf<'getSeoGeo'> | null>(null);
let generation = 0;
const statuses = ['answered', 'not_triggered', 'error', 'unsupported', 'missing'] as const;
const engines = computed(() =>
  (detail.value?.snapshot.engines ?? []).map(engine => {
    const cells = detail.value?.snapshot.cells.filter(cell => cell.engineKey === engine.key) ?? [];
    return {
      ...engine,
      states: statuses.map(status => ({ status, count: cells.filter(cell => cell.status === status).length })),
      hasCells: cells.length > 0
    };
  })
);

async function loadDetail() {
  if (!props.geo || !show.value) return;
  const current = ++generation;
  loading.value = true;
  failed.value = false;
  detail.value = null;
  try {
    const result = await fetchSeoGeo(props.geo.id);
    if (current !== generation) return;
    if (result.error || !result.data) failed.value = true;
    else detail.value = result.data;
  } catch {
    if (current === generation) failed.value = true;
  } finally {
    if (current === generation) loading.value = false;
  }
}
watch(show, value => {
  if (value) void loadDetail();
  else {
    generation += 1;
    detail.value = null;
    loading.value = false;
  }
});
watch(
  () => props.geo?.id,
  () => {
    show.value = false;
  }
);
</script>

<template>
  <section data-testid="geo-section">
    <div class="mb-8px flex flex-wrap items-center justify-between gap-8px">
      <h3 class="m-0 text-14px font-600">{{ $t('page.dashboard.seo.geo.title') }}</h3>
      <NButton v-if="geo" size="small" data-testid="geo-details" @click="show = true">
        {{ $t('page.dashboard.seo.geo.details') }}
      </NButton>
    </div>
    <NText v-if="geo === null" depth="3">{{ $t('page.dashboard.seo.geo.notImported') }}</NText>
    <template v-else>
      <GeoCounts :geo="geo" />
      <p class="mb-0 mt-8px">
        {{ $t('page.dashboard.seo.geo.dataTime') }}: {{ formatDateTime(geo.periodStart) }} –
        {{ formatDateTime(geo.periodEnd) }}
      </p>
    </template>
    <NDrawer v-model:show="show" width="min(640px, 100vw)">
      <NDrawerContent :title="$t('page.dashboard.seo.geo.details')" closable>
        <NSpin :show="loading">
          <NAlert v-if="failed" type="error" role="alert" :title="$t('page.dashboard.seo.geo.loadFailed')">
            <NButton size="small" :loading="loading" data-testid="geo-retry" @click="loadDetail">
              {{ $t('page.dashboard.seo.geo.retry') }}
            </NButton>
          </NAlert>
          <div v-else-if="detail" class="flex-col gap-16px" data-testid="geo-detail">
            <GeoCounts :geo="detail" />
            <section>
              <h3 class="mb-8px mt-0 text-14px font-600">{{ $t('page.dashboard.seo.geo.engines') }}</h3>
              <NText v-if="!engines.length">{{ $t('page.dashboard.seo.geo.unknown') }}</NText>
              <div v-for="engine in engines" :key="engine.key" class="mb-12px">
                <strong class="break-all">{{ engine.name }} ({{ engine.key }})</strong>
                <div v-if="engine.hasCells" class="mt-4px flex flex-wrap gap-8px">
                  <span v-for="state in engine.states" :key="state.status">
                    {{ $t(`page.dashboard.seo.geo.status.${state.status}`) }}: {{ state.count }}
                  </span>
                </div>
                <NText v-else class="block">{{ $t('page.dashboard.seo.geo.unknown') }}</NText>
              </div>
            </section>
            <section>
              <h3 class="mb-8px mt-0 text-14px font-600">{{ $t('page.dashboard.seo.geo.observations') }}</h3>
              <NText v-if="!detail.observationLinks.length">{{ $t('page.dashboard.seo.geo.noObservations') }}</NText>
              <ul v-else class="m-0 flex-col gap-8px pl-20px">
                <li v-for="link in detail.observationLinks" :key="link.observationId" class="break-all">
                  <a
                    v-if="link.url?.startsWith('https://')"
                    :href="link.url"
                    target="_blank"
                    rel="noopener noreferrer"
                    class="text-primary underline"
                  >
                    {{ link.observationId }}
                  </a>
                  <span v-else>{{ link.observationId }}</span>
                </li>
              </ul>
            </section>
          </div>
          <NSkeleton v-else :repeat="3" text />
        </NSpin>
      </NDrawerContent>
    </NDrawer>
  </section>
</template>
