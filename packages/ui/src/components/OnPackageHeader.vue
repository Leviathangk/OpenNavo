<script setup lang="ts">
import { computed } from 'vue';
import type { Component } from 'vue';
import { detailGlow } from '@opennavo/tokens';
import OnAppIcon from './OnAppIcon.vue';
import type { OnAppIconKind } from './OnAppIcon.vue';
import type { OnImageVariant } from '../utils/image';
import OnChip from './OnChip.vue';
import type { OnChipTone } from './OnChip.vue';

// Detail header (08 §10.5, mockup 03, ADR-017): 96 icon, name, developer/summary, badges, host actions, optional caption.
// Shared web/desktop: desktop update/get/open follows local state; web shows Get menu/homepage.
export interface OnPackageHeaderChip {
  key: string;
  label: string;
  tone?: OnChipTone;
  outline?: boolean;
  dot?: boolean;
  /** Chips with an address are links, e.g. the app's categories on the web. */
  href?: string;
}

export interface OnPackageHeaderProps {
  kind: OnAppIconKind;
  token: string;
  name: string;
  /** Appended to the name inside the h1 for subpages, e.g. "Release Notes" on the versions page. */
  headingSuffix?: string;
  /** Developer and summary. */
  subtitle?: string;
  iconUrl?: string | null;
  /** Stored WebP renditions of the icon (X3). */
  iconVariants?: readonly OnImageVariant[] | null;
  accent?: string | null;
  chips?: readonly OnPackageHeaderChip[];
  /** Caption beneath actions, e.g. a link app's download domain or an installation date. */
  meta?: string;
  /** Web: prioritize loading this largest above-the-fold image. */
  priority?: boolean;
  /** Pair web actions in equal columns on small screens, with natural button widths on desktop. */
  stretchActions?: boolean;
  /** Link component for chips with href (NuxtLink/RouterLink); native a by default. */
  linkAs?: string | Component;
}

const props = withDefaults(defineProps<OnPackageHeaderProps>(), { chips: () => [], priority: false, linkAs: 'a' });

const linkAttrs = (href: string) => (props.linkAs === 'a' ? { href } : { to: href });

defineSlots<{ actions?: () => unknown; tags?: () => unknown; default?: () => unknown }>();

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
      :variants="iconVariants"
      :accent="accent"
      :size="96"
      :priority="priority"
    />
    <div class="relative min-w-0 flex-1">
      <!-- The space lives in the interpolation: template whitespace next to the span would be condensed away. -->
      <h1 class="m-0 text-30px font-600 leading-[1.15] tracking-[-0.025em] text-ink-primary">
        {{ name
        }}<span v-if="headingSuffix" class="text-18px font-500 tracking-normal text-ink-secondary">{{
          ` ${headingSuffix}`
        }}</span>
      </h1>
      <p v-if="subtitle" class="m-0 mt-4px text-14px text-ink-secondary md:truncate">{{ subtitle }}</p>
      <div
        v-if="chips.length || $slots.tags"
        class="mt-12px flex flex-wrap items-center gap-6px"
        data-testid="package-header-tags"
      >
        <template v-for="chip in chips" :key="chip.key">
          <component
            :is="linkAs"
            v-if="chip.href"
            v-bind="linkAttrs(chip.href)"
            class="rounded-small no-underline outline-none transition-opacity duration-fast hover:opacity-80 focus-visible:shadow-focus-ring"
          >
            <OnChip size="md" :tone="chip.tone" :outline="chip.outline" :dot="chip.dot">{{ chip.label }}</OnChip>
          </component>
          <OnChip v-else size="md" :tone="chip.tone" :outline="chip.outline" :dot="chip.dot">
            {{ chip.label }}
          </OnChip>
        </template>
        <slot name="tags" />
      </div>
    </div>
    <div
      class="relative flex shrink-0 flex-col items-start gap-10px md:items-end"
      :class="stretchActions ? 'w-full md:w-auto' : ''"
    >
      <div
        v-if="$slots.actions"
        class="items-center gap-8px"
        :class="stretchActions ? 'grid w-full grid-cols-2 md:w-auto md:flex md:justify-end' : 'flex'"
        data-testid="package-header-actions"
      >
        <slot name="actions" />
      </div>
      <small v-if="meta" class="text-12px text-ink-tertiary">{{ meta }}</small>
    </div>
    <!-- Default slot: header overlays, such as desktop uninstall confirmation. -->
    <slot />
  </section>
</template>

<style>
/* Upper-right accent glow (mockup .dhead .bgglow), softened on light backgrounds. */
.on-detail-glow {
  right: -80px;
  top: -140px;
  width: 520px;
  height: 420px;
  opacity: var(--on-effect-accent-glow-opacity);
}
</style>
