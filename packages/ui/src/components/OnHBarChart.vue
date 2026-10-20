<script setup lang="ts">
import { computed } from 'vue';
import OnTooltip from './OnTooltip.vue';
import { useUiMessages } from '../composables/locale';

export interface OnHBarItem {
  key: string;
  /** Left label, e.g. last 30 days. */
  label: string;
  value: number;
  /** Bar-end formatted value, e.g. 17K; defaults to value. */
  display?: string;
  /** Full value for hover/focus/table, e.g. 17,394; defaults to display. */
  full?: string;
  /** Current period uses chart.emphasis; others chart.deemphasis. */
  emphasis?: boolean;
}

export interface OnHBarChartProps {
  items: readonly OnHBarItem[];
  /** Chart name, also accessible table caption. */
  caption: string;
  /** Scale maximum, default largest value. */
  max?: number;
}

const props = defineProps<OnHBarChartProps>();

const messages = useUiMessages();

const ceiling = computed(() => props.max ?? Math.max(0, ...props.items.map(item => item.value)));

const rows = computed(() =>
  props.items.map(item => {
    const percent = ceiling.value > 0 ? Math.min(100, Math.max(0, (item.value / ceiling.value) * 100)) : 0;
    const display = item.display ?? String(item.value);
    return { ...item, percent, display, full: item.full ?? display };
  })
);
</script>

<template>
  <!-- Hide graphics from screen readers; full values live in a visually hidden table. Label bar ends and reveal full values on hover. -->
  <figure class="m-0 font-sans">
    <div class="flex flex-col gap-9px" aria-hidden="true">
      <div
        v-for="row in rows"
        :key="row.key"
        class="grid grid-cols-[72px_minmax(0,1fr)] items-center gap-10px text-11.5px text-ink-secondary"
      >
        <span class="break-words">{{ row.label }}</span>
        <div class="relative mr-40px h-16px">
          <span class="on-hbar-axis absolute left-0 w-1px bg-chart-axis"></span>
          <span class="absolute inset-y-0 left-0" :style="{ width: `${row.percent}%` }">
            <OnTooltip :content="`${row.label}：${row.full}`" :delay="100" lazy class="h-full w-full">
              <span
                class="mt-2px block h-12px w-full rounded-r-tiny"
                :class="row.emphasis ? 'bg-chart-emphasis' : 'bg-chart-deemphasis'"
              ></span>
            </OnTooltip>
          </span>
          <span
            class="pointer-events-none absolute top-0 whitespace-nowrap font-500 leading-16px text-ink-primary"
            :style="{ left: `calc(${row.percent}% + 6px)` }"
            >{{ row.display }}</span
          >
        </div>
      </div>
    </div>
    <!-- Table intrinsic minimum width can escape sr-only; apply visual hiding to its wrapper. -->
    <div class="sr-only">
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
          </tr>
        </thead>
        <tbody>
          <tr v-for="row in rows" :key="row.key">
            <th scope="row">{{ row.label }}</th>
            <td>{{ row.full }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </figure>
</template>

<style>
/* Join baselines across 9 px row gaps. */
.on-hbar-axis {
  top: -5px;
  bottom: -4px;
}
</style>
