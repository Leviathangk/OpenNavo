<script setup lang="ts">
import { computed } from 'vue';
import type { Component } from 'vue';
import OnBadge from './OnBadge.vue';
import OnIcon from './OnIcon.vue';

export interface OnNavItemProps {
  /** lucide:*，17px */
  icon?: string;
  active?: boolean;
  /** Right count, e.g. Installed 218. */
  count?: number;
  /** Right text in the count style, e.g. Clean Up 34 GB; used when there is no count. */
  meta?: string;
  /** Right badge, e.g. Updates 6, taking priority over count. */
  badge?: number;
  /** Right attention dot without a number, e.g. Clean Up while dead background items still load; badgeLabel names it. */
  dot?: boolean;
  /** Accessible badge or dot text. */
  badgeLabel?: string;
  /** Small right-side tag, e.g. Mac for a destination that needs the desktop app. */
  tag?: string;
  /** Accessible text for the tag, e.g. "Mac app required". */
  tagLabel?: string;
  /** Render native a/button or supplied RouterLink; component never reads routes. */
  as?: string | Component;
  href?: string;
  to?: string;
}

const props = withDefaults(defineProps<OnNavItemProps>(), { as: 'a' });

defineSlots<{ default?: () => unknown }>();

const element = computed(() => (props.as === 'a' && props.href === undefined ? 'button' : props.as));

// Only pass defined link attributes; undefined href falls through and overrides RouterLink's own href.
const linkAttrs = computed(() => ({
  ...(props.href === undefined ? {} : { href: props.href }),
  ...(props.to === undefined ? {} : { to: props.to })
}));
</script>

<template>
  <component
    :is="element"
    v-bind="linkAttrs"
    :type="element === 'button' ? 'button' : undefined"
    :aria-current="active ? 'page' : undefined"
    :aria-description="tagLabel || undefined"
    class="m-0 box-border flex h-34px w-full items-center gap-10px rounded-small border-none px-10px font-sans text-13.5px font-500 no-underline outline-none transition-colors duration-fast ease-standard focus-visible:shadow-focus-ring"
    :class="
      active ? 'bg-component-nav-active text-ink-primary' : 'bg-transparent text-ink-tertiary hover:text-ink-secondary'
    "
  >
    <OnIcon v-if="icon" :name="icon" :size="17" />
    <span class="min-w-0 flex-1 truncate text-left"><slot /></span>
    <OnBadge v-if="badge !== undefined && badge > 0" :count="badge" :label="badgeLabel" />
    <span
      v-else-if="dot"
      class="mr-6px h-7px w-7px shrink-0 rounded-full bg-brand-coral"
      :role="badgeLabel ? 'img' : undefined"
      :aria-label="badgeLabel"
      :aria-hidden="badgeLabel ? undefined : 'true'"
    ></span>
    <span v-else-if="count !== undefined" class="text-11.5px text-ink-tertiary tabular-nums">{{ count }}</span>
    <span v-else-if="meta" class="shrink-0 text-11.5px text-ink-tertiary tabular-nums">{{ meta }}</span>
    <span
      v-if="tag"
      class="ml-2px shrink-0 rounded-full border border-solid border-line-subtle px-5px py-1px text-[10px] font-600 leading-[1.4] text-ink-tertiary"
      aria-hidden="true"
      :title="tagLabel"
      >{{ tag }}</span
    >
  </component>
</template>
