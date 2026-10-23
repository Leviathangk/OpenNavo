<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { OnSegmented } from '@opennavo/ui';
import type { DisplayModule } from '@/stores/monitor';
import FanControl from './FanControl.vue';
import MonitorDiskDevices from './MonitorDiskDevices.vue';
import MonitorSpark from './MonitorSpark.vue';
import { CHART } from './meta';
import { useModuleView } from './useModuleView';
import type { ModuleVariant, MonitorRange } from './useModuleView';

// One module's numbers in three sizes (monitor proposal §12): a card on the page, the menu bar popover, the detail sheet.
const props = withDefaults(defineProps<{ module: DisplayModule; variant: ModuleVariant; range?: MonitorRange }>(), {
  range: 'minute'
});
const emit = defineEmits<{ cleanup: []; 'update:range': [range: MonitorRange] }>();

const { t } = useI18n();
const { f, s, sys, detail, hour, chartHeight, view, cores, fans, fanName, fanRanges, primaryVolume, cleanupHint } =
  useModuleView(
    () => props.module,
    () => props.variant,
    () => props.range
  );
const ranges = computed(() => [
  { value: 'minute' as const, label: t('monitor.detail.range60') },
  { value: 'hour' as const, label: t('monitor.detail.range1h') }
]);
</script>

