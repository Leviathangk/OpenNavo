<script setup lang="ts">
import { computed } from 'vue';
import { LOGO } from '@opennavo/shared';
import { color } from '@opennavo/tokens';

// All 1200×630 cards keep the bottom 126 px empty for X's title overlay.
// Satori needs inline styles, numeric SVG dimensions and static font families.
// Image URLs hash only the top-level template (see useOgFrame), so a change here alone keeps old images cached.
const props = defineProps<{ tagline: string; platform: string; site?: string; locale?: string }>();
defineSlots<{ default?: () => unknown }>();
const font = computed(() => (props.locale?.startsWith('ja') ? 'Inter, OG Noto Sans JP' : 'Inter, OG Noto Sans SC'));
const logoHeight = 64;
</script>

<template>
  <div
    :style="{
      width: '1200px',
      height: '630px',
      display: 'flex',
      position: 'relative',
      overflow: 'hidden',
      backgroundColor: color.surface.page,
      color: color.text.primary,
      fontFamily: font,
      fontWeight: 400
    }"
  >
    <div
      :style="{
        position: 'absolute',
        top: '36px',
        left: '64px',
        right: '64px',
        height: '76px',
        display: 'flex',
        justifyContent: 'space-between',
        alignItems: 'flex-start'
      }"
    >
      <div :style="{ display: 'flex', alignItems: 'center', gap: '20px' }">
        <svg :viewBox="LOGO.markViewBox" :width="Math.round(logoHeight * LOGO.markAspect)" :height="logoHeight">
          <defs>
            <linearGradient id="og-brand-n" x1="0" y1="0" x2="1" y2="1">
              <stop
                v-for="stop in LOGO.outerGradient"
                :key="stop.offset"
                :offset="stop.offset"
                :stop-color="stop.color"
              />
            </linearGradient>
            <linearGradient id="og-brand-star" x1="0" y1="0" x2="1" y2="1">
              <stop
                v-for="stop in LOGO.innerGradient"
                :key="stop.offset"
                :offset="stop.offset"
                :stop-color="stop.color"
              />
            </linearGradient>
          </defs>
          <path fill="url(#og-brand-n)" :d="LOGO.outerPath" />
          <path fill="url(#og-brand-star)" :d="LOGO.innerPath" />
        </svg>
        <div :style="{ display: 'flex', flexDirection: 'column', gap: '2px' }">
          <div :style="{ display: 'flex', alignItems: 'baseline', gap: '12px' }">
            <span
              :style="{
                fontSize: '38px',
                fontWeight: 600,
                lineHeight: 1.2,
                letterSpacing: '-0.02em'
              }"
              >OpenNavo</span
            >
            <span :style="{ fontSize: '24px', color: color.text.secondary }">- {{ platform }}</span>
          </div>
          <div :style="{ fontSize: '20px', lineHeight: 1.35, color: color.text.secondary }">
            {{ tagline }}
          </div>
        </div>
      </div>
      <span v-if="site" :style="{ fontSize: '24px', color: color.text.secondary, marginTop: '8px' }">{{ site }}</span>
    </div>
    <div
      :style="{
        position: 'absolute',
        top: '156px',
        left: '64px',
        right: '64px',
        height: '332px',
        display: 'flex',
        alignItems: 'center',
        gap: '48px',
        overflow: 'hidden'
      }"
    >
      <slot />
    </div>
  </div>
</template>
