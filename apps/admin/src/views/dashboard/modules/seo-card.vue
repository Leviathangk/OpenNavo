<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useAuth } from '@/hooks/business/auth';
import { fetchSeoAlerts, fetchSeoOverview } from '@/service/api';
import { useAppStore } from '@/store/modules/app';
import type { DataOf, Schemas } from '@/typings/api/opennavo';
import { formatDateTime } from '@/utils/opennavo';
import { $t } from '@/locales';
import GeoSection from './geo-section.vue';

defineOptions({ name: 'SeoCard' });

const { hasAuth } = useAuth();
const appStore = useAppStore();
const allowed = computed(() => hasAuth('seo:insight:view'));
const overview = ref<DataOf<'getSeoOverview'> | null>(null);
const alerts = ref<DataOf<'listSeoAlerts'>['records']>([]);
const loading = ref(false);
const failed = ref(false);
let generation = 0;

async function load() {
  if (!allowed.value) return;
  const current = ++generation;
  loading.value = true;
  failed.value = false;
  try {
    const [summary, issues] = await Promise.all([
      fetchSeoOverview(),
      fetchSeoAlerts({ current: 1, size: 5, status: 'open' })
    ]);
    if (current !== generation) return;
    if (summary.error || issues.error || !summary.data || !issues.data) {
      failed.value = true;
      return;
    }
    overview.value = summary.data;
    alerts.value = issues.data.records.filter(issue => issue.status === 'open').slice(0, 5);
  } catch {
    if (current === generation) failed.value = true;
  } finally {
    if (current === generation) loading.value = false;
  }
}

watch(
  allowed,
  value => {
    if (value) void load();
    else {
      generation += 1;
      overview.value = null;
      alerts.value = [];
      loading.value = false;
    }
  },
  { immediate: true }
);

const number = (value: number) => new Intl.NumberFormat(appStore.locale).format(value);
const budgets = computed(() =>
  (overview.value?.budgets ?? []).map((budget: Schemas['SeoOverviewBudget']) => {
    const paid = 'limitMicros' in budget;
    const used = paid ? budget.usedMicros : budget.used;
    const limit = paid ? budget.limitMicros : budget.limit;
    const money = (value: number) =>
      new Intl.NumberFormat(appStore.locale, {
        style: 'currency',
        currency: 'USD',
        maximumFractionDigits: 6
      }).format(value / 1_000_000);
    return {
      ...budget,
      usage: `${paid ? money(used) : number(used)} / ${paid ? money(limit) : number(limit)}`,
      percentage: limit > 0 ? Math.min(100, Math.max(0, (used / limit) * 100)) : used > 0 ? 100 : 0
    };
  })
);
// One row per action: its mode beside its budgets, so the two lists no longer sit in unequal columns.
const actionRows = computed(() => {
  const modes = overview.value?.actionModes ?? [];
  const actions = [...new Set([...modes.map(mode => mode.action), ...budgets.value.map(budget => budget.action)])];
  return actions.map(action => ({
    action,
    mode: modes.find(mode => mode.action === action),
    budgets: budgets.value.filter(budget => budget.action === action)
  }));
});
const yesterdayActions = computed(() =>
  [...(overview.value?.yesterday.actions ?? [])].sort(
    (a, b) => b.count - a.count || a.operation.localeCompare(b.operation)
  )
);
</script>

