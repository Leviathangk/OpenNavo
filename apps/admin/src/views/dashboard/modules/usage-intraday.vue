<script setup lang="ts">
import { computed, ref, useTemplateRef, watch } from 'vue';
import { useElementSize } from '@vueuse/core';
import { useThemeVars } from 'naive-ui';
import { formatCount, formatTime } from '@opennavo/shared';
import { fetchDashboardUsageIntraday } from '@/service/api/dashboard';
import type { DataOf } from '@/typings/api/opennavo';
import { useAppStore } from '@/store/modules/app';
import { useThemeStore } from '@/store/modules/theme';
import { $t } from '@/locales';
import { USAGE_SERIES_COLORS } from './usage-series';
import type { UsagePlatform } from './usage-series';

defineOptions({ name: 'UsageIntraday' });

const props = defineProps<{ refreshAt?: Date | null }>();
const theme = useThemeVars();
const app = useAppStore();
const themeStore = useThemeStore();
const text = (key: keyof typeof import('@/locales/langs/pages/zh-cn').default.dashboard.intraday, values = {}) =>
  $t(`page.dashboard.intraday.${key}`, values);
const count = (value: number) => formatCount(value, { locale: app.locale });
const hourLabel = (hour: number) => `${String(hour).padStart(2, '0')}:00`;
const platformLabel = (platform: UsagePlatform) => text(platform);
const colors = computed(() => USAGE_SERIES_COLORS[themeStore.darkMode ? 'dark' : 'light']);
const platforms: UsagePlatform[] = ['desktop', 'web'];
const data = ref<DataOf<'getDashboardUsageIntraday'> | null>(null);
const loading = ref(false);
const failed = ref(false);

async function load() {
  if (loading.value) return;
  loading.value = true;
  failed.value = false;
  try {
    const result = await fetchDashboardUsageIntraday();
    if (result.error || !result.data) {
      failed.value = true;
      return;
    }
    data.value = result.data;
  } catch {
    failed.value = true;
  } finally {
    loading.value = false;
  }
}

// Use the Dashboard's successful 60-second refresh rather than another timer.
watch(() => props.refreshAt, load, { immediate: true });

const mode = ref<'hourly' | 'cumulative'>('hourly');
const elapsed = computed(() => data.value?.hours.filter(entry => entry.web !== null) ?? []);
const latest = computed(() => elapsed.value.at(-1));
const hasActivity = computed(() => Boolean(latest.value?.cumulativeDesktop || latest.value?.cumulativeWeb));
const asOf = computed(() =>
  data.value
    ? text('asOf', {
        date: data.value.date,
        time: formatTime(data.value.asOf, { timeZone: data.value.timeZone })
      })
    : ''
);

const HEIGHT = 240;
const TOP = 12;
const BOTTOM = 204;
const LEFT = 44;
const RIGHT = 20;
const frame = useTemplateRef<HTMLElement>('frame');
const { width } = useElementSize(frame);
const chartWidth = computed(() => Math.max(240, Math.round(width.value)));
const step = computed(() => (chartWidth.value - LEFT - RIGHT) / 24);
const x = (hour: number) => LEFT + step.value * (hour + 0.5);
const value = (
  entry: (typeof elapsed.value)[number],
  platform: UsagePlatform,
  cumulative = mode.value === 'cumulative'
) => (cumulative ? entry[platform === 'web' ? 'cumulativeWeb' : 'cumulativeDesktop'] : entry[platform]) ?? 0;
const ticks = computed(() => {
  const max = Math.max(1, ...elapsed.value.flatMap(entry => platforms.map(platform => value(entry, platform))));
  const power = 10 ** Math.floor(Math.log10(max / 4));
  const unit = max / 4 / power;
  const size = Math.max(1, (unit <= 1 ? 1 : unit <= 2 ? 2 : unit <= 5 ? 5 : 10) * power);
  return Array.from({ length: Math.ceil(max / size) + 1 }, (_, index) => index * size);
});
const y = (n: number) => BOTTOM - (n / (ticks.value.at(-1) || 1)) * (BOTTOM - TOP);
const hourTicks = computed(() => {
  const stride = chartWidth.value < 500 ? 6 : 3;
  return Array.from({ length: 24 }, (_, hour) => hour).filter(hour => hour % stride === 0 || hour === 23);
});
const lines = computed(() =>
  platforms.map(platform => ({
    platform,
    points: elapsed.value.map(entry => `${x(entry.hour).toFixed(1)},${y(value(entry, platform)).toFixed(1)}`).join(' ')
  }))
);

