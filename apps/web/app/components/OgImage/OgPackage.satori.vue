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
  // eslint-disable-next-line vue/no-unused-properties -- Version the signed cache URL without rendering the build ID.
  build?: string;
  // eslint-disable-next-line vue/no-unused-properties -- Invalidate immutable images when the app version changes.
  contentVersion?: string;
  // eslint-disable-next-line vue/no-unused-properties -- Request-time visibility guard checked by server/middleware/og-visibility.ts.
  visibility?: string;
}>();
// Wide CJK characters and long names need smaller lettering within the fixed share-image canvas.
const titleSize = computed(() => {
  const length = Array.from(props.name).reduce((sum, char) => sum + (char.codePointAt(0)! > 0x024f ? 2 : 1), 0);
  return length > 36 ? 56 : length > 22 ? 68 : 88;
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
        gap: '22px'
      }"
    >
      <span
        class="font-semibold"
        :style="{
          display: 'block',
          fontSize: `${titleSize}px`,
          fontWeight: 600,
          lineHeight: 1.1,
          letterSpacing: '-0.03em',
          wordBreak: 'break-word',
          lineClamp: 2
        }"
      >
        {{ name }}
      </span>
      <span
        v-if="summary"
        :style="{ display: 'block', fontSize: '32px', lineHeight: 1.4, color: color.text.secondary, lineClamp: 2 }"
      >
        {{ summary }}
      </span>
    </div>
    <OgAppIcon :name="name" :token="token" :kind="kind" :src="iconUrl" :accent="accent" :size="300" />
  </OgFrame>
</template>