<template>
  <NCard v-if="allowed" :bordered="false" size="small" class="card-wrapper" :title="$t('page.dashboard.seo.title')">
    <NSpin :show="loading">
      <NAlert v-if="failed" type="error" role="alert" :title="$t('page.dashboard.seo.loadFailed')">
        <NButton size="small" :loading="loading" @click="load">{{ $t('page.dashboard.seo.retry') }}</NButton>
      </NAlert>
      <div v-else-if="overview" class="flex-col gap-16px text-13px">
        <div class="flex flex-wrap items-center gap-16px">
          <NTag :type="overview.automationReady ? 'success' : 'warning'" size="small">
            {{ $t(overview.automationReady ? 'page.dashboard.seo.ready' : 'page.dashboard.seo.notReady') }}
          </NTag>
          <span>{{ $t('page.dashboard.seo.keywords') }} · {{ number(overview.keywordCount) }}</span>
          <span>{{ $t('page.dashboard.seo.targets') }} · {{ number(overview.targetCount) }}</span>
        </div>
        <section>
          <h3 class="mb-8px mt-0 text-14px font-600">{{ $t('page.dashboard.seo.breakers') }}</h3>
          <NText v-if="!overview.breakers.length" type="success">{{ $t('page.dashboard.seo.healthy') }}</NText>
          <ul v-else class="m-0 flex-col gap-8px pl-20px">
            <li v-for="(breaker, index) in overview.breakers" :key="index" class="break-words">
              {{ $t(`page.dashboard.seo.${breaker.kind}`) }} · {{ breaker.scope }} · {{ breaker.reason }} ·
              {{ $t('page.dashboard.seo.openedAt') }} {{ formatDateTime(breaker.openedAt) }}
            </li>
          </ul>
        </section>
        <section>
          <h3 class="mb-8px mt-0 text-14px font-600">{{ $t('page.dashboard.seo.watermarks') }}</h3>
          <NText v-if="!overview.watermarks.length" depth="3">{{ $t('page.dashboard.seo.noData') }}</NText>
          <dl v-else class="m-0 grid grid-cols-2 gap-x-24px gap-y-8px sm:grid-cols-3 xl:grid-cols-6">
            <div v-for="watermark in overview.watermarks" :key="watermark.source" class="min-w-0">
              <dt class="break-all text-12px opacity-70">{{ watermark.source }}</dt>
              <dd class="m-0 tabular-nums">{{ watermark.completeThrough ?? $t('page.dashboard.seo.noData') }}</dd>
            </div>
          </dl>
        </section>
        <div class="grid grid-cols-1 items-start gap-x-24px gap-y-16px xl:grid-cols-2">
          <section class="min-w-0">
            <h3 class="mb-8px mt-0 text-14px font-600">
              {{ $t('page.dashboard.seo.modes') }} · {{ $t('page.dashboard.seo.budgets') }}
            </h3>
            <NText v-if="!actionRows.length" depth="3">{{ $t('page.dashboard.seo.noData') }}</NText>
            <NTable v-else size="small" :bordered="false" class="tabular-nums">
              <thead>
                <tr>
                  <th scope="col">{{ $t('page.dashboard.seo.action') }}</th>
                  <th scope="col">{{ $t('page.dashboard.seo.mode') }}</th>
                  <th scope="col" class="!text-right">{{ $t('page.dashboard.seo.usage') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="row in actionRows" :key="row.action">
                  <th scope="row" class="break-all !bg-transparent !font-normal">{{ row.action }}</th>
                  <td class="whitespace-nowrap">
                    <div class="flex items-center gap-8px">
                      <NTag v-if="row.mode" size="small" :type="row.mode.mode === 'live' ? 'success' : 'info'">
                        {{ row.mode.mode }}
                      </NTag>
                      <NText v-else depth="3">—</NText>
                      <NText v-if="row.mode && !row.mode.enabled" depth="3">{{ $t('page.dashboard.seo.disabled') }}</NText>
                    </div>
                  </td>
                  <td>
                    <NText v-if="!row.budgets.length" depth="3" class="block text-right">—</NText>
                    <div
                      v-for="budget in row.budgets"
                      :key="budget.period"
                      class="flex items-center justify-end gap-8px whitespace-nowrap"
                    >
                      <span class="opacity-70">{{ $t(`page.dashboard.seo.${budget.period}`) }}</span>
                      <span>{{ budget.usage }}</span>
                      <div class="w-64px shrink-0">
                        <NProgress
                          type="line"
                          :percentage="budget.percentage"
                          :show-indicator="false"
                          :height="4"
                          :aria-label="`${budget.action}: ${budget.usage}`"
                        />
                      </div>
                    </div>
                  </td>
                </tr>
              </tbody>
            </NTable>
          </section>
          <section class="min-w-0">
            <h3 class="mb-8px mt-0 text-14px font-600">{{ $t('page.dashboard.seo.yesterday') }}</h3>
            <NText v-if="!yesterdayActions.length" depth="3">{{ $t('page.dashboard.seo.noActivity') }}</NText>
            <NTable v-else size="small" :bordered="false" class="tabular-nums">
              <thead>
                <tr>
                  <th scope="col">{{ $t('page.dashboard.seo.operation') }}</th>
                  <th scope="col">{{ $t('page.dashboard.seo.mode') }}</th>
                  <th scope="col" class="!text-right">{{ $t('page.dashboard.seo.count') }}</th>
                  <th scope="col" class="!text-right">{{ $t('page.dashboard.seo.compensated') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="action in yesterdayActions" :key="`${action.operation}-${action.mode}`">
                  <th scope="row" class="break-all !bg-transparent !font-normal">{{ action.operation }}</th>
                  <td>
                    <NTag size="small" :type="action.mode === 'live' ? 'success' : 'info'">{{ action.mode }}</NTag>
                  </td>
                  <td class="text-right">{{ number(action.count) }}</td>
                  <td class="text-right">{{ number(action.compensated) }}</td>
                </tr>
              </tbody>
            </NTable>
          </section>
        </div>
        <div class="grid grid-cols-1 items-start gap-x-24px gap-y-16px xl:grid-cols-2">
          <GeoSection :geo="overview.geo" />
          <section>
            <h3 class="mb-8px mt-0 text-14px font-600">{{ $t('page.dashboard.seo.alerts') }}</h3>
            <NText v-if="!alerts.length" depth="3">{{ $t('page.dashboard.seo.noAlerts') }}</NText>
            <ul v-else class="m-0 flex-col gap-12px pl-20px">
              <li v-for="alert in alerts" :key="alert.id">
                <div class="flex flex-wrap items-center gap-8px">
                  <strong>{{ alert.rule }}</strong>
                  <NTag size="small">{{ $t(`page.dashboard.seo.${alert.layer}`) }}</NTag>
                  <span>{{ $t('page.dashboard.seo.lastSeen') }} {{ formatDateTime(alert.lastSeen) }}</span>
                </div>
                <div class="mt-4px break-all">{{ alert.url }}</div>
              </li>
            </ul>
          </section>
        </div>
      </div>
      <NSkeleton v-else :repeat="3" text />
    </NSpin>
  </NCard>
</template>
