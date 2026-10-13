<script setup lang="ts">
import { computed } from 'vue';
import { color } from '@opennavo/tokens';
import OgAppIcon from '../og/OgAppIcon.vue';
import OgFrame from '../og/OgFrame.vue';

// Category and collection share images (WP13): a label, the page title and subtitle beside up to four
// apps the page lists. Without app icons the brand icon stands in; icons are never invented.
const props = defineProps<{
  label: string;
  title: string;
  description?: string;
  locale?: string;
  tagline: string;
  platform: string;
  site?: string;
  icons?: { name: string; token: string; src?: string | null; accent?: string | null }[];
}>();
// Wide CJK characters and long titles need smaller lettering within the fixed canvas.
const titleSize = computed(() => {
  const length = Array.from(props.title).reduce((sum, char) => sum + (char.codePointAt(0)! > 0x024f ? 2 : 1), 0);
  return length > 36 ? 56 : length > 22 ? 66 : 80;
});
const iconRows = computed(() => {
  const icons = props.icons?.slice(0, 4) ?? [];
  return [icons.slice(0, 2), icons.slice(2, 4)].filter(row => row.length);
});
</script>

<template>
  <OgFrame :tagline="tagline" :platform="platform" :site="site" :locale="locale">
    <div :style="{ display: 'flex', flexDirection: 'column', width: '724px', flexShrink: 0, gap: '18px' }">
      <div :style="{ fontSize: '26px', lineHeight: 1.3, color: color.text.secondary, lineClamp: 1 }">{{ label }}</div>
      <div
        class="font-semibold"
        :style="{
          fontSize: `${titleSize}px`,
          fontWeight: 600,
          lineHeight: 1.1,
          letterSpacing: '-0.03em',
          wordBreak: 'break-word',
          lineClamp: 2
        }"
      >
        {{ title }}
      </div>
      <div v-if="description" :style="{ fontSize: '30px', lineHeight: 1.4, color: color.text.secondary, lineClamp: 2 }">
        {{ description }}
      </div>
    </div>
    <div
      v-if="iconRows.length"
      :style="{ display: 'flex', flexDirection: 'column', gap: '20px', width: '300px', flexShrink: 0 }"
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
