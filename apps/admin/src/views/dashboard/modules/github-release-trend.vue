<script setup lang="ts">
import { computed, ref, useTemplateRef } from 'vue';
import { useElementSize } from '@vueuse/core';
import { useThemeVars } from 'naive-ui';
import { formatCount, formatDate } from '@opennavo/shared';
import type { Schemas } from '@/typings/api/opennavo';
import { $t } from '@/locales';
type ReleaseSeries = 'downloads' | 'updates';

defineOptions({ name: 'GitHubReleaseTrend' });

type Day = Schemas['GitHubReleaseOverview']['days'][number];

const props = defineProps<{
  days: Day[];
  colors: Record<ReleaseSeries, string>;
  locale: App.I18n.LangType;
  title: string;
}>();

// SVG units equal CSS pixels: the plot spans TOP..BOTTOM and the date labels sit in the band below it.
const HEIGHT = 240;
const TOP = 12;
const BOTTOM = 204;
const LEFT = 44;
const RIGHT = 40;

const theme = useThemeVars();
const frame = useTemplateRef<HTMLElement>('frame');
const { width } = useElementSize(frame);
// Hover and keyboard select a day, not a position: the dashboard refreshes every minute, and a shifted or shorter list
// must neither move the selection to another day nor leave it past the end.
const selected = ref<string | null>(null);
const active = computed(() => {
  const index = props.days.findIndex(entry => entry.date === selected.value);
  return index < 0 ? null : index;
});
const select = (index: number | null) => {
  selected.value = index === null ? null : (props.days[index]?.date ?? null);
};
const platforms: ReleaseSeries[] = ['downloads', 'updates'];

const label = (key: ReleaseSeries) => $t(`page.dashboard.githubReleases.${key}`);
const count = (value: number | null) => (value === null ? '—' : formatCount(value, { locale: props.locale }));
// Dates are calendar days of the usage time zone, so they are read as UTC midnight.
const day = (date: string, weekday = false) =>
  formatDate(`${date}T00:00:00Z`, { locale: props.locale, timeZone: 'UTC', weekday });

const chartWidth = computed(() => Math.max(320, Math.round(width.value)));
const step = computed(() => (chartWidth.value - LEFT - RIGHT) / Math.max(1, props.days.length));
const x = (index: number) => LEFT + step.value * (index + 0.5);

// Round axis steps (1, 2, 5 × 10ⁿ) with at most about five intervals; counts never get fractional ticks.
function niceStep(raw: number) {
  const power = 10 ** Math.floor(Math.log10(raw));
  const unit = raw / power;
  return Math.max(1, (unit <= 1 ? 1 : unit <= 2 ? 2 : unit <= 5 ? 5 : 10) * power);
}
const ticks = computed(() => {
  const max = Math.max(1, ...props.days.flatMap(entry => [entry.downloads ?? 0, entry.updates ?? 0]));
  const size = niceStep(max / 4);
  return Array.from({ length: Math.ceil(max / size) + 1 }, (_, index) => index * size);
});
const y = (value: number) => BOTTOM - (value / (ticks.value.at(-1) || 1)) * (BOTTOM - TOP);

// Keep roughly 72 px per date label and always label the latest day.
const dateTicks = computed(() => {
  const total = props.days.length;
  const stride = Math.max(1, Math.ceil(total / Math.max(1, Math.floor((chartWidth.value - LEFT - RIGHT) / 72))));
  return props.days
    .map((entry, index) => ({ index, text: day(entry.date) }))
    .filter(({ index }) => (total - 1 - index) % stride === 0);
});

// Missing snapshot days break each line, so outages never look like zero transfers.
const lines = computed(() =>
  platforms.flatMap(platform => {
    const segments: string[][] = [];
    let points: string[] = [];
    for (const [index, entry] of props.days.entries()) {
      const value = entry[platform];
      if (value === null) {
        if (points.length) segments.push(points);
        points = [];
      } else {
        points.push(`${x(index).toFixed(1)},${y(value).toFixed(1)}`);
      }
    }
    if (points.length) segments.push(points);
    return segments.map((segment, index) => ({ key: `${platform}-${index}`, platform, points: segment.join(' ') }));
  })
);

// Every day gets a marker while they stay readable; longer ranges mark only the latest day and the hovered one.
const dots = computed(() => {
  const last = props.days.length - 1;
  const indexes = new Set(
    props.days
      .map((_, index) => index)
      .filter(
        index =>
          props.days.length <= 14 ||
          index === last ||
          !props.days[index - 1] ||
          props.days[index - 1]!.downloads === null ||
          props.days[index + 1]?.downloads === null
      )
  );
  if (active.value !== null) indexes.add(active.value);
  return [...indexes].flatMap(index =>
    platforms
      .filter(platform => props.days[index]![platform] !== null)
      .map(platform => ({
        key: `${platform}-${index}`,
        cx: x(index),
        cy: y(props.days[index]![platform]!),
        r: index === active.value ? 5 : 4,
        color: props.colors[platform]
      }))
  );
});

