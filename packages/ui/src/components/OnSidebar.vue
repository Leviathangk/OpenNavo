<script setup lang="ts">
import type { Component } from 'vue';
import OnLogo from './OnLogo.vue';
import OnNavItem from './OnNavItem.vue';

// Sidebar (08 §9.1, ADR-017): brand, optional top/download card, grouped navigation, bottom links.
// Host supplies URLs/current item and RouterLink/NuxtLink through linkAs; component never reads routes.
export interface OnSidebarItem {
  key: string;
  label: string;
  /** lucide:* */
  icon?: string;
  href: string;
  active?: boolean;
  /** Right count, e.g. Installed 8. */
  count?: number;
  /** Right text in the count style when there is no count, e.g. a size. */
  meta?: string;
  /** Right badge, e.g. Updates 4, taking priority over count. */
  badge?: number;
  /** Right attention dot without a number; badgeLabel names it. */
  dot?: boolean;
  badgeLabel?: string;
}

export interface OnSidebarGroup {
  key: string;
  label: string;
  items: readonly OnSidebarItem[];
}

export interface OnSidebarProps {
  groups: readonly OnSidebarGroup[];
  footerItems?: readonly OnSidebarItem[];
  /** Accessible navigation name. */
  label: string;
  /** Eyebrow above top card. */
  topLabel?: string;
  /** Desktop reserves 52 px draggable space for traffic lights, as does toolbar (08 §9.1); web does not. */
  dragRegion?: boolean;
  linkAs?: string | Component;
}

const props = withDefaults(defineProps<OnSidebarProps>(), { footerItems: () => [], dragRegion: false, linkAs: 'a' });

defineSlots<{ brand?: () => unknown; top?: () => unknown; footer?: () => unknown }>();

// Same convention as OnAppCard: native a uses href, routers use to.
const linkAttrs = (href: string) => (props.linkAs === 'a' ? { href } : { to: href });
</script>

<template>
  <aside class="flex h-full min-h-0 flex-col border-r border-r-solid border-line-sidebar bg-surface-sidebar px-12px">
    <div v-if="dragRegion" class="h-52px shrink-0" data-tauri-drag-region></div>
    <div class="px-8px pb-18px" :class="dragRegion ? 'pt-4px' : 'pt-18px'">
      <slot name="brand"><OnLogo wordmark /></slot>
    </div>
    <section v-if="$slots.top" class="border-t border-t-solid border-line-sidebar pb-10px pt-14px">
      <div v-if="topLabel" class="px-8px pb-8px text-label text-ink-tertiary">{{ topLabel }}</div>
      <slot name="top" />
    </section>
    <nav class="flex min-h-0 flex-1 flex-col gap-12px overflow-y-auto pt-4px" :aria-label="label">
      <div v-for="group in groups" :key="group.key" class="flex flex-col gap-2px">
        <div class="px-8px pb-6px pt-2px text-label text-ink-tertiary">{{ group.label }}</div>
        <OnNavItem
          v-for="item in group.items"
          :key="item.key"
          :as="linkAs"
          v-bind="linkAttrs(item.href)"
          :icon="item.icon"
          :active="item.active"
          :count="item.count"
          :meta="item.meta"
          :badge="item.badge"
          :dot="item.dot"
          :badge-label="item.badgeLabel"
        >
          {{ item.label }}
        </OnNavItem>
      </div>
    </nav>
    <div v-if="footerItems.length || $slots.footer" class="border-t border-t-solid border-line-sidebar py-10px">
      <OnNavItem
        v-for="item in footerItems"
        :key="item.key"
        :as="linkAs"
        v-bind="linkAttrs(item.href)"
        :icon="item.icon"
        :active="item.active"
        :count="item.count"
        :meta="item.meta"
        :badge="item.badge"
        :dot="item.dot"
        :badge-label="item.badgeLabel"
      >
        {{ item.label }}
      </OnNavItem>
      <slot name="footer" />
    </div>
  </aside>
</template>
