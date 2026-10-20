<script setup lang="ts">
import type { Component } from 'vue';
import OnCard from './OnCard.vue';
import OnIcon from './OnIcon.vue';
import OnPageHeader from './OnPageHeader.vue';
import OnSkeleton from './OnSkeleton.vue';

// Shared category overview (08 §10.2, ADR-017): top-level icon/name/count/optional-description cards and child categories.
export interface OnCategoriesItem {
  key: string;
  name: string;
  /** lucide:* */
  icon: string;
  /** Host-formatted count, e.g. 12 items. */
  count: string;
  description?: string | null;
  /** Subcategory name. */
  children: readonly string[];
  /**
   * Subcategories as links (T1: the tree links every child). When given, the card's name becomes its main
   * link stretched over the card, and each child is a link of its own above it; without, the whole card is
   * one link and children are plain text (desktop).
   */
  childLinks?: readonly { name: string; href: string }[];
  href: string;
}

export interface OnCategoriesViewProps {
  title: string;
  subtitle?: string;
  /** null means loading; show skeletons. */
  items: readonly OnCategoriesItem[] | null;
  linkAs?: string | Component;
}

const props = withDefaults(defineProps<OnCategoriesViewProps>(), { linkAs: 'a' });

defineSlots<{ default?: () => unknown }>();

const linkAttrs = (href: string) => (props.linkAs === 'a' ? { href } : { to: href });
</script>

<template>
  <div class="flex flex-col">
    <OnPageHeader :title="title" :subtitle="subtitle" />
    <!-- Default slot before grid for host states such as load failure. -->
    <slot />
    <div v-if="items === null" class="grid grid-cols-[repeat(auto-fill,minmax(240px,1fr))] gap-12px" aria-hidden="true">
      <OnSkeleton v-for="index in 8" :key="index" height="88px" radius="big" />
    </div>
    <ul v-else class="m-0 grid list-none grid-cols-[repeat(auto-fill,minmax(240px,1fr))] gap-12px p-0">
      <li v-for="item in items" :key="item.key" class="flex">
        <OnCard v-if="item.childLinks?.length" as="div" interactive padding="md" class="relative flex-1">
          <div class="flex items-center gap-12px">
            <span
              class="grid h-36px w-36px shrink-0 place-items-center rounded-default bg-surface-chip text-ink-secondary"
            >
              <OnIcon :name="item.icon" :size="18" />
            </span>
            <span class="min-w-0 flex-1">
              <component
                :is="linkAs"
                v-bind="linkAttrs(item.href)"
                class="on-category-link block truncate text-14px font-600 text-ink-primary no-underline outline-none focus-visible:underline"
                >{{ item.name }}</component
              >
              <span class="block text-12px text-ink-tertiary tabular-nums">{{ item.count }}</span>
            </span>
          </div>
          <p v-if="item.description" class="m-0 mt-10px line-clamp-2 text-12.5px leading-[1.5] text-ink-tertiary">
            {{ item.description }}
          </p>
          <ul class="relative z-1 m-0 mt-10px flex list-none flex-wrap gap-x-10px gap-y-4px p-0">
            <li v-for="child in item.childLinks" :key="child.href">
              <component
                :is="linkAs"
                v-bind="linkAttrs(child.href)"
                class="rounded-tiny text-12px text-ink-tertiary no-underline outline-none hover:text-ink-primary hover:underline focus-visible:shadow-focus-ring"
                >{{ child.name }}</component
              >
            </li>
          </ul>
        </OnCard>
        <OnCard v-else as="a" :href="item.href" :link-as="linkAs" interactive padding="md" class="flex-1">
          <div class="flex items-center gap-12px">
            <span
              class="grid h-36px w-36px shrink-0 place-items-center rounded-default bg-surface-chip text-ink-secondary"
            >
              <OnIcon :name="item.icon" :size="18" />
            </span>
            <span class="min-w-0 flex-1">
              <span class="block truncate text-14px font-600 text-ink-primary">{{ item.name }}</span>
              <span class="block text-12px text-ink-tertiary tabular-nums">{{ item.count }}</span>
            </span>
          </div>
          <p v-if="item.description" class="m-0 mt-10px line-clamp-2 text-12.5px leading-[1.5] text-ink-tertiary">
            {{ item.description }}
          </p>
          <p v-if="item.children.length" class="m-0 mt-10px truncate text-12px text-ink-tertiary">
            {{ item.children.join(' · ') }}
          </p>
        </OnCard>
      </li>
    </ul>
  </div>
</template>

<style>
/* The card's main link covers the whole card; child links sit above it (z-1). */
.on-category-link::after {
  content: '';
  position: absolute;
  inset: 0;
  border-radius: inherit;
}
</style>
