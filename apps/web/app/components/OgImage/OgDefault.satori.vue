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
  // eslint-disable-next-line vue/no-unused-properties -- Version the signed cache URL without rendering the build ID.
  build?: string;
  variant: 'home' | 'discover';
  icons?: { name: string; token: string; src?: string | null; accent?: string | null }[];
}>();
const titleLines = computed(() => props.title.split('\n'));
// Under a title of at most two lines the description has room for a third (the Russian download copy needs it).
const descriptionLines = computed(() => (titleLines.value.length > 2 ? 2 : 3));
// Missing discovery data falls back to the real brand asset, never invented app icons.
const iconRows = computed(() => {
  const icons = props.icons?.slice(0, 4) ?? [];
  return [icons.slice(0, 2), icons.slice(2, 4)].filter(row => row.length);
});
</script>

<template>
  <OgFrame :tagline="tagline" :platform="platform" :site="site" :locale="locale">
    <!-- Satori clamps only block text and requires divs to be flex, so clamped text sits in block spans. -->
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
        <span v-for="line in titleLines" :key="line" :style="{ display: 'block', lineClamp: 1 }">{{ line }}</span>
      </div>
      <span
        v-if="description"
        :style="{
          display: 'block',
          fontSize: '30px',
          lineHeight: 1.4,
          color: color.text.secondary,
          whiteSpace: 'pre-wrap',
          lineClamp: descriptionLines
        }"
      >
        {{ description }}
      </span>
    </div>
    <div
      v-if="variant === 'discover' && iconRows.length"
      :style="{
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
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
