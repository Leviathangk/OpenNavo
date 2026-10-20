<script setup lang="ts">
import { computed } from 'vue';
import type { Component } from 'vue';
import { interpolatePlural } from '@opennavo/shared';
import type { PackageKind } from '@opennavo/shared';
import OnAppIcon from './OnAppIcon.vue';
import { useUiLocale, useUiMessages } from '../composables/locale';

export interface OnCollectionIcon {
  /** Package metadata enables letter/terminal fallback without icons; URL-only missing images use placeholders. */
  kind?: PackageKind;
  token?: string;
  name?: string;
  src?: string | null;
  accent?: string | null;
}

export interface OnCollectionCardProps {
  title: string;
  subtitle?: string | null;
  /** Item count for View all 12 apps button copy. */
  count: number;
  /** First five item icons. */
  icons?: readonly OnCollectionIcon[];
  href: string;
  /** Link component (NuxtLink/RouterLink), native a by default. */
  linkAs?: string | Component;
  /** h2 where the cards are the page's list (/collections), h3 inside a section (Discover, rails). */
  headingLevel?: 2 | 3;
}

const props = withDefaults(defineProps<OnCollectionCardProps>(), { icons: () => [], linkAs: 'a', headingLevel: 3 });

const messages = useUiMessages();
const locale = useUiLocale();

const stack = computed(() => props.icons.slice(0, 5));
const heading = computed(() => `h${props.headingLevel}`);
const linkAttrs = computed(() => (props.linkAs === 'a' ? { href: props.href } : { to: props.href }));
</script>

<template>
  <!-- The title is the card's one link (T1), stretched over the card; "View all" only looks like a button. -->
  <article
    class="on-collection group relative box-border flex min-w-0 flex-col rounded-big border border-solid border-line-subtle p-14px font-sans transition-colors duration-fast ease-standard hover:border-line-strong"
  >
    <div v-if="stack.length" class="mb-12px flex" aria-hidden="true">
      <span v-for="(icon, index) in stack" :key="index" class="on-collection-icon -mr-8px inline-flex rounded-app-icon">
        <OnAppIcon
          v-if="icon.kind && icon.token && icon.name"
          :kind="icon.kind"
          :token="icon.token"
          :name="icon.name"
          :src="icon.src"
          :accent="icon.accent"
          :size="30"
        />
        <img
          v-else-if="icon.src"
          :src="icon.src"
          alt=""
          width="30"
          height="30"
          loading="lazy"
          class="block h-30px w-30px rounded-app-icon object-cover"
        />
        <span v-else class="block h-30px w-30px rounded-app-icon bg-surface-raised"></span>
      </span>
    </div>
    <component :is="heading" class="m-0 text-14px font-600 text-ink-primary">
      <component
        :is="linkAs"
        v-bind="linkAttrs"
        class="on-collection-link text-inherit no-underline outline-none focus-visible:underline"
        >{{ title }}</component
      >
    </component>
    <p v-if="subtitle" class="m-0 mt-4px text-12px leading-[1.5] text-ink-tertiary">{{ subtitle }}</p>
    <!-- Equal-height grid cards pin the label to the bottom with at least 12 px spacing from the body. -->
    <div class="mt-auto pt-12px" aria-hidden="true">
      <span
        class="on-collection-more box-border flex min-h-34px w-full items-center justify-center rounded-default bg-button-secondary-bg px-16px py-6px text-center text-13px font-600 text-button-secondary-text transition-colors duration-fast ease-standard group-hover:bg-button-secondary-hover"
        >{{ interpolatePlural(messages.collection.viewAll, { count }, count, locale) }}</span
      >
    </div>
  </article>
</template>

<style>
.on-collection {
  background: linear-gradient(160deg, var(--on-component-collection-from), var(--on-component-collection-to) 70%);
}

.on-collection-icon {
  box-shadow: 0 0 0 2px var(--on-component-collection-ring);
}

/* The title link covers the whole card, so the card opens from anywhere and keeps one link for crawlers. */
.on-collection-link::after {
  content: '';
  position: absolute;
  inset: 0;
  border-radius: inherit;
}

.on-collection:has(.on-collection-link:focus-visible) {
  box-shadow: var(--on-shadow-focus-ring);
}
</style>
