<script setup lang="ts">
import { computed } from 'vue';
import { color } from '@opennavo/tokens';
import OgAppIcon from '../og/OgAppIcon.vue';
import OgFrame from '../og/OgFrame.vue';

// List and article share images (WP13): a label, the page title and subtitle beside up to four
// apps the page lists. Without app icons the brand icon stands in; icons are never invented.
const props = defineProps<{
  label: string;
  title: string;
  description?: string;
  locale?: string;
  tagline: string;
  platform: string;
  site?: string;
  // eslint-disable-next-line vue/no-unused-properties -- Version the signed cache URL without rendering the build ID.
  build?: string;
  icons?: { name: string; token: string; src?: string | null; accent?: string | null }[];
}>();
// Wide CJK characters and long titles need smaller lettering within the fixed canvas; article
// headlines too long for two lines get a third. Every size still fits the frame's 332 px.
const titleStyle = computed(() => {
  const length = Array.from(props.title).reduce((sum, char) => sum + (char.codePointAt(0)! > 0x024f ? 2 : 1), 0);
  if (length > 48) return { fontSize: '48px', lineClamp: 3 };
  return { fontSize: `${length > 36 ? 56 : length > 22 ? 66 : 80}px`, lineClamp: 2 };
});
// Two icons a row; rows center in the column, so a lone icon (or the last of three) sits in its middle.
const iconRows = computed(() => {
  const icons = props.icons?.slice(0, 4) ?? [];
  return [icons.slice(0, 2), icons.slice(2, 4)].filter(row => row.length);
});
</script>

<template>
  <OgFrame :tagline="tagline" :platform="platform" :site="site" :locale="locale">
    <!-- Satori clamps only block text and requires divs to be flex, so clamped text sits in block spans. -->
    <div :style="{ display: 'flex', flexDirection: 'column', width: '724px', flexShrink: 0, gap: '18px' }">
      <span :style="{ display: 'block', fontSize: '26px', lineHeight: 1.3, color: color.text.secondary, lineClamp: 1 }">
        {{ label }}
      </span>
      <span
        class="font-semibold"
        :style="{
          display: 'block',
          ...titleStyle,
          fontWeight: 600,
          lineHeight: 1.1,
          letterSpacing: '-0.03em',
          wordBreak: 'break-word'
        }"
      >
        {{ title }}
      </span>
      <span
        v-if="description"
        :style="{ display: 'block', fontSize: '30px', lineHeight: 1.4, color: color.text.secondary, lineClamp: 2 }"
      >
        {{ description }}
      </span>
    </div>
    <div
      v-if="iconRows.length"
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
