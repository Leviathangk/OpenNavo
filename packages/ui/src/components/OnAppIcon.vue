<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue';
import { cliAbbreviation, darken, iconColor, iconInitials } from '@opennavo/shared';
import type { PackageKind } from '@opennavo/shared';
import { imageSrcset } from '../utils/image';
import type { OnImageVariant } from '../utils/image';

export type OnAppIconSize = 20 | 24 | 26 | 28 | 30 | 32 | 40 | 44 | 46 | 54 | 64 | 96 | 118;

/** Catalog package kinds plus Mac App Store apps, which only ever fall back to a letter tile. */
export type OnAppIconKind = PackageKind | 'mas';

export interface OnAppIconProps {
  kind: OnAppIconKind;
  token: string;
  name: string;
  /** Remote/local extracted icon, falling back to a tile on load failure. */
  src?: string | null;
  /** Stored WebP renditions of `src` (64/128/256 px, X3); the browser picks one for the size and pixel density. */
  variants?: readonly OnImageVariant[] | null;
  size?: OnAppIconSize;
  /** Rendered CSS width when the icon lives in a scaled scene; pixel density is handled by srcset. */
  sizes?: string;
  /** Package accent for letter-tile background. */
  accent?: string | null;
  /** Provide for standalone icons without adjacent names; read as an image, otherwise decorative. */
  label?: string;
  /** Critical first-screen icon (detail/hero): eager loading and higher priority (05 §8). */
  priority?: boolean;
}

const props = withDefaults(defineProps<OnAppIconProps>(), { size: 44 });

const failed = ref(false);
const image = ref<HTMLImageElement>();

// SSR images may fail before hydration attaches error handlers; recheck on mount.
onMounted(() => {
  const element = image.value;
  if (element?.complete && element.naturalWidth === 0) failed.value = true;
});

watch(
  () => props.src,
  () => {
    failed.value = false;
  }
);

const showImage = computed(() => Boolean(props.src) && !failed.value);
const srcset = computed(() => imageSrcset(props.variants));
const box = computed(() => ({ width: `${props.size}px`, height: `${props.size}px` }));

// App letter tile (casks and App Store apps): accent or token hash, 160° gradient darkened 18%, text 42% of side (08 §8.10).
const letterColor = computed(() => iconColor(props.token, props.accent));
const letterStyle = computed(() => ({
  '--on-tile-from': letterColor.value,
  '--on-tile-to': darken(letterColor.value),
  fontSize: `${Math.round(props.size * 0.42)}px`
}));

// Formula terminal tile: monospace abbreviation at 30% of side, bottom bar colored only by token hash.
const cliStyle = computed(() => ({ fontSize: `${Math.round(props.size * 0.3)}px` }));
const barColor = computed(() => iconColor(props.token));
</script>

<template>
  <span
    class="relative box-border inline-grid shrink-0 place-items-center"
    :class="{ 'overflow-hidden rounded-app-icon shadow-app-icon': !showImage }"
    :style="box"
    :role="label ? 'img' : undefined"
    :aria-label="label"
    :aria-hidden="label ? undefined : 'true'"
  >
    <img
      v-if="showImage"
      ref="image"
      :src="src ?? undefined"
      :srcset="srcset"
      :sizes="srcset ? (sizes ?? `${size}px`) : undefined"
      alt=""
      :loading="priority ? 'eager' : 'lazy'"
      :fetchpriority="priority ? 'high' : undefined"
      decoding="async"
      class="h-full w-full object-contain"
      @error="failed = true"
    />
    <span
      v-else-if="kind !== 'formula'"
      class="on-letter-tile grid h-full w-full place-items-center font-600 leading-none text-component-letter-icon-text"
      :style="letterStyle"
    >
      {{ iconInitials(name) }}
    </span>
    <span v-else class="on-cli-tile relative grid h-full w-full place-items-center" :style="cliStyle">
      <span class="font-mono font-600 leading-none text-component-cli-icon-text">{{ cliAbbreviation(token) }}</span>
      <span class="on-cli-bar absolute h-2px rounded-full" :style="{ backgroundColor: barColor }"></span>
    </span>
  </span>
</template>

<style>
.on-letter-tile {
  background: linear-gradient(160deg, var(--on-tile-from), var(--on-tile-to));
  text-shadow: var(--on-shadow-icon-text);
}

.on-cli-tile {
  background: linear-gradient(160deg, var(--on-component-cli-icon-from), var(--on-component-cli-icon-to));
  letter-spacing: -0.04em;
}

.on-cli-bar {
  left: 18%;
  right: 18%;
  bottom: 16%;
}
</style>
