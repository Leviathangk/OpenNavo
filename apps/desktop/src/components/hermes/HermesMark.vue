<script setup lang="ts">
import { computed, useId } from 'vue';
import { LOGO } from '@opennavo/shared';

const props = withDefaults(defineProps<{ size?: number }>(), { size: 24 });
const gradient = `hermes-${useId()}`;
// Polaris is 11.3 units tall in the logo's 24-unit box; use the fuller small cut at 32 px and below.
const compact = computed(() => props.size <= 32);
const star = computed(() => (compact.value ? LOGO.small.innerPath : LOGO.innerPath));
const transform = computed(() =>
  compact.value ? 'translate(-15.25 2.71) scale(1.65)' : 'translate(-15.8648 1.8) scale(1.7)'
);
</script>

<template>
  <svg
    :width="size"
    :height="size"
    viewBox="0 0 24 24"
    xmlns="http://www.w3.org/2000/svg"
    class="shrink-0"
    aria-hidden="true"
  >
    <defs>
      <linearGradient :id="gradient" x1="0" y1="0" x2="1" y2="1">
        <stop v-for="stop in LOGO.outerGradient" :key="stop.offset" :offset="stop.offset" :stop-color="stop.color" />
      </linearGradient>
      <linearGradient :id="`${gradient}-spark`" x1="0" y1="0" x2="1" y2="1">
        <stop
          v-for="stop in LOGO.innerGradientOnLight"
          :key="stop.offset"
          :offset="stop.offset"
          :stop-color="stop.color"
        />
      </linearGradient>
    </defs>
    <g :transform="transform">
      <path :d="star" :fill="`url(#${gradient})`" />
    </g>
    <path
      d="M19 14.9c.23 1.9.68 2.35 2.58 2.58-1.9.23-2.35.68-2.58 2.58-.23-1.9-.68-2.35-2.58-2.58 1.9-.23 2.35-.68 2.58-2.58Z"
      :fill="`url(#${gradient}-spark)`"
    />
  </svg>
</template>
