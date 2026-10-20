<script setup lang="ts">
import type { Component } from 'vue';
import OnCollectionCard from './OnCollectionCard.vue';
import type { OnCollectionIcon } from './OnCollectionCard.vue';
import OnPageHeader from './OnPageHeader.vue';
import OnSkeleton from './OnSkeleton.vue';

// Shared collections (ADR-017): title, card grid, footer with web pagination. Card titles are h2 under the h1.
export interface OnCollectionsItem {
  key: string;
  title: string;
  subtitle?: string | null;
  count: number;
  icons: readonly OnCollectionIcon[];
  href: string;
}

export interface OnCollectionsViewProps {
  title: string;
  subtitle?: string;
  /** null means loading; show skeletons. */
  items: readonly OnCollectionsItem[] | null;
  linkAs?: string | Component;
}

withDefaults(defineProps<OnCollectionsViewProps>(), { linkAs: 'a' });

defineSlots<{
  /** After heading, before grid: offline/load errors. */
  default?: () => unknown;
  /** Loaded but no collections. */
  empty?: () => unknown;
  footer?: () => unknown;
}>();
</script>

<template>
  <div class="flex flex-col">
    <OnPageHeader :title="title" :subtitle="subtitle" />
    <slot />
    <div v-if="items === null" class="grid grid-cols-[repeat(auto-fill,minmax(260px,1fr))] gap-12px" aria-hidden="true">
      <OnSkeleton v-for="index in 6" :key="index" height="168px" radius="big" />
    </div>
    <div v-else-if="items.length" class="grid grid-cols-[repeat(auto-fill,minmax(260px,1fr))] gap-12px">
      <OnCollectionCard
        v-for="item in items"
        :key="item.key"
        :title="item.title"
        :subtitle="item.subtitle"
        :count="item.count"
        :icons="item.icons"
        :href="item.href"
        :link-as="linkAs"
        :heading-level="2"
      />
    </div>
    <slot v-else name="empty" />
    <slot name="footer" />
  </div>
</template>
