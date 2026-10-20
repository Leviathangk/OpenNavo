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
        <div class="grid grid-cols-1 gap-16px md:grid-cols-2">
          <section>
            <h3 class="mb-8px mt-0 text-14px font-600">{{ $t('page.dashboard.seo.budgets') }}</h3>
            <NText v-if="!budgets.length" depth="3">{{ $t('page.dashboard.seo.noData') }}</NText>
            <div v-for="budget in budgets" :key="`${budget.action}-${budget.period}`" class="mb-8px">
              <div class="flex flex-wrap justify-between gap-8px">
                <span class="break-all">{{ budget.action }} · {{ $t(`page.dashboard.seo.${budget.period}`) }}</span>
                <span class="tabular-nums">{{ budget.usage }}</span>
              </div>
              <NProgress
                type="line"
                :percentage="budget.percentage"
                :show-indicator="false"
                :aria-label="`${budget.action}: ${budget.usage}`"
              />
            </div>
          </section>
          <section>
            <h3 class="mb-8px mt-0 text-14px font-600">{{ $t('page.dashboard.seo.modes') }}</h3>
            <NText v-if="!overview.actionModes.length" depth="3">{{ $t('page.dashboard.seo.noData') }}</NText>
            <div
              v-for="action in overview.actionModes"
              :key="action.action"
              class="mb-8px flex flex-wrap items-center gap-8px"
            >
              <span class="break-all">{{ action.action }}</span>
              <NTag size="small" :type="action.mode === 'live' ? 'success' : 'info'">{{ action.mode }}</NTag>
              <NText v-if="!action.enabled" depth="3">{{ $t('page.dashboard.seo.disabled') }}</NText>
            </div>
          </section>
          <section>
            <h3 class="mb-8px mt-0 text-14px font-600">{{ $t('page.dashboard.seo.watermarks') }}</h3>
            <NText v-if="!overview.watermarks.length" depth="3">{{ $t('page.dashboard.seo.noData') }}</NText>
            <div
              v-for="watermark in overview.watermarks"
              :key="watermark.source"
              class="mb-8px flex flex-wrap justify-between gap-8px"
            >
              <span>{{ watermark.source }}</span>
              <span>{{ watermark.completeThrough ?? $t('page.dashboard.seo.noData') }}</span>
            </div>
          </section>
          <section>
            <h3 class="mb-8px mt-0 text-14px font-600">{{ $t('page.dashboard.seo.yesterday') }}</h3>
            <NText v-if="!overview.yesterday.actions.length" depth="3">{{ $t('page.dashboard.seo.noActivity') }}</NText>
            <div
              v-for="action in overview.yesterday.actions"
              :key="`${action.operation}-${action.mode}`"
              class="mb-8px flex flex-wrap items-center gap-8px"
            >
              <span class="break-all">{{ action.operation }}</span>
              <NTag size="small" :type="action.mode === 'live' ? 'success' : 'info'">{{ action.mode }}</NTag>
              <span>{{ $t('page.dashboard.seo.count') }} {{ number(action.count) }}</span>
              <span>{{ $t('page.dashboard.seo.compensated') }} {{ number(action.compensated) }}</span>
            </div>
          </section>
        </div>
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
      <NSkeleton v-else :repeat="3" text />
    </NSpin>
  </NCard>
</template>