<template>
  <div v-if="view" class="flex flex-col" :class="detail ? 'gap-14px' : 'gap-8px'">
    <div v-if="variant !== 'card'" class="flex items-baseline gap-x-8px" :class="detail ? 'flex-wrap gap-y-2px' : ''">
      <span
        class="whitespace-nowrap font-600 tabular-nums tracking-[-0.01em]"
        :class="[detail ? 'text-28px' : 'text-22px', view.warn ? 'text-status-warning' : 'text-ink-primary']"
        data-testid="monitor-value"
        >{{ view.value }}</span
      >
      <span class="min-w-0 text-12.5px text-ink-tertiary" :class="detail ? '' : 'truncate'">{{ view.sub }}</span>
    </div>
    <p v-else class="m-0 truncate text-12px text-ink-tertiary">{{ view.sub }}</p>

    <!-- The detail's charts cover the last minute or, from the 5-second trend, the last hour. -->
    <div v-if="detail && view.charts.length" class="-mb-6px flex justify-end">
      <OnSegmented
        :model-value="range"
        :options="ranges"
        size="sm"
        :aria-label="t('monitor.detail.rangeLabel')"
        @update:model-value="value => emit('update:range', value)"
      />
    </div>
    <div v-for="chart in view.charts" :key="chart.key">
      <div v-if="chart.label" class="mb-2px text-11.5px text-ink-tertiary">{{ chart.label }}</div>
      <MonitorSpark
        :values="chart.values"
        :color="chart.color"
        :max="chart.max"
        :height="chart.height ?? chartHeight"
      />
    </div>
    <div v-if="detail && view.charts.length" class="-mt-8px flex justify-between text-11px text-ink-tertiary">
      <span>{{ hour ? t('monitor.detail.hourAgo') : t('monitor.detail.minuteAgo') }}</span
      ><span>{{ t('monitor.detail.now') }}</span>
    </div>

    <!-- Per-core bars: performance cores in coral, efficiency cores in purple. -->
    <div v-if="module === 'cpu' && variant !== 'popover' && cores.length" :class="detail ? 'mt-4px' : ''">
      <div class="flex items-end gap-3px" :class="detail ? 'h-58px' : 'h-28px'">
        <i
          v-for="(core, index) in cores"
          :key="index"
          class="block min-h-2px flex-1 rounded-[2px] opacity-85"
          :style="{
            height: `${Math.round(core.value * 100)}%`,
            background: core.efficiency ? CHART.efficiency : CHART.cpu
          }"
        ></i>
      </div>
      <div
        v-if="detail && sys?.performanceCores != null"
        class="mt-4px flex justify-between text-11px text-ink-tertiary"
      >
        <span>{{ t('monitor.cpu.performanceLegend', { count: sys.performanceCores }) }}</span>
        <span>{{ t('monitor.cpu.efficiencyLegend', { count: sys.efficiencyCores ?? 0 }) }}</span>
      </div>
    </div>

    <!-- Memory composition and disk / battery fill bars. -->
    <div
      v-if="module === 'memory' && s"
      class="flex h-8px gap-2px overflow-hidden rounded-[4px]"
      role="img"
      :aria-label="view.sub"
    >
      <i class="block min-w-2px" :style="{ flex: s.memory.appBytes, background: 'var(--on-chart-series1)' }"></i>
      <i class="block min-w-2px" :style="{ flex: s.memory.wiredBytes, background: 'var(--on-status-info)' }"></i>
      <i class="block min-w-2px" :style="{ flex: s.memory.compressedBytes, background: 'var(--on-chart-series2)' }"></i>
      <i
        class="block min-w-2px"
        :style="{
          flex: Math.max(0, s.memory.totalBytes - s.memory.usedBytes),
          background: 'var(--on-chart-deemphasis)'
        }"
      ></i>
    </div>
    <div
      v-if="module === 'disk' && primaryVolume && !detail"
      class="flex h-8px gap-2px overflow-hidden rounded-[4px]"
      role="img"
      :aria-label="view.sub"
    >
      <i
        class="block"
        :style="{
          flex: primaryVolume.totalBytes - primaryVolume.freeBytes,
          background: 'var(--on-chart-series1)'
        }"
      ></i>
      <i class="block" :style="{ flex: primaryVolume.freeBytes, background: 'var(--on-chart-deemphasis)' }"></i>
    </div>
    <div
      v-if="module === 'battery' && s?.battery?.percent != null"
      class="flex gap-2px overflow-hidden rounded-[4px]"
      :class="detail ? 'h-10px' : 'h-8px'"
      role="img"
      :aria-label="view.value"
    >
      <i class="block" :style="{ flex: s.battery.percent, background: 'var(--on-status-success)' }"></i>
      <i class="block" :style="{ flex: 1 - s.battery.percent, background: 'var(--on-chart-deemphasis)' }"></i>
    </div>

    <!-- Sensors include the fans, as in Stats: one bar per fan against its own maximum, then their control. -->
    <section v-if="module === 'sensors' && fans.length" class="flex flex-col gap-8px" data-testid="monitor-fans">
      <h3 v-if="variant !== 'card'" class="m-0 text-13px font-600 text-ink-primary">{{ t('monitor.modules.fans') }}</h3>
      <div class="grid grid-cols-[minmax(0,max-content)_1fr_auto] items-center gap-x-10px gap-y-7px text-12px">
        <div v-for="fan in fans" :key="fan.id" class="contents" data-testid="monitor-fan">
          <span class="max-w-160px truncate text-ink-tertiary">{{ fanName(fan) }}</span>
          <span class="h-6px overflow-hidden rounded-[3px] bg-line-subtle">
            <i
              class="block h-full rounded-[3px]"
              :style="{
                width: `${Math.round((fan.rpm / Math.max(1, fan.maxRpm)) * 100)}%`,
                background: CHART.fan
              }"
            ></i>
          </span>
          <span class="text-right tabular-nums text-ink-primary">{{ f.rpm(fan.rpm) }}</span>
        </div>
      </div>
      <dl v-if="fanRanges.length" class="m-0 grid grid-cols-[auto_1fr] gap-x-14px gap-y-6px text-12.5px">
        <template v-for="fanRange in fanRanges" :key="fanRange.key">
          <dt class="text-ink-tertiary">{{ fanRange.label }}</dt>
          <dd class="m-0 text-right tabular-nums text-ink-primary">{{ fanRange.value }}</dd>
        </template>
      </dl>
      <FanControl v-if="variant !== 'card'" :compact="variant === 'popover'" />
    </section>

    <div v-if="view.rows.length" class="flex flex-col gap-8px">
      <h3 v-if="module === 'sensors' && fans.length" class="m-0 text-13px font-600 text-ink-primary">
        {{ t('monitor.sensors.temperatures') }}
      </h3>
      <dl class="m-0 grid grid-cols-[auto_1fr] gap-x-14px gap-y-6px text-12.5px">
        <template v-for="row in view.rows" :key="row.key">
          <dt class="text-ink-tertiary">{{ row.label }}</dt>
          <dd class="m-0 text-right tabular-nums text-ink-primary">{{ row.value }}</dd>
        </template>
      </dl>
    </div>

    <!-- Volumes, cleanup, privacy notes and fan control belong to the larger views. -->
    <div v-if="module === 'disk' && detail" class="flex flex-col gap-10px">
      <h3 class="m-0 text-13px font-600 text-ink-primary">{{ t('monitor.disk.volumes') }}</h3>
      <div v-for="volume in s?.disk.volumes ?? []" :key="volume.mountPoint" class="flex flex-col gap-6px">
        <div class="flex justify-between gap-10px text-12.5px">
          <span class="truncate font-600 text-ink-primary">{{ volume.name }}</span>
          <span class="shrink-0 text-ink-tertiary">{{
            t('monitor.disk.volumeFree', {
              kind: volume.internal ? t('monitor.disk.internal') : t('monitor.disk.external'),
              free: f.bytes(volume.freeBytes),
              total: f.bytes(volume.totalBytes)
            })
          }}</span>
        </div>
        <div class="flex h-6px gap-2px overflow-hidden rounded-[3px]">
          <i
            class="block"
            :style="{
              flex: volume.totalBytes - volume.freeBytes,
              background: 'var(--on-chart-series1)'
            }"
          ></i>
          <i class="block" :style="{ flex: volume.freeBytes, background: 'var(--on-chart-deemphasis)' }"></i>
        </div>
      </div>
    </div>
    <MonitorDiskDevices v-if="module === 'disk' && detail" />
    <button
      v-if="module === 'disk' && variant !== 'popover'"
      type="button"
      class="m-0 inline-flex cursor-pointer items-center gap-5px self-start border-none bg-transparent p-0 text-12px font-500 text-brand-salmon hover:underline"
      data-testid="monitor-cleanup"
      @click.stop="emit('cleanup')"
    >
      {{ cleanupHint }}
    </button>
    <p v-if="module === 'network' && detail" class="m-0 text-12px leading-[1.55] text-ink-tertiary">
      {{ t('monitor.network.privacy') }}
    </p>
    <p v-if="module === 'gpu' && detail" class="m-0 text-12px leading-[1.55] text-ink-tertiary">
      {{ t('monitor.gpu.noProcesses') }}
    </p>
    <p v-if="module === 'sensors' && detail" class="m-0 text-12px leading-[1.55] text-ink-tertiary">
      {{ t('monitor.sensors.note') }}
    </p>
  </div>
  <p v-else class="m-0 text-12px text-ink-tertiary">{{ t('monitor.waiting') }}</p>
</template>
