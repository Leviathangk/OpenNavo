<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import type { ProcessInfo } from '@/ipc/bindings';
import { useAppLocale } from '@/composables/useAppLocale';
import { useMonitorStore } from '@/stores/monitor';
import { CHART, useMonitorFormat } from './meta';

// Why the machine is as busy as it is. The rows above can name only some of the processes: the rest still run but did
// not make the list, ended before they could be read, or are the kernel, root and other users' processes, whose CPU
// OpenNavo cannot read. Shown only once the machine is busy enough for the difference to matter.
const props = defineProps<{ rows: ProcessInfo[]; compact?: boolean }>();

const { t } = useI18n();
const { appLocale } = useAppLocale();
const store = useMonitorStore();
const f = useMonitorFormat();

/** Below this the list explains itself. */
const MIN_USAGE = 0.1;
/** Shares under this stay out of the legend. */
const MIN_SHARE = 0.005;
const SYSTEM = 'rgba(255,255,255,0.32)';

const parts = computed(() => {
  const breakdown = store.sample?.cpuBreakdown;
  if (!breakdown || breakdown.usage < MIN_USAGE) return null;
  const listed = props.rows.reduce((sum, entry) => sum + entry.cpu, 0);
  // A merged entry stands for several processes; only those that did something count as listed.
  const shown = props.rows.reduce((total, entry) => total + (entry.cpu >= 0.001 ? entry.count : 0), 0);
  return {
    listed,
    rest: Math.max(0, breakdown.readable - listed),
    restCount: Math.max(0, breakdown.activeCount - shown),
    finished: breakdown.finished,
    from: breakdown.finishedFrom.map(parent => parent.name),
    other: breakdown.other
  };
});

const segments = computed(() => {
  const value = parts.value;
  if (!value) return [];
  return [
    { key: 'listed', share: value.listed, color: CHART.cpu, opacity: 1 },
    { key: 'rest', share: value.rest, color: CHART.cpu, opacity: 0.5 },
    { key: 'finished', share: value.finished, color: CHART.temperature, opacity: 1 },
    { key: 'other', share: value.other, color: SYSTEM, opacity: 1 }
  ];
});

/** The parts can add up to more than the machine (the host and the processes are read a moment apart): scale them down. */
const fit = computed(() => {
  const total = segments.value.reduce((sum, segment) => sum + segment.share, 0);
  return total > 1 ? 1 / total : 1;
});

const items = computed(() => {
  const value = parts.value;
  if (!value) return [];
  const names = new Intl.ListFormat(appLocale.value, { type: 'conjunction' }).format(value.from);
  return [
    {
      key: 'rest',
      share: value.rest,
      label: value.restCount
        ? t('monitor.processes.breakdown.rest', { count: value.restCount })
        : t('monitor.processes.breakdown.restAny'),
      note: '',
      hint: t('monitor.processes.breakdown.hintRest')
    },
    {
      key: 'finished',
      share: value.finished,
      label: t('monitor.processes.breakdown.finished'),
      note: value.from.length ? t('monitor.processes.breakdown.finishedFrom', { names }) : '',
      hint: t('monitor.processes.breakdown.hintFinished')
    },
    {
      key: 'other',
      share: value.other,
      label: t('monitor.processes.breakdown.other'),
      note: '',
      hint: t('monitor.processes.breakdown.hintOther')
    }
  ].filter(item => item.share >= MIN_SHARE);
});
const dot = (key: string) => segments.value.find(segment => segment.key === key);
</script>

<template>
  <div
    v-if="parts"
    class="flex flex-col gap-6px"
    :class="compact ? 'px-2px pb-4px pt-6px' : 'border-t border-t-solid border-line-subtle px-16px py-12px'"
    data-testid="monitor-cpu-breakdown"
  >
    <div
      class="flex h-6px overflow-hidden rounded-[3px] bg-[rgba(255,255,255,0.06)]"
      role="img"
      :aria-label="t('monitor.processes.breakdown.title')"
    >
      <i
        v-for="segment in segments"
        :key="segment.key"
        class="block h-full"
        :style="{
          width: `${segment.share * fit * 100}%`,
          background: segment.color,
          opacity: segment.opacity
        }"
      ></i>
    </div>
    <ul
      v-if="items.length"
      class="m-0 flex list-none gap-x-12px gap-y-2px p-0 text-11.5px text-ink-tertiary"
      :class="compact ? 'flex-wrap' : 'flex-col'"
    >
      <li
        v-for="item in items"
        :key="item.key"
        class="flex items-center gap-6px"
        :class="compact ? '' : 'justify-between'"
        :title="item.hint"
      >
        <span class="flex min-w-0 items-center gap-6px">
          <i
            class="block h-6px w-6px shrink-0 rounded-full"
            :style="{ background: dot(item.key)?.color, opacity: dot(item.key)?.opacity }"
          ></i>
          <span class="truncate">{{ item.label }}</span>
          <span v-if="item.note" class="truncate">· {{ item.note }}</span>
        </span>
        <span class="tabular-nums text-ink-secondary">{{ f.percent(item.share) }}</span>
      </li>
    </ul>
  </div>
</template>
