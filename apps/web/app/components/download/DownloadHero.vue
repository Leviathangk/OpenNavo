<script setup lang="ts">
import type { PackageSummary } from '@opennavo/api';
import { OnAppIcon } from '@opennavo/ui';
import type { OnAppIconSize } from '@opennavo/ui';

// Download hero: client icon at the center of the cover glow and dashed orbits; at ≥1280 popular apps sit on the orbits.
// Bleeds to the content column; the backdrop mask fades into whichever surface hosts it instead of painting a fade color.
const props = defineProps<{ apps: readonly PackageSummary[] }>();

defineSlots<{ default?: () => unknown }>();

const RINGS = [112, 184, 272, 376, 496] as const;

// Ring radius and angle (degrees, counterclockwise from the right) around the client icon; chosen to clear the
// title/body/button boxes at ≥1280 width, where the content column is at least 1056 wide.
const SLOTS: {
  radius: number;
  angle: number;
  size: OnAppIconSize;
  rotate: number;
  duration: number;
  delay: number;
  opacity: number;
}[] = [
  { radius: 184, angle: 166, size: 54, rotate: -8, duration: 7, delay: 0, opacity: 1 },
  { radius: 184, angle: 12, size: 46, rotate: 9, duration: 7.6, delay: -2.4, opacity: 1 },
  { radius: 376, angle: 186, size: 46, rotate: 7, duration: 8.4, delay: -3, opacity: 0.85 },
  { radius: 376, angle: -3, size: 44, rotate: -7, duration: 8, delay: -5, opacity: 0.85 },
  { radius: 496, angle: 203, size: 40, rotate: -5, duration: 6.6, delay: -1.5, opacity: 0.6 },
  { radius: 496, angle: -21, size: 40, rotate: 6, duration: 7.2, delay: -4, opacity: 0.6 }
];

const orbit = computed(() =>
  props.apps.slice(0, SLOTS.length).map((pkg, index) => {
    const slot = SLOTS[index] as (typeof SLOTS)[number];
    const radians = (slot.angle * Math.PI) / 180;
    return {
      pkg,
      size: slot.size,
      style: {
        left: `${Math.round(slot.radius * Math.cos(radians) - slot.size / 2)}px`,
        top: `${Math.round(-slot.radius * Math.sin(radians) - slot.size / 2)}px`,
        opacity: String(slot.opacity)
      },
      bob: {
        '--download-rotate': `${slot.rotate}deg`,
        animationDuration: `${slot.duration}s`,
        animationDelay: `${slot.delay}s`
      }
    };
  })
);
</script>

<template>
  <section class="download-hero relative isolate overflow-hidden">
    <div class="download-hero-backdrop pointer-events-none absolute inset-0" aria-hidden="true">
      <div class="download-hero-center absolute left-1/2">
        <div class="download-hero-glow absolute rounded-full"></div>
        <svg class="download-hero-rings absolute" width="1000" height="1000" viewBox="0 0 1000 1000" fill="none">
          <g class="download-hero-ring">
            <circle v-for="radius in RINGS" :key="radius" cx="500" cy="500" :r="radius" />
          </g>
        </svg>
        <span
          v-for="item in orbit"
          :key="item.pkg.token"
          class="download-hero-orbit absolute hidden xl:block"
          :style="item.style"
        >
          <span class="download-hero-bob block" :style="item.bob">
            <OnAppIcon
              kind="cask"
              :token="item.pkg.token"
              :name="item.pkg.displayName"
              :src="item.pkg.iconUrl"
              :variants="item.pkg.iconVariants"
              :accent="item.pkg.accentColor"
              :size="item.size"
            />
          </span>
        </span>
      </div>
    </div>

    <div
      class="relative mx-auto box-border flex max-w-816px flex-col items-center px-16px pb-48px pt-40px text-center md:px-28px md:pb-56px md:pt-56px"
    >
      <!-- The heading names the product; the icon is decorative. -->
      <img
        class="download-hero-icon block h-96px w-96px md:h-128px md:w-128px"
        src="/icon-512.png"
        width="128"
        height="128"
        alt=""
        aria-hidden="true"
        decoding="async"
        fetchpriority="high"
      />
      <slot />
    </div>
  </section>
</template>

<style>
/* Center of the client icon: top padding plus half the icon size. */
.download-hero-center {
  top: 88px;
}

@media (min-width: 768px) {
  .download-hero-center {
    top: 120px;
  }
}

/* Fade glow/orbits at the top under the toolbar and over the lower half, so no cover edge shows on any surface. */
.download-hero-backdrop {
  mask: linear-gradient(180deg, transparent, var(--on-text-primary) 48px, var(--on-text-primary) 52%, transparent 96%);
}

.download-hero-glow {
  left: -440px;
  top: -440px;
  width: 880px;
  height: 880px;
  background: var(--on-effect-cover-glow);
  animation: download-breathe 8s ease-in-out infinite;
}

/* Rotate the whole dashed-orbit SVG on the compositor so dashes drift without repainting. */
.download-hero-rings {
  left: -500px;
  top: -500px;
  animation: download-spin 240s linear infinite;
}

.download-hero-ring {
  stroke: var(--on-effect-ring-stroke);
  stroke-dasharray: var(--on-effect-ring-dasharray);
}

.download-hero-orbit {
  filter: drop-shadow(var(--on-shadow-float-icon));
}

.download-hero-bob {
  transform: rotate(var(--download-rotate));
  animation: download-bob 8s ease-in-out infinite alternate;
}

.download-hero-icon {
  filter: drop-shadow(var(--on-shadow-float-icon));
  animation: download-float 6s ease-in-out infinite alternate;
}

@keyframes download-breathe {
  0%,
  100% {
    opacity: 1;
    scale: 1;
  }
  50% {
    opacity: 0.86;
    scale: 1.03;
  }
}

@keyframes download-spin {
  to {
    rotate: 360deg;
  }
}

@keyframes download-bob {
  from {
    transform: translateY(-6px) rotate(var(--download-rotate));
  }
  to {
    transform: translateY(6px) rotate(calc(var(--download-rotate) + 3deg));
  }
}

@keyframes download-float {
  from {
    transform: translateY(-5px);
  }
  to {
    transform: translateY(5px);
  }
}

/* Reduced motion: hold a static frame rather than base.css's shortened single iteration. */
@media (prefers-reduced-motion: reduce) {
  .download-hero *,
  .download-hero *::before,
  .download-hero *::after {
    animation: none !important;
  }
}
</style>
