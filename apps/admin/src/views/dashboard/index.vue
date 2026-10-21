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
      <NGrid :x-gap="16" :y-gap="16" responsive="screen" item-responsive>
        <NGi v-for="item in stats" :key="item.key" span="24 s:12 m:6">
          <StatCard :label="item.label" :value="item.value" :hint="item.hint" />
        </NGi>
        <NGi v-if="overview?.hermes" span="24 s:12 m:6">
          <HermesCard :hermes="overview.hermes" />
        </NGi>
      </NGrid>
      <NAlert v-if="failed" type="error" role="alert" :title="$t('page.dashboard.seo.loadFailed')">
        <NButton :loading="loading" @click="load">{{ $t('page.dashboard.seo.retry') }}</NButton>
      </NAlert>
      <UsageCard v-if="overview?.usage" :usage="overview.usage" />
      <SeoCard />
      <template v-if="overview">
        <NGrid :x-gap="16" :y-gap="16" responsive="screen" item-responsive>
          <NGi span="24 m:16">
            <CoverageCard :coverage="overview.coverage" />
          </NGi>
          <NGi span="24 m:8">
            <SyncCard :jobs="overview.jobs" :snapshot="overview.snapshot" />
          </NGi>
          <NGi span="24 m:8">
            <TodoCard :feedback-open="overview.feedbackOpen" />
          </NGi>
        </NGrid>
        <NText v-if="refreshedAt" depth="3" tag="p" class="m-0 text-right text-12px">
          {{ $t('page.dashboard.refreshedAt', { time: dayjs(refreshedAt).format('HH:mm:ss') }) }}
        </NText>
      </template>
    </div>
  </NSpin>
</template>