// Latest values sit at the line ends unless they would collide; the tooltip and table still carry them.
const endLabels = computed(() => {
  const last = props.days.at(-1);
  if (!last || last.downloads === null || last.updates === null) return [];
  const labels = platforms.map(platform => ({ platform, y: y(last[platform]!), text: count(last[platform]) }));
  return Math.abs(labels[0]!.y - labels[1]!.y) >= 14 ? labels : [];
});

const current = computed(() => (active.value === null ? null : (props.days[active.value] ?? null)));
const tooltipStyle = computed(() => {
  const left = x(active.value ?? 0);
  return {
    left: `${left}px`,
    transform: left > chartWidth.value * 0.6 ? 'translateX(calc(-100% - 12px))' : 'translateX(12px)',
    background: theme.value.popoverColor,
    boxShadow: theme.value.boxShadow2,
    color: theme.value.textColor2
  };
});

function move(delta: number) {
  const last = props.days.length - 1;
  select(Math.min(last, Math.max(0, (active.value ?? last) + delta)));
}
</script>

<template>
  <figure class="m-0 min-w-0">
    <figcaption class="mb-8px flex flex-wrap items-center justify-between gap-x-16px gap-y-4px">
      <span class="font-600">{{ title }}</span>
      <span class="flex flex-wrap gap-16px text-12px" :style="{ color: theme.textColor2 }">
        <span v-for="platform in platforms" :key="platform" class="flex items-center gap-6px">
          <span class="h-2px w-16px rounded-full" :style="{ background: colors[platform] }" />
          {{ label(platform) }}
        </span>
      </span>
    </figcaption>
    <div
      ref="frame"
      class="relative rounded-4px outline-none focus-visible:outline-solid"
      tabindex="0"
      @focus="move(0)"
      @blur="select(null)"
      @keydown.left.prevent="move(-1)"
      @keydown.right.prevent="move(1)"
      @keydown.home.prevent="select(0)"
      @keydown.end.prevent="select(days.length - 1)"
    >
      <svg
        :viewBox="`0 0 ${chartWidth} ${HEIGHT}`"
        :height="HEIGHT"
        class="block w-full"
        role="img"
        :aria-label="title"
        @pointerleave="select(null)"
      >
        <title>{{ title }}</title>
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
          <text v-for="tick in dateTicks" :key="tick.index" :x="x(tick.index)" :y="HEIGHT - 12" text-anchor="middle">
            {{ tick.text }}
          </text>
        </g>
        <line
          v-if="active !== null"
          :x1="x(active)"
          :x2="x(active)"
          :y1="TOP"
          :y2="BOTTOM"
          :stroke="theme.borderColor"
          shape-rendering="crispEdges"
        />
        <polyline
          v-for="line in lines"
          :key="line.key"
          :data-series="line.platform"
          :points="line.points"
          fill="none"
          :stroke="colors[line.platform]"
          :stroke-dasharray="line.platform === 'updates' ? '5 4' : undefined"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
        />
        <circle
          v-for="dot in dots"
          :key="dot.key"
          :cx="dot.cx"
          :cy="dot.cy"
          :r="dot.r"
          :fill="dot.color"
          :stroke="theme.cardColor"
          stroke-width="2"
        />
        <text
          v-for="end in endLabels"
          :key="end.platform"
          :x="x(days.length - 1) + 10"
          :y="end.y + 4"
          font-size="12"
          font-weight="600"
          :fill="theme.textColor2"
          class="tabular-nums"
        >
          {{ end.text }}
        </text>
        <rect
          v-for="(entry, index) in days"
          :key="entry.date"
          :x="LEFT + step * index"
          y="0"
          :width="step"
          :height="HEIGHT"
          fill="transparent"
          @pointerenter="select(index)"
        />
      </svg>
      <div
        v-if="current"
        class="pointer-events-none absolute top-4px z-1 min-w-160px rounded-6px px-12px py-8px text-12px"
        :style="tooltipStyle"
        role="status"
        aria-live="polite"
      >
        <div class="mb-4px font-600" :style="{ color: theme.textColor1 }">{{ day(current.date, true) }}</div>
        <div v-for="platform in platforms" :key="platform" class="flex items-center gap-8px leading-20px">
          <span class="h-2px w-12px shrink-0 rounded-full" :style="{ background: colors[platform] }" />
          <strong class="min-w-32px tabular-nums" :style="{ color: theme.textColor1 }">
            {{ count(current[platform]) }}
          </strong>
          <span>{{ label(platform) }}</span>
        </div>
      </div>
    </div>
  </figure>
</template>