// Retain the selected hour on refresh, but never carry a selection across midnight.
const selected = ref<{ date: string; hour: number } | null>(null);
const current = computed(() =>
  selected.value?.date === data.value?.date
    ? elapsed.value.find(entry => entry.hour === selected.value?.hour)
    : undefined
);
function select(hour: number | null) {
  selected.value = hour === null || !data.value ? null : { date: data.value.date, hour };
}
function move(delta: number) {
  const last = latest.value?.hour ?? 0;
  select(Math.min(last, Math.max(0, (current.value?.hour ?? last) + delta)));
}
const tooltipStyle = computed(() => {
  const left = x(current.value?.hour ?? 0);
  return {
    left: `${left}px`,
    transform: left > chartWidth.value * 0.5 ? 'translateX(calc(-100% - 8px))' : 'translateX(8px)',
    background: theme.value.popoverColor,
    boxShadow: theme.value.boxShadow2,
    color: theme.value.textColor2
  };
});
</script>

<template>
  <section aria-labelledby="usage-intraday-title" data-testid="usage-intraday" class="min-w-0">
    <div class="mb-8px flex flex-wrap items-center justify-between gap-8px">
      <h3 id="usage-intraday-title" class="m-0 text-14px font-600">{{ text('title') }}</h3>
      <NText v-if="data" depth="3" class="text-12px">{{ asOf }}</NText>
    </div>
    <p class="mb-12px mt-0 text-12px" :style="{ color: theme.textColor3 }">{{ text('description') }}</p>
    <NAlert v-if="failed" type="error" role="alert" :title="text('loadFailed')" class="mb-12px">
      <NButton :loading="loading" @click="load">{{ $t('page.dashboard.retry') }}</NButton>
    </NAlert>
    <NSkeleton v-if="loading && !data" height="240px" />
    <template v-if="data">
      <div class="mb-8px flex flex-wrap items-center justify-between gap-12px">
        <NRadioGroup
          v-model:value="mode"
          size="small"
          name="intraday-mode"
          :aria-label="text('measure')"
          class="max-w-full !flex flex-wrap"
        >
          <NRadioButton value="hourly">{{ text('hourly') }}</NRadioButton>
          <NRadioButton value="cumulative">{{ text('cumulative') }}</NRadioButton>
        </NRadioGroup>
        <span class="flex flex-wrap gap-16px text-12px" :style="{ color: theme.textColor2 }">
          <span v-for="platform in platforms" :key="platform" class="flex items-center gap-6px">
            <span class="h-2px w-16px rounded-full" :style="{ background: colors[platform] }" />
            {{ platformLabel(platform) }}
          </span>
        </span>
      </div>
      <NEmpty v-if="!hasActivity" :description="$t('page.dashboard.usage.empty')" class="py-16px" />
      <figure v-else class="m-0">
        <figcaption class="sr-only">{{ text(mode) }}</figcaption>
        <div
          ref="frame"
          class="relative rounded-4px outline-none focus-visible:outline-solid"
          tabindex="0"
          :aria-label="text('keyboardHint')"
          @focus="move(0)"
          @blur="select(null)"
          @keydown.left.prevent="move(-1)"
          @keydown.right.prevent="move(1)"
          @keydown.home.prevent="select(0)"
          @keydown.end.prevent="select(latest?.hour ?? 0)"
        >
          <svg
            :viewBox="`0 0 ${chartWidth} ${HEIGHT}`"
            :height="HEIGHT"
            role="img"
            :aria-label="`${text('title')} · ${text(mode)}`"
            class="block w-full"
            @pointerleave="select(null)"
          >
            <title>{{ text('title') }} · {{ text(mode) }}</title>
            <g font-size="12" :fill="theme.textColor3" class="tabular-nums">
              <template v-for="tick in ticks" :key="tick">
                <line
                  :x1="LEFT"
                  :x2="chartWidth - RIGHT"
                  :y1="y(tick)"
                  :y2="y(tick)"
                  :stroke="tick ? theme.dividerColor : theme.borderColor"
                  shape-rendering="crispEdges"
                />
                <text :x="LEFT - 8" :y="y(tick) + 4" text-anchor="end">{{ count(tick) }}</text>
              </template>
              <text v-for="hour in hourTicks" :key="hour" :x="x(hour)" :y="HEIGHT - 12" text-anchor="middle">
                {{ hourLabel(hour) }}
              </text>
            </g>
            <line
              v-if="current"
              :x1="x(current.hour)"
              :x2="x(current.hour)"
              :y1="TOP"
              :y2="BOTTOM"
              :stroke="theme.borderColor"
            />
            <template v-if="mode === 'hourly'">
              <g v-for="entry in elapsed" :key="entry.hour">
                <rect
                  v-for="(platform, index) in platforms"
                  :key="platform"
                  :data-series="platform"
                  :data-hour="entry.hour"
                  :x="x(entry.hour) + (index - 1) * step * 0.34"
                  :y="y(value(entry, platform))"
                  :width="step * 0.3"
                  :height="BOTTOM - y(value(entry, platform))"
                  :fill="colors[platform]"
                />
              </g>
            </template>
            <template v-else>
              <polyline
                v-for="line in lines"
                :key="line.platform"
                :data-series="line.platform"
                :points="line.points"
                fill="none"
                :stroke="colors[line.platform]"
                stroke-width="2"
                stroke-linejoin="round"
              />
              <template v-if="latest">
                <circle
                  v-for="platform in platforms"
                  :key="platform"
                  :cx="x(latest.hour)"
                  :cy="y(value(latest, platform))"
                  r="4"
                  :fill="colors[platform]"
                  :stroke="theme.cardColor"
                  stroke-width="2"
                />
              </template>
            </template>
            <rect
              v-for="entry in elapsed"
              :key="entry.hour"
              :x="LEFT + step * entry.hour"
              y="0"
              :width="step"
              :height="HEIGHT"
              fill="transparent"
              :data-hover-hour="entry.hour"
              @pointerenter="select(entry.hour)"
            />
          </svg>
          <div
            v-if="current"
            role="status"
            aria-live="polite"
            class="pointer-events-none absolute top-4px z-1 rounded-6px px-12px py-8px text-12px"
            :style="tooltipStyle"
          >
            <div class="mb-4px font-600">{{ hourLabel(current.hour) }} · {{ text(mode) }}</div>
            <div v-for="platform in platforms" :key="platform" class="flex items-center gap-8px leading-20px">
              <strong class="tabular-nums">{{ count(value(current, platform)) }}</strong>
              <span>{{ platformLabel(platform) }}</span>
            </div>
          </div>
        </div>
      </figure>
      <details class="mt-8px">
        <summary class="cursor-pointer rounded py-4px text-13px focus-visible:outline-solid">
          {{ text('details') }}
        </summary>
        <div class="mt-8px overflow-x-auto">
          <NTable size="small" class="tabular-nums">
            <thead>
              <tr>
                <th scope="col">{{ text('hour') }}</th>
                <th scope="col">{{ text('hourlyDesktop') }}</th>
                <th scope="col">{{ text('hourlyWeb') }}</th>
                <th scope="col">{{ text('cumulativeDesktop') }}</th>
                <th scope="col">{{ text('cumulativeWeb') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="entry in data.hours" :key="entry.hour" :data-table-hour="entry.hour">
                <th scope="row" class="whitespace-nowrap !bg-transparent !font-normal">{{ hourLabel(entry.hour) }}</th>
                <td v-for="key in ['desktop', 'web', 'cumulativeDesktop', 'cumulativeWeb'] as const" :key="key">
                  {{ entry[key] === null ? '—' : count(entry[key]) }}
                </td>
              </tr>
            </tbody>
          </NTable>
        </div>
      </details>
    </template>
  </section>
</template>
