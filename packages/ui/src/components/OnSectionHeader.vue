<script setup lang="ts">
import { computed } from 'vue';
import type { Component } from 'vue';
import OnIcon from './OnIcon.vue';

// Shared section header (mockup 02, ADR-017): title, muted description, right View rankings link. The h2 holds
// only the title (C3): the description sits beside it in its own paragraph, so it never becomes heading text.
export interface OnSectionHeaderProps {
  title: string;
  hint?: string;
  moreLabel?: string;
  moreHref?: string;
  /** Link component (NuxtLink/RouterLink); moreHref passed as to, native a by default. */
  linkAs?: string | Component;
}

const props = withDefaults(defineProps<OnSectionHeaderProps>(), { linkAs: 'a' });

const linkAttrs = computed(() => (props.linkAs === 'a' ? { href: props.moreHref } : { to: props.moreHref }));
</script>

<template>
  <div class="mb-12px flex flex-wrap items-baseline justify-between gap-12px">
    <div class="flex min-w-0 flex-1 flex-wrap items-baseline gap-x-10px gap-y-4px">
      <h2 class="m-0 max-w-full break-words text-headline text-ink-primary">{{ title }}</h2>
      <p v-if="hint" class="m-0 max-w-full break-words text-13px font-400 text-ink-tertiary">{{ hint }}</p>
    </div>
    <component
      :is="linkAs"
      v-if="moreLabel && moreHref"
      v-bind="linkAttrs"
      class="inline-flex shrink-0 items-center gap-4px rounded-tiny text-13px text-ink-secondary no-underline outline-none hover:text-ink-primary focus-visible:shadow-focus-ring"
    >
      {{ moreLabel }}
      <OnIcon name="arrow-right" :size="14" />
    </component>
  </div>
</template>
