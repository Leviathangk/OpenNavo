<script setup lang="ts">
import { computed } from 'vue';
import { useThemeVars } from 'naive-ui';
import { formatCount } from '@opennavo/shared';
import type { Schemas } from '@/typings/api/opennavo';
import { useAppStore } from '@/store/modules/app';
import { useThemeStore } from '@/store/modules/theme';
import { $t } from '@/locales';
import { USAGE_SERIES_COLORS } from './usage-series';
import GitHubReleaseTrend from './github-release-trend.vue';

defineOptions({ name: 'GitHubReleaseCard' });

const props = defineProps<{ stats: Schemas['GitHubReleaseOverview'] }>();
const theme = useThemeVars();
const appStore = useAppStore();
const themeStore = useThemeStore();
const locale = computed(() => appStore.locale);
const text = (key: keyof typeof import('@/locales/langs/pages/zh-cn').default.dashboard.githubReleases, values = {}) =>
  $t(`page.dashboard.githubReleases.${key}`, values);
const count = (value: number | null) => (value === null ? '—' : formatCount(value, { locale: locale.value }));
const colors = computed(() => {
  const palette = USAGE_SERIES_COLORS[themeStore.darkMode ? 'dark' : 'light'];
  return { downloads: palette.desktop, updates: palette.web };
});
const categories = ['downloads', 'updates'] as const;
const snapshot = computed(() => {
  if (!props.stats.lastSnapshotAt) return text('never');
  const time = new Intl.DateTimeFormat(locale.value, {
    timeZone: props.stats.timeZone,
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23'
  }).format(new Date(props.stats.lastSnapshotAt));
  return text('snapshot', { time });
});
const days = computed(() =>
  props.stats.days.filter(day => props.stats.dailyStartDate && day.date >= props.stats.dailyStartDate)
);
const hasDaily = computed(() => days.value.some(day => day.downloads !== null || day.updates !== null));
</script>

<template>
  <NCard :bordered="false" size="small" class="card-wrapper" :title="text('title')" data-testid="github-release-card">
    <div class="flex-col gap-16px">
      <NText depth="3" class="text-12px" data-testid="github-release-snapshot">{{ snapshot }}</NText>
      <dl class="m-0 grid grid-cols-1 gap-12px sm:grid-cols-2">
        <div
          v-for="category in categories"
          :key="category"
          :data-metric="category"
          class="rounded-8px px-14px py-12px"
          :style="{ background: theme.actionColor }"
        >
          <dt class="flex items-center gap-6px text-13px" :style="{ color: theme.textColor2 }">
            <span class="h-2px w-12px rounded-full" :style="{ background: colors[category] }" />
            {{ text(category) }}
          </dt>
          <dd class="m-0 mt-4px flex flex-wrap items-baseline gap-8px tabular-nums">
            <strong class="text-26px font-600 leading-32px" :style="{ color: theme.textColor1 }">
              {{ count(stats[category].total) }}
            </strong>
            <span class="text-12px" :style="{ color: theme.textColor3 }">{{ text('total') }}</span>
          </dd>
          <dd
            class="m-0 mt-4px flex flex-wrap gap-x-16px gap-y-4px text-12px tabular-nums"
            :style="{ color: theme.textColor2 }"
          >
            <span>{{ text('today', { value: count(stats[category].today) }) }}</span>
            <span>{{ text('yesterday', { value: count(stats[category].yesterday) }) }}</span>
          </dd>
        </div>
      </dl>
      <GitHubReleaseTrend v-if="hasDaily" :days="days" :colors="colors" :locale="locale" :title="text('trend')" />
      <NEmpty v-else :description="text(stats.lastSnapshotAt ? 'pending' : 'empty')" />
      <details v-if="hasDaily">
        <summary class="cursor-pointer rounded py-4px text-13px focus-visible:outline-solid">
          {{ text('details') }}
        </summary>
        <div class="mt-8px overflow-x-auto">
          <NTable :single-line="false" size="small" class="tabular-nums">
            <thead>
              <tr>
                <th scope="col">{{ text('date') }}</th>
                <th scope="col" class="!text-right">{{ text('downloads') }}</th>
                <th scope="col" class="!text-right">{{ text('updates') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="entry in [...days].reverse()" :key="entry.date">
                <th scope="row" class="whitespace-nowrap !bg-transparent !font-normal">{{ entry.date }}</th>
                <td class="text-right">{{ count(entry.downloads) }}</td>
                <td class="text-right">{{ count(entry.updates) }}</td>
              </tr>
            </tbody>
          </NTable>
        </div>
      </details>
      <div class="flex-col gap-4px text-12px" :style="{ color: theme.textColor3 }">
        <p class="m-0">{{ text('downloadsHint') }}</p>
        <p class="m-0">{{ text('updatesHint') }}</p>
        <p v-if="stats.dailyStartDate" class="m-0">{{ text('dailyStart', { date: stats.dailyStartDate }) }}</p>
        <p class="m-0">{{ text('dailyHint') }}</p>
      </div>
    </div>
  </NCard>
</template>
