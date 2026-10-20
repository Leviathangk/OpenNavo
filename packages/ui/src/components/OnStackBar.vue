<script setup lang="ts">
import { computed } from 'vue';
import OnTooltip from './OnTooltip.vue';
import { formatPercent } from '@opennavo/shared';
import { useUiLocale, useUiMessages } from '../composables/locale';

export interface OnStackItem {
  key: string;
  label: string;
  value: number;
  /** Formatted legend/tooltip value, e.g. 12.1 GB; defaults to value. */
  display?: string;
}

export interface OnStackBarProps {
  items: readonly OnStackItem[];
  /** Chart name, also the accessible table caption. */
  caption: string;
  /** Format entries without display, including merged Other segments. */
  format?: (value: number) => string;
}

const props = defineProps<OnStackBarProps>();

const messages = useUiMessages();
const locale = useUiLocale();

// Use category colors in order without cycling (§11); with >3 segments, merge the third onward into Other.
const SERIES = ['bg-chart-series1', 'bg-chart-series2', 'bg-chart-series3'] as const;

const segments = computed(() => {
  const format = props.format ?? String;
  const positive = props.items
    .filter(item => item.value > 0)
    .map(item => ({ ...item, display: item.display ?? format(item.value) }));
  let merged = positive;
  if (positive.length > SERIES.length) {
    const rest = positive.slice(SERIES.length - 1).reduce((sum, item) => sum + item.value, 0);
    merged = [
      ...positive.slice(0, SERIES.length - 1),
      { key: 'other', label: messages.value.chart.other, value: rest, display: format(rest) }
    ];
  }
  const total = merged.reduce((sum, item) => sum + item.value, 0);
  return merged.map((item, index) => ({
    ...item,
    color: SERIES[index] as string,
    percent: formatPercent(total > 0 ? item.value / total : 0, { locale: locale.value })
  }));
});
</script>

<template>
  <figure class="m-0 font-sans">
    <div class="flex h-12px gap-2px" aria-hidden="true">
      <OnTooltip
        v-for="(segment, index) in segments"
        :key="segment.key"
        :content="`${segment.label}：${segment.display}（${segment.percent}）`"
        :delay="100"
        lazy
        class="min-w-2px"
        :style="{ flex: `${segment.value} 1 0` }"
      >
        <span
          class="block h-12px w-full"
          :class="[
            segment.color,
            index === 0 ? 'rounded-l-tiny' : '',
            index === segments.length - 1 ? 'rounded-r-tiny' : ''
          ]"
        ></span>
      </OnTooltip>
    </div>
    <ul class="m-0 mt-12px flex list-none flex-col gap-8px p-0" aria-hidden="true">
      <li v-for="segment in segments" :key="segment.key" class="flex items-center gap-8px text-12px text-ink-secondary">
        <span class="h-10px w-10px shrink-0 rounded-tiny" :class="segment.color"></span>
        <span class="min-w-0 truncate">{{ segment.label }}</span>
        <b class="ml-auto font-500 text-ink-primary">{{ segment.display }}</b>
        <span class="min-w-38px shrink-0 text-right tabular-nums text-ink-tertiary">{{ segment.percent }}</span>
      </li>
    </ul>
    <table class="sr-only">
      <caption>
        {{
          caption
        }}
      </caption>
      <thead>
        <tr>
          <th scope="col">{{ messages.chart.item }}</th>
          <th scope="col">{{ messages.chart.value }}</th>
          <th scope="col">{{ messages.chart.share }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="segment in segments" :key="segment.key">
          <th scope="row">{{ segment.label }}</th>
          <td>{{ segment.display }}</td>
          <td>{{ segment.percent }}</td>
        </tr>
      </tbody>
    </table>
  </figure>
</template>
