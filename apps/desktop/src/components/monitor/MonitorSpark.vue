<script setup lang="ts">
import { computed } from 'vue';

// Small line chart for the last minute: short gaps from a slower sampling rate stay connected, while a real pause
// still breaks the line instead of dropping to zero or pretending that the value stayed constant.
const props = withDefaults(
  defineProps<{
    values: readonly (number | null)[];
    color: string;
    /** Upper bound; defaults to the largest value (rates), 1 for ratios. */
    max?: number;
    height?: number;
    fill?: boolean;
    label?: string;
  }>(),
  { height: 44, fill: true }
);

const WIDTH = 300;
/** Background menu-bar sampling can be as slow as five seconds. Bridge that expected gap, but keep sleep/offline gaps. */
const MAX_BRIDGED_GAP_SLOTS = 5;
const segments = computed(() => {
  const n = props.values.length;
  if (n < 2) return [];
  const top = props.max ?? Math.max(1, ...props.values.map(value => value ?? 0)) * 1.1;
  const y = (value: number) => props.height - (Math.min(value, top) / top) * (props.height - 4) - 2;
  const runs: string[][] = [];
  let run: string[] = [];
  let lastIndex: number | null = null;
  props.values.forEach((value, index) => {
    if (value == null) return;
    if (lastIndex != null && index - lastIndex - 1 > MAX_BRIDGED_GAP_SLOTS) {
      if (run.length) runs.push(run);
      run = [];
    }
    run.push(`${((index / (n - 1)) * WIDTH).toFixed(1)},${y(value).toFixed(1)}`);
    lastIndex = index;
  });
  if (run.length) runs.push(run);
  return runs;
});
</script>

<template>
  <svg
    class="block w-full"
    :viewBox="`0 0 ${WIDTH} ${height}`"
    preserveAspectRatio="none"
    :height="height"
    :role="label ? 'img' : undefined"
    :aria-label="label"
    :aria-hidden="label ? undefined : 'true'"
  >
    <template v-for="(points, index) in segments" :key="index">
      <circle
        v-if="points.length === 1"
        :cx="points[0]!.split(',')[0]"
        :cy="points[0]!.split(',')[1]"
        r="2"
        :fill="color"
      />
      <polygon
        v-if="fill && points.length > 1"
        :points="`${points[0]!.split(',')[0]},${height} ${points.join(' ')} ${points.at(-1)!.split(',')[0]},${height}`"
        :fill="color"
        fill-opacity="0.14"
      />
      <polyline
        v-if="points.length > 1"
        :points="points.join(' ')"
        fill="none"
        :stroke="color"
        stroke-width="1.6"
        vector-effect="non-scaling-stroke"
      />
    </template>
  </svg>
</template>
