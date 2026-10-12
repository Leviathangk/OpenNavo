<script setup lang="ts">
import { computed } from 'vue';
import type { CSSProperties } from 'vue';
import { useI18n } from 'vue-i18n';
import type { MenuBarStyle, MonitorSample } from '@/ipc/bindings';
import { useMonitorStore } from '@/stores/monitor';
import type { DisplayModule } from '@/stores/monitor';
import { NONE, shownStyle } from './meta';

// Preview of one menu bar item as Rust draws it (`monitor/menubar.rs`): the same text, fonts, baselines and width.
// Like the item's image it has no margin; whoever shows it adds the space AppKit leaves between items.
const props = defineProps<{ module: DisplayModule; styleKind?: MenuBarStyle }>();

const { t } = useI18n();
const store = useMonitorStore();
const style = computed(() => shownStyle(props.module, props.styleKind ?? store.item(props.module).style));

/** Rust draws these labels in English in every locale, as Stats does. */
const LABELS: Partial<Record<DisplayModule, string>> = {
  cpu: 'CPU',
  gpu: 'GPU',
  memory: 'RAM',
  disk: 'DISK',
  battery: 'BAT'
};
/** Clear space either side of the graph's value; the graph takes x 2–42 and the value starts at 45. */
const PAD = 2;
const GRAPH = 45;

type Font = { size: number; weight: number; tabular?: boolean; baseline: number };
type Layout = { fonts: Font[]; left: number; minWidth: number; pad: number; centered: boolean };

const UNITS = [
  [1e9, 'G'],
  [1e6, 'M'],
  [1e3, 'K']
] as const;
/** At most three digits ("8.5M", "12.3M", "120M"), as Rust's `rate`. */
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

