<script setup lang="ts">
import { computed, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { convertFileSrc, isTauri } from '@tauri-apps/api/core';
import { OnAppIcon, OnButton, OnConfirm, OnSegmented } from '@opennavo/ui';
import type { ProcessInfo } from '@/ipc/bindings';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { useMonitorStore } from '@/stores/monitor';
import MonitorCpuBreakdown from './MonitorCpuBreakdown.vue';
import { useMonitorFormat } from './meta';
import type { ProcessSort } from './meta';
import { useProcessView } from './useProcessView';

// Top processes (decisions 5 and 9): CPU share of the whole machine, memory as phys_footprint, and from M3 disk and
// network rates; only GUI apps can be quit, after confirmation, the same as ⌘Q in the app. Each row also says how
// many cores that share is, a view can merge an app's helpers into one row, and under the CPU list the rest of the
// machine's busy share is accounted for, so the rows are never mistaken for the whole story.
const props = withDefaults(defineProps<{ sort?: ProcessSort; limit?: number; tabs?: boolean; compact?: boolean }>(), {
  sort: 'cpu',
  limit: 8,
  tabs: false,
  compact: false
});
/** With tabs the parent owns the order (v-model:sort), so health actions can switch it. */
const emit = defineEmits<{ 'update:sort': [sort: ProcessSort] }>();

const { t } = useI18n();
const store = useMonitorStore();
const toasts = useToasts();
const f = useMonitorFormat();
const { errorText } = usePackageState();

const order = computed(() => props.sort);
const { view, set: setView } = useProcessView();
const viewOptions = computed(() => [
  { value: 'process' as const, label: t('monitor.processes.viewProcess') },
  { value: 'app' as const, label: t('monitor.processes.viewApp') }
]);
const processes = computed(() => {
  const sample = store.sample;
  return (view.value === 'app' ? (sample?.processGroups ?? sample?.processes) : sample?.processes) ?? [];
});
const cores = computed(() => store.system?.logicalCores ?? 0);
/** Disk and network rates arrive only while a visible view shows those modules; offer the tabs once they do. */
const tabOptions = computed(() => [
  { value: 'cpu' as const, label: t('monitor.processes.byCpu') },
  { value: 'memory' as const, label: t('monitor.processes.byMemory') },
  ...(processes.value.some(entry => entry.diskReadBytesPerSec != null)
    ? [{ value: 'disk' as const, label: t('monitor.modules.disk') }]
    : []),
  ...(processes.value.some(entry => entry.netDownBytesPerSec != null)
    ? [{ value: 'network' as const, label: t('monitor.modules.network') }]
    : [])
]);
const TITLES: Record<ProcessSort, string> = {
  cpu: 'monitor.processes.topCpu',
  memory: 'monitor.processes.topMemory',
  disk: 'monitor.processes.topDisk',
  network: 'monitor.processes.topNetwork'
};
const metric = (entry: ProcessInfo) => {
  switch (order.value) {
    case 'cpu':
      return entry.cpu ?? 0;
    case 'memory':
      return entry.memoryBytes;
    case 'disk':
      return (entry.diskReadBytesPerSec ?? 0) + (entry.diskWriteBytesPerSec ?? 0);
    case 'network':
      return (entry.netDownBytesPerSec ?? 0) + (entry.netUpBytesPerSec ?? 0);
  }
  return 0;
};
/** Idle processes are left out of the disk and network lists rather than shown at 0 B/s. */
const list = computed(() =>
  [...processes.value]
    .filter(entry => order.value === 'cpu' || order.value === 'memory' || metric(entry) > 0)
    .sort((a, b) => metric(b) - metric(a))
    .slice(0, props.limit)
);
const top = computed(() => Math.max(1e-9, ...list.value.map(entry => metric(entry))));
const display = (entry: ProcessInfo) =>
  order.value === 'cpu'
    ? f.percent(entry.cpu)
    : order.value === 'memory'
      ? f.bytes(entry.memoryBytes)
      : f.speed(metric(entry));
/** Hover detail: the Activity Monitor scale for CPU (each core counts as 100%), the two directions for I/O. */
function hint(entry: ProcessInfo) {
  switch (order.value) {
    case 'cpu':
      return entry.cpu == null || !store.system
        ? undefined
        : t('monitor.processes.activityValue', { value: f.percent(entry.cpu * store.system.logicalCores) });
    case 'disk':
      return t('monitor.processes.diskSplit', {
        read: f.speed(entry.diskReadBytesPerSec),
        write: f.speed(entry.diskWriteBytesPerSec)
      });
    case 'network':
      return `↓ ${f.speed(entry.netDownBytesPerSec)} · ↑ ${f.speed(entry.netUpBytesPerSec)}`;
  }
  return undefined;
}
/** What a CPU share is in cores, so a busy Mac's 11% reads as one core and not as "almost nothing". */
const coresText = (entry: ProcessInfo) => {
  if (order.value !== 'cpu' || !cores.value || entry.cpu * cores.value < 0.05) return '';
  return t('monitor.processes.cores', { value: f.decimal(entry.cpu * cores.value, 1) });
};
const icon = (entry: ProcessInfo) => (entry.iconPath && isTauri() ? convertFileSrc(entry.iconPath) : null);
const tag = (entry: ProcessInfo) =>
  entry.kind === 'system'
    ? t('monitor.processes.system')
    : entry.kind === 'background'
      ? t('monitor.processes.background')
      : '';

const quitting = ref<ProcessInfo | null>(null);
const busy = ref(false);
async function quit() {
  const entry = quitting.value;
  if (!entry) return;
  busy.value = true;
  try {
    await store.quitApp(entry.pid);
    toasts.push({ tone: 'success', title: t('monitor.processes.quitDone', { name: entry.name }) });
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  } finally {
    busy.value = false;
    quitting.value = null;
  }
}
async function activityMonitor() {
  try {
    await store.openActivityMonitor();
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}
</script>

<template>
  <section
    class="overflow-hidden"
    :class="compact ? '' : 'rounded-big border border-solid border-line-subtle bg-surface-card'"
    data-testid="monitor-processes"
  >
    <header
      class="flex items-center gap-10px"
      :class="compact ? 'px-2px pb-2px pt-6px' : 'border-b border-b-solid border-line-subtle py-10px pl-16px pr-12px'"
    >
      <b class="font-600" :class="compact ? 'text-11.5px text-ink-tertiary' : 'text-13.5px text-ink-primary'">{{
        t(TITLES[order])
      }}</b>
      <OnSegmented
        v-if="tabs"
        :model-value="sort"
        :options="tabOptions"
        size="sm"
        :aria-label="t('monitor.processes.sortBy')"
        @update:model-value="value => emit('update:sort', value)"
      />
      <span class="flex-1"></span>
      <OnSegmented
        :model-value="view"
        :options="viewOptions"
        size="sm"
        :aria-label="t('monitor.processes.view')"
        data-testid="monitor-process-view"
        @update:model-value="setView"
      />
      <OnButton v-if="tabs" variant="ghost" size="sm" icon-right="arrow-up-right" @click="activityMonitor">{{
        t('monitor.processes.activityMonitor')
      }}</OnButton>
    </header>
    <p
      v-if="order === 'cpu' && cores"
      class="m-0 text-11px text-ink-tertiary"
      :class="compact ? 'px-2px pb-4px' : 'px-16px pt-8px'"
    >
      {{ t('monitor.processes.scaleCpu', { percent: f.percent(1 / cores) }) }}
    </p>
    <ul class="m-0 list-none p-0">
      <li
        v-for="entry in list"
        :key="entry.pid"
        class="group flex items-center gap-10px border-t border-t-solid border-[rgba(255,255,255,0.04)] first:border-t-0"
        :class="compact ? 'h-34px px-2px' : 'h-40px pl-16px pr-12px'"
      >
        <span class="flex min-w-0 items-center gap-9px" :class="compact ? 'flex-1' : 'w-210px'">
          <OnAppIcon
            kind="cask"
            :token="entry.bundleId ?? entry.name"
            :name="entry.name"
            :src="icon(entry)"
            :size="compact ? 20 : 24"
          />
          <span class="truncate text-12.5px text-ink-primary">{{ entry.name }}</span>
          <span v-if="entry.count > 1" class="shrink-0 text-11.5px tabular-nums text-ink-tertiary"
            >×{{ entry.count }}</span
          >
          <span
            v-if="entry.isSelf"
            class="shrink-0 rounded-[5px] bg-[rgba(255,255,255,0.06)] px-6px text-10.5px leading-18px text-ink-tertiary"
            >{{ t('monitor.processes.self') }}</span
          >
          <span
            v-if="tag(entry)"
            class="shrink-0 rounded-[5px] bg-[rgba(255,255,255,0.06)] px-6px text-10.5px leading-18px text-ink-tertiary"
            >{{ tag(entry) }}</span
          >
        </span>
        <span v-if="!compact" class="h-6px min-w-48px flex-1 overflow-hidden rounded-[3px] bg-[rgba(255,255,255,0.06)]">
          <i
            class="block h-full rounded-[3px] bg-brand-coral"
            :style="{ width: `${Math.max(2, (metric(entry) / top) * 100)}%` }"
          ></i>
        </span>
        <span
          class="whitespace-nowrap text-right text-12.5px tabular-nums text-ink-secondary"
          :class="order === 'cpu' ? 'w-[124px]' : 'w-84px'"
          :title="hint(entry)"
          >{{ display(entry)
          }}<span v-if="coresText(entry)" class="ml-6px text-11.5px text-ink-tertiary">{{
            coresText(entry)
          }}</span></span
        >
        <span v-if="!compact" class="flex w-52px justify-end">
          <OnButton
            v-if="entry.kind === 'app' && entry.quittable"
            variant="ghost"
            size="xs"
            class="opacity-0 group-hover:opacity-100 focus-visible:opacity-100"
            @click="quitting = entry"
            >{{ t('monitor.processes.quit') }}</OnButton
          >
        </span>
      </li>
    </ul>
    <p v-if="!list.length" class="m-0 px-16px py-12px text-12px text-ink-tertiary">
      {{ t('monitor.waiting') }}
    </p>
    <MonitorCpuBreakdown v-if="order === 'cpu' && list.length" :rows="list" :compact="compact" />
    <OnConfirm
      :open="Boolean(quitting)"
      :title="t('monitor.processes.quitTitle', { name: quitting?.name ?? '' })"
      :description="t('monitor.processes.quitBody', { name: quitting?.name ?? '' })"
      :confirm-label="t('monitor.processes.quit')"
      :loading="busy"
      @update:open="value => (value ? undefined : (quitting = null))"
      @confirm="quit"
    />
  </section>
</template>
