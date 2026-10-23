<script setup lang="ts">
import { computed, useId } from 'vue';
import { BRAND, LOGO, logoInlineShift } from '@opennavo/shared';

export interface OnLogoProps {
  /** Mark height, 22 alongside wordmark (08 §2). */
  size?: number;
  /** Show OpenNavo wordmark. */
  wordmark?: boolean;
  /** Inline alignment in items-center parent centers N with text; automatic with wordmark (08 §2). */
  inline?: boolean;
  /** Polaris-only decoration for badges/eyebrows (08 §2), hidden from screen readers. */
  star?: boolean;
}

const props = withDefaults(defineProps<OnLogoProps>(), { size: 22 });

// Multiple logos may share a page; gradient IDs must be unique.
const id = useId();
// Star-only decoration keeps main geometry; full marks use small geometry at 32 px and below.
const geometry = computed(() => (!props.star && props.size <= 32 ? LOGO.small : LOGO));
// Tight logo bounds preserve inline spacing with proportional width; standalone star uses square bounds.
const viewBox = computed(() => (props.star ? LOGO.starViewBox : geometry.value.markViewBox));
const width = computed(() =>
  props.star ? props.size : Math.round(props.size * geometry.value.markAspect * 100) / 100
);
const decorative = computed(() => props.wordmark || props.star);
// Bounds center N plus star, leaving N low; raise inline to center N, or the star's area centroid, with text.
const shift = computed(() => {
  if (props.star) return logoInlineShift(LOGO.starCenterOffset);
  return props.wordmark || props.inline ? logoInlineShift(geometry.value.nCenterOffset) : undefined;
});
</script>

<template>
  <span class="inline-flex items-center gap-8px">
    <svg
      :viewBox="viewBox"
      :width="width"
      :height="size"
      xmlns="http://www.w3.org/2000/svg"
      class="shrink-0"
      :style="shift ? { transform: shift } : undefined"
      :role="decorative ? undefined : 'img'"
      :aria-label="decorative ? undefined : BRAND.name"
      :aria-hidden="decorative ? 'true' : undefined"
    >
      <defs>
        <linearGradient v-if="!star" :id="`${id}-outer`" x1="0" y1="0" x2="1" y2="1">
          <stop v-for="stop in LOGO.outerGradient" :key="stop.offset" :offset="stop.offset" :stop-color="stop.color" />
        </linearGradient>
        <!-- Polaris follows the theme: warm white on dark, LOGO.innerGradientOnLight's amber on light (tokens). -->
        <linearGradient :id="`${id}-inner`" x1="0" y1="0" x2="1" y2="1">
          <stop
            v-for="(stop, index) in LOGO.innerGradient"
            :key="stop.offset"
            :offset="stop.offset"
            :stop-color="stop.color"
            :style="{ stopColor: `var(--on-brand-logo-core-gradient-${index + 1}, ${stop.color})` }"
          />
        </linearGradient>
      </defs>
      <path v-if="!star" :fill="`url(#${id}-outer)`" :d="geometry.outerPath" />
      <path :fill="`url(#${id}-inner)`" :d="geometry.innerPath" />
    </svg>
    <span v-if="wordmark" class="text-15px font-600 tracking-[-0.01em] text-brand-coral">{{ BRAND.name }}</span>
  </span>
</template>
