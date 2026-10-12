<script setup lang="ts">
import { computed } from 'vue';

// Small line chart for the last minute: missing readings break the line instead of dropping to zero.
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
const segments = computed(() => {
  const n = props.values.length;
  if (n < 2) return [];
  const top = props.max ?? Math.max(1, ...props.values.map(value => value ?? 0)) * 1.1;
  const y = (value: number) => props.height - (Math.min(value, top) / top) * (props.height - 4) - 2;
  const runs: string[][] = [];
  let run: string[] = [];
  props.values.forEach((value, index) => {
    if (value == null) {
      if (run.length) runs.push(run);
      run = [];
      return;
    }
    run.push(`${((index / (n - 1)) * WIDTH).toFixed(1)},${y(value).toFixed(1)}`);
  });
  if (run.length) runs.push(run);
  return runs.filter(points => points.length > 1);
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
      <polygon
        v-if="fill"
        :points="`${points[0]!.split(',')[0]},${height} ${points.join(' ')} ${points.at(-1)!.split(',')[0]},${height}`"
        :fill="color"
        fill-opacity="0.14"
      />
      <polyline
        :points="points.join(' ')"
        fill="none"
        :stroke="color"
        stroke-width="1.6"
        vector-effect="non-scaling-stroke"
      />
    </template>
  </svg>
</template>
