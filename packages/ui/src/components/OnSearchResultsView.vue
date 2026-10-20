<script setup lang="ts">
import type { Component } from 'vue';
import type { PackageKind } from '@opennavo/shared';
import OnAppRow from './OnAppRow.vue';
import type { OnImageVariant } from '../utils/image';
import OnChip from './OnChip.vue';
import OnPageHeader from './OnPageHeader.vue';
import OnSkeleton from './OnSkeleton.vue';

// Search (08 §10.4, ADR-017): title/count, related searches, host search box, rows with alias hints,
// then web pagination. Host empty slot handles no results/no query.
export interface OnSearchResult {
  key: string;
  kind: PackageKind;
  token: string;
  name: string;
  src?: string | null;
  /** Stored WebP renditions of the icon (X3). */
  variants?: readonly OnImageVariant[] | null;
  accent?: string | null;
  /** Muted suffix after name: desktop token, web version. */
  meta?: string;
  description?: string | null;
  /** Full alias/old-name match label; host supplies null when equal to display name. */
  matched?: string | null;
  href: string;
}

export interface OnSearchResultsViewProps {
  title: string;
  subtitle?: string;
  /** Synonym expansion hint, e.g. Also searched: visual studio code. */
  hint?: string;
  /** null means no result yet, loading or no query; skeletons while loading. */
  results: readonly OnSearchResult[] | null;
  loading?: boolean;
  linkAs?: string | Component;
}

withDefaults(defineProps<OnSearchResultsViewProps>(), { loading: false, linkAs: 'a' });

const emit = defineEmits<{ navigate: [result: OnSearchResult, index: number] }>();

defineSlots<{
  /** Search box beneath title. */
  search?: () => unknown;
  action?: (props: { result: OnSearchResult }) => unknown;
  /** No results or no query. */
  empty?: () => unknown;
  /** Before results, e.g. load failure. */
  default?: () => unknown;
  footer?: () => unknown;
}>();
</script>

<template>
  <div class="flex flex-col">
    <OnPageHeader :title="title" :subtitle="subtitle">
      <p v-if="hint" class="m-0 mt-6px text-13px text-ink-tertiary">{{ hint }}</p>
    </OnPageHeader>
    <div v-if="$slots.search" class="mb-16px max-w-480px">
      <slot name="search" />
    </div>
    <slot />
    <div v-if="results === null && loading" class="flex flex-col gap-8px" aria-hidden="true">
      <OnSkeleton v-for="index in 6" :key="index" height="64px" radius="default" />
    </div>
    <ul v-else-if="results?.length" class="m-0 list-none rounded-big border border-solid border-line-subtle p-0">
      <li
        v-for="(result, index) in results"
        :key="result.key"
        class="border-t border-t-solid border-line-subtle px-14px first:border-t-0"
      >
        <OnAppRow
          :kind="result.kind"
          :token="result.token"
          :name="result.name"
          :src="result.src"
          :variants="result.variants"
          :accent="result.accent"
          :meta="result.meta"
          :description="result.description"
          :href="result.href"
          :link-as="linkAs"
          @navigate="emit('navigate', result, index)"
        >
          <template v-if="result.matched" #badges>
            <OnChip tone="outline">{{ result.matched }}</OnChip>
          </template>
          <template v-if="$slots.action" #actions>
            <slot name="action" :result="result" />
          </template>
        </OnAppRow>
      </li>
    </ul>
    <slot v-else name="empty" />
    <slot name="footer" />
  </div>
</template>
