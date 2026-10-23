<script setup lang="ts">
import { computed, ref } from 'vue';
import { useIntervalFn } from '@vueuse/core';
import dayjs from 'dayjs';
import { fetchDashboardOverview } from '@/service/api';
import type { DataOf } from '@/typings/api/opennavo';
import { $t } from '@/locales';
import CoverageCard from './modules/coverage-card.vue';
import HermesCard from './modules/hermes-card.vue';
import SeoCard from './modules/seo-card.vue';
import StatCard from './modules/stat-card.vue';
import SyncCard from './modules/sync-card.vue';
import TodoCard from './modules/todo-card.vue';
import UsageCard from './modules/usage-card.vue';

defineOptions({ name: 'Dashboard' });

// Dashboard (07 §7.1): scale, coverage, sync health, Hermes and open feedback; refresh every 60 seconds.
const overview = ref<DataOf<'getDashboardOverview'> | null>(null);
const loading = ref(false);
const failed = ref(false);
const refreshedAt = ref<Date | null>(null);

async function load() {
  if (loading.value) return;
  loading.value = true;
  failed.value = false;
  try {
    const summary = await fetchDashboardOverview();
    if (summary.error || !summary.data) {
      failed.value = true;
      return;
    }
    overview.value = summary.data;
    refreshedAt.value = new Date();
  } catch {
    failed.value = true;
  } finally {
    loading.value = false;
  }
}

void load();
useIntervalFn(load, 60_000);

const stats = computed(() => {
  const packages = overview.value?.packages;
  if (!packages) return [];
  return [
    // App and font counts are disjoint and exclude disabled or removed packages.
    { key: 'total', label: $t('page.dashboard.total'), value: packages.casks },
    { key: 'fonts', label: $t('page.dashboard.fonts'), value: packages.fonts },
    {
      key: 'disabled',
      label: $t('page.dashboard.disabled'),
      value: packages.disabled,
      hint: $t('page.dashboard.disabledHint', { deprecated: packages.deprecated, hidden: packages.hidden })
    }
  ];
});
</script>

<template>
  <NSpin :show="loading && !overview">
    <div class="flex-col gap-16px">
      <!-- Five headline cards: 3 + 2 rows on medium screens (Hermes spans two), one row on wide screens. -->
      <div v-if="overview" class="grid grid-cols-1 gap-16px sm:grid-cols-2 lg:grid-cols-3 2xl:grid-cols-5">
        <StatCard v-for="item in stats" :key="item.key" :label="item.label" :value="item.value" :hint="item.hint" />
        <TodoCard :feedback-open="overview.feedbackOpen" />
        <HermesCard v-if="overview.hermes" :hermes="overview.hermes" class="sm:col-span-2 2xl:col-span-1" />
      </div>
      <NAlert v-if="failed" type="error" role="alert" :title="$t('page.dashboard.seo.loadFailed')">
        <NButton :loading="loading" @click="load">{{ $t('page.dashboard.seo.retry') }}</NButton>
      </NAlert>
      <UsageCard v-if="overview?.usage" :usage="overview.usage" />
      <div v-if="overview" class="grid grid-cols-1 gap-16px lg:grid-cols-3">
        <CoverageCard :coverage="overview.coverage" class="lg:col-span-2" />
        <SyncCard :jobs="overview.jobs" :snapshot="overview.snapshot" />
      </div>
      <SeoCard />
      <NText v-if="overview && refreshedAt" depth="3" tag="p" class="m-0 text-right text-12px">
        {{ $t('page.dashboard.refreshedAt', { time: dayjs(refreshedAt).format('HH:mm:ss') }) }}
      </NText>
    </div>
  </NSpin>
</template>
