<script setup lang="ts">
import { formatPercent } from '@opennavo/shared';
import { OnLogo } from '@opennavo/ui';

// Monitor demo (desktop menu bar items and Monitor modules): live readings in the menu bar and a popover with
// CPU history, memory, the hottest sensor and fan speed. Readings are illustrative and cycle every 1.2 seconds.
const FRAMES = [
  { cpu: 0.23, memory: 0.61, temp: 52, fan: 2450 },
  { cpu: 0.31, memory: 0.62, temp: 54, fan: 2580 },
  { cpu: 0.18, memory: 0.61, temp: 53, fan: 2510 },
  { cpu: 0.27, memory: 0.63, temp: 55, fan: 2640 }
] as const;
const DURATIONS = FRAMES.map(() => 1200);
// CPU history as fractions; each frame shifts the window one sample.
const HISTORY = [0.18, 0.22, 0.2, 0.31, 0.26, 0.19, 0.24, 0.35, 0.28, 0.21, 0.25, 0.33, 0.23, 0.31, 0.18, 0.27];

const { t, locale } = useI18n();
const formattingLocale = computed(() => toAppLocale(locale.value));
const root = ref<HTMLElement>();
const step = useDemoLoop(root, DURATIONS);
const frame = computed(() => FRAMES[step.value] ?? FRAMES[0]);

const percent = (value: number) => formatPercent(value, { locale: formattingLocale.value });
// Units are technical and stay untranslated, as in the desktop monitor (components/monitor/meta.ts).
const rpm = computed(() => `${new Intl.NumberFormat(formattingLocale.value).format(frame.value.fan)}\u00a0rpm`);
const celsius = computed(() => `${frame.value.temp}\u00a0°C`);
// Labels are the desktop menu bar's own (monitor/menubar.rs), which are not translated.
const bar = computed(() => [
  { label: 'CPU', value: percent(frame.value.cpu) },
  { label: 'RAM', value: percent(frame.value.memory) }
]);

// Twelve samples ending at the current frame, drawn into a 240×48 box.
const line = computed(() => {
  const samples = Array.from({ length: 12 }, (_, index) => HISTORY[(step.value + index) % HISTORY.length] ?? 0);
  const points = samples.map((value, index) => `${(index * 240) / 11},${48 - value * 110}`);
  return { stroke: `M${points.join(' L')}`, fill: `M0,48 L${points.join(' L')} L240,48 Z` };
});
</script>

<template>
  <div ref="root" class="mx-auto flex max-w-330px flex-col" inert aria-hidden="true">
    <div
      class="flex h-30px items-center justify-end gap-12px rounded-default border border-solid border-line-subtle bg-surface-inset px-10px text-11.5px text-ink-secondary"
    >
      <!-- Menu bar items follow the desktop's Stats-style mini widget: a small label over the value. -->
      <span v-for="item in bar" :key="item.label" class="flex flex-col items-start leading-none">
        <span class="text-7px font-600 text-ink-tertiary">{{ item.label }}</span>
        <span class="mt-1px text-11px font-500 text-ink-primary tabular-nums">{{ item.value }}</span>
      </span>
      <span class="text-12px font-500 text-ink-primary tabular-nums">{{ frame.temp }}°</span>
      <span class="rounded-tiny bg-surface-raised px-5px py-1px"><OnLogo :size="13" /></span>
      <span class="tabular-nums">9:41</span>
    </div>
    <div
      class="mt-8px overflow-hidden rounded-panel border border-solid border-line-default bg-surface-card p-14px shadow-popover"
    >
      <div class="flex items-center justify-between gap-8px">
        <span class="text-12px font-600 text-ink-primary">{{ t('landing.toolkit.demo.modules.cpu') }}</span>
        <span class="flex items-center gap-6px text-11.5px text-status-success">
          <span class="h-6px w-6px rounded-full bg-status-success"></span>{{ t('landing.toolkit.demo.health') }}
        </span>
      </div>
      <div class="mt-6px flex items-end gap-10px">
        <span class="text-26px font-600 leading-none tracking-[-0.02em] text-ink-primary tabular-nums">{{
          percent(frame.cpu)
        }}</span>
        <svg viewBox="0 0 240 48" class="h-40px min-w-0 flex-1" preserveAspectRatio="none">
          <path :d="line.fill" class="landing-monitor-area" />
          <path :d="line.stroke" fill="none" stroke-width="1.6" class="stroke-brand-coral" />
        </svg>
      </div>
      <div class="mt-12px grid grid-cols-3 gap-8px border-t border-t-solid border-line-subtle pt-10px">
        <span class="flex flex-col gap-2px">
          <span class="text-10.5px text-ink-tertiary">{{ t('landing.toolkit.demo.modules.memory') }}</span>
          <span class="text-13px font-600 text-ink-primary tabular-nums">{{ percent(frame.memory) }}</span>
        </span>
        <span class="flex flex-col gap-2px">
          <span class="text-10.5px text-ink-tertiary">{{ t('landing.toolkit.demo.modules.sensors') }}</span>
          <span class="text-13px font-600 text-ink-primary tabular-nums">{{ celsius }}</span>
        </span>
        <span class="flex flex-col gap-2px">
          <span class="text-10.5px text-ink-tertiary">{{ t('landing.toolkit.demo.modules.fans') }}</span>
          <span class="text-13px font-600 text-ink-primary tabular-nums">{{ rpm }}</span>
        </span>
      </div>
    </div>
  </div>
</template>

<style>
.landing-monitor-area {
  fill: color-mix(in srgb, var(--on-brand-coral) 16%, transparent);
}
</style>
