<script setup lang="ts">
import { computed } from 'vue';
import { detailGlow } from '@opennavo/tokens';
import OnAppIcon from './OnAppIcon.vue';
import type { OnAppIconKind } from './OnAppIcon.vue';
import OnChip from './OnChip.vue';
import type { OnChipTone } from './OnChip.vue';

// Detail header (08 §10.5, mockup 03, ADR-017): 96 icon, name, developer/summary, badges, host actions, source caption.
// Shared web/desktop: desktop update/get/open follows local state; web shows Get menu/homepage.
export interface OnPackageHeaderChip {
  key: string;
  label: string;
  tone?: OnChipTone;
  outline?: boolean;
  dot?: boolean;
}

export interface OnPackageHeaderProps {
  kind: OnAppIconKind;
  token: string;
  name: string;
  /** Developer and summary. */
  subtitle?: string;
  iconUrl?: string | null;
  accent?: string | null;
  chips?: readonly OnPackageHeaderChip[];
  /** Caption beneath actions, e.g. source homebrew/cask and installation date. */
  meta?: string;
  /** Web: prioritize loading this largest above-the-fold image. */
  priority?: boolean;
}

const props = withDefaults(defineProps<OnPackageHeaderProps>(), { chips: () => [], priority: false });

defineSlots<{ actions?: () => unknown; default?: () => unknown }>();

const glow = computed(() => ({ background: detailGlow(props.accent ?? undefined) }));
</script>

<template>
  <section
    class="relative box-border flex flex-col gap-18px overflow-hidden rounded-huge border border-solid border-line-subtle bg-component-detail-header-bg px-26px py-24px md:flex-row md:items-center md:gap-22px"
  >
    <div class="on-detail-glow pointer-events-none absolute rounded-full" :style="glow" aria-hidden="true"></div>
    <OnAppIcon
      class="relative"
      :kind="kind"
      :token="token"
      :name="name"
      :src="iconUrl"
      :accent="accent"
      :size="96"
      :priority="priority"
    />
    <div class="relative min-w-0 flex-1">
      <h1 class="m-0 text-30px font-600 leading-[1.15] tracking-[-0.025em] text-ink-primary">{{ name }}</h1>
      <p v-if="subtitle" class="m-0 mt-4px text-14px text-ink-secondary md:truncate">{{ subtitle }}</p>
      <div v-if="chips.length" class="mt-12px flex flex-wrap gap-6px">
        <OnChip
          v-for="chip in chips"
          :key="chip.key"
          size="md"
          :tone="chip.tone"
          :outline="chip.outline"
          :dot="chip.dot"
        >
          {{ chip.label }}
        </OnChip>
      </div>
    </div>
    <div class="relative flex shrink-0 flex-col items-start gap-10px md:items-end">
      <div v-if="$slots.actions" class="flex items-center gap-8px">
        <slot name="actions" />
      </div>
      <small v-if="meta" class="text-12px text-ink-tertiary">{{ meta }}</small>
    </div>
    <!-- Default slot: header overlays, such as desktop uninstall confirmation. -->
    <slot />
  </section>
</template>

<style>
/* Upper-right accent glow (mockup .dhead .bgglow). */
.on-detail-glow {
  right: -80px;
  top: -140px;
  width: 520px;
  height: 420px;
}
</style>
