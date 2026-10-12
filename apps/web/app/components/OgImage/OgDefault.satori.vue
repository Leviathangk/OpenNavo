<script setup lang="ts">
import { computed } from 'vue';
import { color } from '@opennavo/tokens';
import OgAppIcon from '../og/OgAppIcon.vue';
import OgFrame from '../og/OgFrame.vue';

const props = defineProps<{
  title: string;
  description?: string;
  locale?: string;
  tagline: string;
  platform: string;
  site?: string;
  variant: 'home' | 'discover';
  icons?: { name: string; token: string; src?: string | null; accent?: string | null }[];
}>();
const titleLines = computed(() => props.title.split('\n'));
// Missing discovery data falls back to the real brand asset, never invented app icons.
const iconRows = computed(() => {
  const icons = props.icons?.slice(0, 4) ?? [];
  return [icons.slice(0, 2), icons.slice(2, 4)].filter(row => row.length);
});
</script>

<template>
  <OgFrame :tagline="tagline" :platform="platform" :site="site" :locale="locale">
    <div
      :style="{
        display: 'flex',
        flexDirection: 'column',
        width: '724px',
        flexShrink: 0,
        gap: '20px'
      }"
    >
      <div
        class="font-semibold"
        :style="{
          display: 'flex',
          flexDirection: 'column',
          fontSize: variant === 'home' ? '68px' : '80px',
          fontWeight: 600,
          lineHeight: 1.08,
          letterSpacing: '-0.03em'
        }"
      >
        <div v-for="line in titleLines" :key="line" :style="{ lineClamp: 1 }">{{ line }}</div>
      </div>
      <div
        v-if="description"
        :style="{
          fontSize: '30px',
          lineHeight: 1.4,
          color: color.text.secondary,
          whiteSpace: 'pre-wrap',
          lineClamp: 2
        }"
      >
        {{ description }}
      </div>
    </div>
    <div
      v-if="variant === 'discover' && iconRows.length"
      :style="{
        display: 'flex',
        flexDirection: 'column',
        gap: '20px',
        width: '300px',
        flexShrink: 0
      }"
    >
      <div v-for="(row, index) in iconRows" :key="index" :style="{ display: 'flex', gap: '20px' }">
        <OgAppIcon
          v-for="icon in row"
          :key="icon.token"
          :name="icon.name"
          :token="icon.token"
          :src="icon.src"
          :accent="icon.accent"
          :size="140"
        />
      </div>
    </div>
    <OgAppIcon v-else name="OpenNavo" token="opennavo" src="/icon-512.png" :size="300" />
  </OgFrame>
</template>
