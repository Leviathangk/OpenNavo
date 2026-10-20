<script setup lang="ts">
import { computed } from 'vue';
import type { Component } from 'vue';
import { interpolate } from '@opennavo/shared';
import OnIcon from './OnIcon.vue';
import { useUiMessages } from '../composables/locale';
import { paginationRange } from '../utils/pagination';

export interface OnPaginationProps {
  /** Current page, one-based. */
  page: number;
  pageCount: number;
  /** Maximum slots including ellipses. */
  max?: number;
  /** Web: each page has a ?page= URL; render links when supplied. */
  hrefFor?: (page: number) => string;
  linkAs?: string | Component;
  /**
   * Extra pages this many steps before and after the current one (T1), so deep pages of a long list are a
   * few clicks away. Defaults to 10 and 100 for link pagination (web) and none for buttons (desktop).
   */
  jumps?: readonly number[];
}

const props = withDefaults(defineProps<OnPaginationProps>(), { max: 7, linkAs: 'a' });

const emit = defineEmits<{ 'update:page': [page: number] }>();

const LINK_JUMPS: readonly number[] = [10, 100];

const messages = useUiMessages();
const items = computed(() =>
  paginationRange(props.page, props.pageCount, props.max, props.jumps ?? (props.hrefFor ? LINK_JUMPS : []))
);

const baseClass =
  'm-0 box-border inline-flex h-32px min-w-32px items-center justify-center gap-4px rounded-default border-none px-10px font-sans text-13px no-underline outline-none transition-colors duration-fast ease-standard focus-visible:shadow-focus-ring';

function attrsFor(target: number) {
  if (!props.hrefFor) return { type: 'button' };
  const href = props.hrefFor(target);
  return props.linkAs === 'a' ? { href } : { to: href };
}

function go(target: number, event: MouseEvent) {
  if (target < 1 || target > props.pageCount || target === props.page) {
    event.preventDefault();
    return;
  }
  emit('update:page', target);
}

const tag = computed(() => (props.hrefFor ? props.linkAs : 'button'));
</script>

<template>
  <nav
    v-if="pageCount > 1"
    :aria-label="messages.nav.pagination"
    class="flex max-w-full flex-wrap items-center justify-center gap-4px"
  >
    <component
      :is="page > 1 ? tag : 'span'"
      v-bind="page > 1 ? attrsFor(page - 1) : {}"
      :class="[
        baseClass,
        page > 1
          ? 'bg-transparent text-ink-secondary hover:bg-surface-raised hover:text-ink-primary'
          : 'text-ink-disabled'
      ]"
      :aria-disabled="page > 1 ? undefined : 'true'"
      @click="go(page - 1, $event)"
    >
      <OnIcon name="lucide:chevron-left" :size="14" />
      {{ messages.nav.previous }}
    </component>
    <template v-for="item in items" :key="item.type === 'page' ? item.page : item.key">
      <span
        v-if="item.type === 'ellipsis'"
        class="inline-flex h-32px min-w-24px items-center justify-center text-13px text-ink-tertiary"
        aria-hidden="true"
        >…</span
      >
      <component
        :is="tag"
        v-else
        v-bind="attrsFor(item.page)"
        :aria-current="item.page === page ? 'page' : undefined"
        :aria-label="interpolate(messages.nav.page, { page: item.page })"
        class="tabular-nums"
        :class="[
          baseClass,
          item.page === page
            ? 'bg-surface-raised font-600 text-ink-primary'
            : 'bg-transparent text-ink-secondary hover:bg-surface-raised hover:text-ink-primary'
        ]"
        @click="go(item.page, $event)"
      >
        {{ item.page }}
      </component>
    </template>
    <component
      :is="page < pageCount ? tag : 'span'"
      v-bind="page < pageCount ? attrsFor(page + 1) : {}"
      :class="[
        baseClass,
        page < pageCount
          ? 'bg-transparent text-ink-secondary hover:bg-surface-raised hover:text-ink-primary'
          : 'text-ink-disabled'
      ]"
      :aria-disabled="page < pageCount ? undefined : 'true'"
      @click="go(page + 1, $event)"
    >
      {{ messages.nav.next }}
      <OnIcon name="lucide:chevron-right" :size="14" />
    </component>
  </nav>
</template>
