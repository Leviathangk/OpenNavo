<script setup lang="ts">
import { computed } from 'vue';
import type { PackageKind } from '@opennavo/shared';
import { color } from '@opennavo/tokens';
import OgAppIcon from '../og/OgAppIcon.vue';
import OgFrame from '../og/OgFrame.vue';

const props = defineProps<{
  name: string;
  summary?: string;
  kind: PackageKind;
  token: string;
  iconUrl?: string | null;
  accent?: string | null;
  locale?: string;
  tagline: string;
  platform: string;
  site?: string;
}>();
// Wide CJK characters and long names need smaller lettering within the fixed share-image canvas.
const titleSize = computed(() => {
  const length = Array.from(props.name).reduce((sum, char) => sum + (char.codePointAt(0)! > 0x024f ? 2 : 1), 0);
  return length > 36 ? 56 : length > 22 ? 68 : 88;
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
        gap: '22px'
      }"
    >
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
        {{ name }}
      </div>
      <div v-if="summary" :style="{ fontSize: '32px', lineHeight: 1.4, color: color.text.secondary, lineClamp: 2 }">
        {{ summary }}
      </div>
    </div>
    <OgAppIcon :name="name" :token="token" :kind="kind" :src="iconUrl" :accent="accent" :size="300" />
  </OgFrame>
</template>
