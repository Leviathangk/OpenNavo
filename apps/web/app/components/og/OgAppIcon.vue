<script setup lang="ts">
import { computed } from 'vue';
import { cliAbbreviation, darken, iconColor, iconInitials } from '@opennavo/shared';
import type { PackageKind } from '@opennavo/shared';
import { color, radius, shadow } from '@opennavo/tokens';
import { ogIconSrc } from '#shared/utils/ogIcon';

const props = withDefaults(
  defineProps<{
    name: string;
    token: string;
    kind?: PackageKind;
    src?: string | null;
    accent?: string | null;
    size: number;
  }>(),
  { kind: 'cask' }
);
const base = computed(() => iconColor(props.token, props.accent));
// Share images render on the server only, where the private internal base is set.
const config = useRuntimeConfig();
const source = computed(() => ogIconSrc(props.src, config.public.cdnBase, config.cdnBaseInternal));
const box = computed(() => ({
  display: 'flex',
  position: 'relative' as const,
  alignItems: 'center',
  justifyContent: 'center',
  flexShrink: 0,
  width: `${props.size}px`,
  height: `${props.size}px`,
  borderRadius: `${Math.round((props.size * Number.parseFloat(radius.appIcon)) / 100)}px`
}));
</script>

<template>
  <!-- Backgrounds tolerate unavailable icon URLs without failing the entire Satori image. -->
  <div
    v-if="source"
    :style="{
      ...box,
      backgroundImage: `url(${source})`,
      backgroundSize: `${size}px ${size}px`,
      backgroundRepeat: 'no-repeat',
      backgroundPosition: '0% 0%'
    }"
  ></div>
  <div
    v-else
    :style="{
      ...box,
      backgroundImage:
        kind === 'cask'
          ? `linear-gradient(160deg, ${base}, ${darken(base)})`
          : `linear-gradient(160deg, ${color.component.cliIconFrom}, ${color.component.cliIconTo})`,
      color: kind === 'cask' ? color.component.letterIconText : color.component.cliIconText,
      fontSize: `${Math.round(size * (kind === 'cask' ? 0.42 : 0.3))}px`,
      fontWeight: 600,
      letterSpacing: '-0.02em',
      textShadow: shadow.iconText
    }"
  >
    <span>{{ kind === 'cask' ? iconInitials(name) : cliAbbreviation(token) }}</span>
    <div
      v-if="kind === 'formula'"
      :style="{
        position: 'absolute',
        left: '18%',
        right: '18%',
        bottom: '16%',
        height: '3px',
        borderRadius: `${radius.full}px`,
        backgroundColor: base
      }"
    ></div>
  </div>
</template>