const value = computed(() => {
  const sample = store.sample;
  if (!sample) return NONE;
  switch (props.module) {
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
  return NONE;
});
/** The widest reading the module can show: the item keeps that width, so its neighbors never shift. */
const widest = computed(() => {
  switch (props.module) {
    case 'disk': {
      const volume = store.sample && root(store.sample);
      return volume ? `${Math.round(volume.totalBytes / 1e9)}G` : '';
    }
    case 'network':
      return '↓99.9M';
    case 'sensors':
      return '44°';
  }
  return '100%';
});
/** Label over value, upload over download, or the value alone. */
const lines = computed(() => {
  if (style.value !== 'text') return [value.value];
  if (props.module === 'network')
    return [store.sample ? `↑${rate(store.sample.network.upBytesPerSec)}` : NONE, value.value];
  const label = LABELS[props.module];
  return label ? [label, value.value] : [value.value];
});

/** The module's main value as 0–1; network is scaled to its recent peak, a temperature to 100 °C. */
function main(sample: MonitorSample, peak: number) {
  switch (props.module) {
    case 'cpu':
      return sample.cpu.usage;
    case 'gpu':
      return sample.gpu?.usage ?? null;
    case 'memory':
      return sample.memory.totalBytes ? sample.memory.usedBytes / sample.memory.totalBytes : null;
    case 'network':
      return sample.network.downBytesPerSec / peak;
    case 'sensors': {
      const celsius = hottest(sample);
      return celsius == null ? null : celsius / 100;
    }
  }
  return null;
}
const clamp = (ratio: number) => Math.min(1, Math.max(0, ratio));
/** Rust's graph in a 22 pt box: 30 readings over x 2–41 with a gap where sampling paused, or bars across x 2–42. */
const graph = computed(() => {
  if (style.value !== 'line' && style.value !== 'bars') return null;
  const recent = store.history.slice(-30);
  const peak = Math.max(
    1,
    ...recent.map(entry => Math.max(entry.network.downBytesPerSec, entry.network.upBytesPerSec))
  );
  if (style.value === 'line') {
    const values: (number | null)[] = Array.from({ length: 30 - recent.length }, () => null);
    let last: MonitorSample | undefined;
    for (const entry of recent) {
      if (last && entry.at - last.at > 6000) values[values.length - 1] = null;
      const reading = main(entry, peak);
      values.push(reading == null ? null : clamp(reading));
      last = entry;
    }
    let path = '';
    let drawing = false;
    values.forEach((reading, index) => {
      if (reading == null) {
        drawing = false;
        return;
      }
      const x = 2 + (index * 39) / Math.max(1, values.length - 1);
      path += `${drawing ? 'L' : 'M'}${x.toFixed(2)} ${(19 - reading * 16).toFixed(2)}`;
      drawing = true;
    });
    return { path, bars: [] };
  }
  const sample = store.sample;
  const readings = !sample
    ? []
    : props.module === 'cpu'
      ? sample.cpu.cores
      : props.module === 'network'
        ? [sample.network.downBytesPerSec / peak, sample.network.upBytesPerSec / peak]
        : [main(sample, peak)].filter((reading): reading is number => reading != null);
  const step = 40 / Math.max(1, readings.length);
  return {
    path: '',
    bars: readings.map((reading, index) => ({
      x: 2 + index * step,
      y: 19 - clamp(reading) * 16,
      width: Math.max(0.3, step - 1),
      height: clamp(reading) * 16
    }))
  };
});

/** Rust's `layout()`: font and baseline (up from the bottom of 22 pt) of each line, and how the item is sized. */
const layout = computed<Layout>(() => {
  if (graph.value)
    return {
      fonts: [{ size: 12, weight: 500, tabular: true, baseline: 6.5 }],
      left: GRAPH,
      minWidth: 0,
      pad: PAD,
      centered: true
    };
  if (props.module === 'sensors')
    return { fonts: [{ size: 13, weight: 400, baseline: 6.5 }], left: 2, minWidth: 0, pad: 4, centered: false };
  if (lines.value.length === 1)
    return { fonts: [{ size: 12, weight: 400, baseline: 6.5 }], left: 0, minWidth: 31, pad: 0, centered: true };
  if (props.module === 'network') {
    const font = { size: 9, weight: 500, tabular: true };
    return {
      fonts: [
        { ...font, baseline: 13 },
        { ...font, baseline: 3 }
      ],
      left: PAD,
      minWidth: 0,
      pad: PAD,
      centered: true
    };
  }
  return {
    fonts: [
      { size: 7, weight: 300, baseline: 14 },
      { size: 12, weight: 400, baseline: 3 }
    ],
    left: 0,
    minWidth: 31,
    pad: 0,
    centered: false
  };
});
/** Each line in its font, plus the widest reading unseen in the value's font: the widest row sets the item's width. */
const rows = computed(() => {
  const { fonts } = layout.value;
  return [
    ...fonts.map((font, index) => ({ text: lines.value[index] ?? '', font, hidden: false })),
    { text: widest.value, font: fonts.at(-1)!, hidden: true }
  ];
});
const fontStyle = (font: Font): CSSProperties => ({
  fontSize: `${font.size}px`,
  fontWeight: font.weight,
  fontVariantNumeric: font.tabular ? 'tabular-nums' : undefined
});
</script>

<template>
  <span
    class="relative inline-grid h-22px shrink-0 text-[#E6E6E6]"
    role="img"
    :aria-label="`${t(`monitor.modules.${module}`)} ${value}`"
    :data-style="style"
    :style="{ minWidth: `${layout.minWidth}px`, paddingLeft: `${layout.left}px`, paddingRight: `${layout.pad}px` }"
  >
    <svg v-if="graph" class="absolute left-0 top-0" width="45" height="22" aria-hidden="true">
      <path v-if="graph.path" :d="graph.path" fill="none" stroke="currentColor" stroke-width="1.2" />
      <rect
        v-for="(bar, index) in graph.bars"
        :key="index"
        :x="bar.x"
        :y="bar.y"
        :width="bar.width"
        :height="bar.height"
        fill="currentColor"
      />
    </svg>
    <span
      v-for="(row, index) in rows"
      :key="index"
      class="col-start-1 row-start-1 flex items-baseline"
      :class="{ 'justify-center': layout.centered, invisible: row.hidden }"
    >
      <!-- An empty box's baseline is its bottom edge, so the text's baseline lands where AppKit draws it. -->
      <span :style="{ height: `${22 - row.font.baseline}px` }"></span>
      <span class="whitespace-nowrap leading-none" :style="fontStyle(row.font)">{{ row.text }}</span>
    </span>
  </span>
</template>
