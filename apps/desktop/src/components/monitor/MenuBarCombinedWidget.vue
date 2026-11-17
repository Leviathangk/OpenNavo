<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import type { MonitorSample } from '@/ipc/bindings';
import { MODULES, useMonitorStore } from '@/stores/monitor';
import type { DisplayModule } from '@/stores/monitor';
import { NONE } from './meta';

// Preview of the optional combined menu bar item as Rust draws it (`monitor/menubar.rs`): one rotating
// 12 pt line in menu bar order, with the same English labels and per-metric width as the native item.
const { t } = useI18n();
const store = useMonitorStore();

const LABELS: Partial<Record<DisplayModule, string>> = {
  cpu: 'CPU',
  gpu: 'GPU',
  memory: 'RAM',
  disk: 'DISK',
  network: 'NET',
  battery: 'BAT'
};
const UNITS = [
  [1e9, 'G'],
  [1e6, 'M'],
  [1e3, 'K']
] as const;
function rate(bytes: number) {
  const [scale, unit] = UNITS.find(([size]) => bytes >= size * 0.9995) ?? ([1, 'B'] as const);
  const value = bytes / scale;
  return unit !== 'K' && unit !== 'B' && value < 99.95 ? `${value.toFixed(1)}${unit}` : `${Math.round(value)}${unit}`;
}
const percent = (ratio: number) => `${Math.round(ratio * 100)}%`;
const root = (sample: MonitorSample) => sample.disk.volumes.find(volume => volume.mountPoint === '/');
const hottest = (sample: MonitorSample) => {
  const values = (sample.sensors ?? []).map(reading => reading.celsius);
  return values.length ? Math.max(...values) : null;
};

const modules = computed(() => MODULES.filter(module => store.item(module).enabled && store.modules.includes(module)));
const index = ref(0);
const current = computed(() => (modules.value.length ? modules.value[index.value % modules.value.length] : null));
let timer: ReturnType<typeof setInterval> | undefined;
onMounted(() => {
  timer = setInterval(() => {
    index.value = (index.value + 1) % Math.max(1, modules.value.length);
  }, 5_000);
});
onBeforeUnmount(() => clearInterval(timer));
function part(module: DisplayModule) {
  const sample = store.sample;
  const value = (() => {
    if (!sample) return NONE;
    switch (module) {
      case 'cpu':
        return percent(sample.cpu.usage);
      case 'gpu':
        return sample.gpu ? percent(sample.gpu.usage) : NONE;
      case 'memory':
        return percent(sample.memory.totalBytes ? sample.memory.usedBytes / sample.memory.totalBytes : 0);
      case 'disk': {
        const volume = root(sample);
        return volume ? `${Math.round(volume.freeBytes / 1e9)}G` : NONE;
      }
      case 'network':
        return `↓${rate(sample.network.downBytesPerSec)}`;
      case 'battery':
        return sample.battery ? percent(sample.battery.percent) : NONE;
      case 'sensors': {
        const celsius = hottest(sample);
        return celsius == null ? NONE : `${Math.round(celsius)}°`;
      }
    }
  })();
  const widest = (() => {
    switch (module) {
      case 'disk':
        return sample && root(sample) ? `${Math.round(root(sample)!.totalBytes / 1e9)}G` : '';
      case 'network':
        return '↓99.9M';
      case 'sensors':
        return '44°';
      default:
        return '100%';
    }
  })();
  const label = LABELS[module];
  return {
    value: label ? `${label} ${value}` : value,
    widest: label ? `${label} ${widest}` : widest
  };
}
const text = computed(() => (current.value ? part(current.value).value : NONE));
const widest = computed(() => (current.value ? part(current.value).widest : ''));
const label = computed(() =>
  current.value ? `${t(`monitor.modules.${current.value}`)} ${text.value}` : t('monitor.rail.empty')
);
</script>

<template>
  <span
    class="relative inline-grid h-22px shrink-0 text-component-menu-bar-text"
    role="img"
    :aria-label="label"
    data-testid="monitor-menubar-combined"
  >
    <span class="col-start-1 row-start-1 flex items-baseline pl-2px pr-2px">
      <span :style="{ height: '15.5px' }"></span>
      <span class="whitespace-nowrap text-12px font-400 leading-none">{{ text || NONE }}</span>
    </span>
    <span class="invisible col-start-1 row-start-1 flex items-baseline pl-2px pr-2px">
      <span :style="{ height: '15.5px' }"></span>
      <span class="whitespace-nowrap text-12px font-400 leading-none">{{ widest || text || NONE }}</span>
    </span>
  </span>
</template>
