<script setup lang="ts" generic="S extends string">
import { onMounted, onBeforeUnmount, ref } from 'vue';
import type { Component } from 'vue';
import type { PackageSummary } from '@opennavo/api';
import OnBreadcrumb from './OnBreadcrumb.vue';
import type { OnBreadcrumbItem } from './OnBreadcrumb.vue';
import OnPageHeader from './OnPageHeader.vue';
import OnSegmented from './OnSegmented.vue';
import OnSkeleton from './OnSkeleton.vue';
import type { OnSegmentedOption } from '../types';

// App list (08 §10.2, ADR-017): optional breadcrumbs, title/description/sort/filter, card grid, pagination footer.
// Shared category/web app lists; host card slot supplies desktop local state or web Get menus.
export interface OnPackageListViewProps<V extends string> {
  breadcrumb?: readonly OnBreadcrumbItem[];
  title: string;
  subtitle?: string;
  sorts?: readonly OnSegmentedOption<V>[];
  sort?: V;
  sortLabel?: string;
  /** null means loading; show skeletons. */
  items: readonly PackageSummary[] | null;
  linkAs?: string | Component;
  /**
   * Fit whole rows to the width and emit pageSize (desktop). The web passes false: it keeps one page
   * size so a list has a single set of URLs, and the CSS grid alone decides the columns.
   */
  fitToWidth?: boolean;
}

const props = withDefaults(defineProps<OnPackageListViewProps<S>>(), {
  breadcrumb: () => [],
  sorts: () => [],
  linkAs: 'a',
  fitToWidth: true
});

const emit = defineEmits<{ 'update:sort': [value: S]; pageSize: [value: number] }>();

// Keep complete rows near the original 24-card page size. Hosts use this size for both requests and pagination.
const root = ref<HTMLElement>();
const pageSize = ref(24);
let observer: ResizeObserver | undefined;
let resizeTimer: ReturnType<typeof setTimeout> | undefined;
function fitPage(width: number) {
  if (width <= 0) return;
  const columns = Math.max(1, Math.floor((width + 12) / 272));
  const rows = Math.max(1, Math.min(Math.ceil(24 / columns), Math.floor(100 / columns)));
  pageSize.value = Math.min(100, columns * rows);
  emit('pageSize', pageSize.value);
}
onMounted(() => {
  if (!root.value || !props.fitToWidth) return;
  fitPage(root.value.clientWidth);
  if (typeof ResizeObserver === 'undefined') return;
  let previous = root.value.clientWidth;
  observer = new ResizeObserver(([entry]) => {
    if (!entry || entry.contentRect.width === previous) return;
    previous = entry.contentRect.width;
    clearTimeout(resizeTimer);
    resizeTimer = setTimeout(() => fitPage(previous), 120);
  });
  observer.observe(root.value);
});
onBeforeUnmount(() => {
  observer?.disconnect();
  clearTimeout(resizeTimer);
});

defineSlots<{
  /** Filter left of sorting, e.g. web Show disabled. */
  filters?: () => unknown;
  card: (props: { pkg: PackageSummary }) => unknown;
  /** Loaded without data. */
  empty?: () => unknown;
  /** After heading/before grid for host states such as load failure. */
  default?: () => unknown;
  /** After grid, e.g. pagination. */
  footer?: () => unknown;
}>();
</script>

<template>
  <div ref="root" class="flex flex-col">
    <OnBreadcrumb v-if="breadcrumb.length" class="pt-6px" :items="breadcrumb" :link-as="linkAs" />
    <OnPageHeader :title="title" :subtitle="subtitle">
      <template v-if="$slots.filters || (sorts.length && sort)" #actions>
        <slot name="filters" />
        <OnSegmented
          v-if="sorts.length && sort"
          :model-value="sort"
          size="sm"
          :aria-label="sortLabel"
          :options="sorts"
          @update:model-value="emit('update:sort', $event)"
        />
      </template>
    </OnPageHeader>
    <slot />
    <div v-if="items === null" class="grid grid-cols-[repeat(auto-fill,minmax(260px,1fr))] gap-12px" aria-hidden="true">
      <OnSkeleton v-for="index in pageSize" :key="index" height="148px" radius="big" />
    </div>
    <div v-else-if="items.length" class="grid grid-cols-[repeat(auto-fill,minmax(260px,1fr))] gap-12px">
      <template v-for="pkg in items" :key="`${pkg.kind}/${pkg.token}`">
        <slot name="card" :pkg="pkg" />
      </template>
    </div>
    <slot v-else name="empty" />
    <slot name="footer" />
  </div>
</template>
