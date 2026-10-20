<script setup lang="ts">
import { computed } from 'vue';
import type { Component } from 'vue';
import type { PackageSummary } from '@opennavo/api';
import { interpolatePlural } from '@opennavo/shared';
import OnAppIcon from './OnAppIcon.vue';
import OnIcon from './OnIcon.vue';
import { useUiLocale, useUiMessages } from '../composables/locale';

export interface OnRankRowProps {
  rank: number;
  pkg: PackageSummary;
  /** Right formatted compact value, e.g. 91K. */
  value: string;
  href?: string;
  /** Link component (NuxtLink/RouterLink), href passed as to; native a by default. */
  linkAs?: string | Component;
  /** Rank change, positive means up; providing this prop shows the column, null/zero show a dash (08 §10.3). */
  change?: number | null;
}

const props = withDefaults(defineProps<OnRankRowProps>(), { linkAs: 'a' });

const linkAttrs = computed(() => (props.linkAs === 'a' ? { href: props.href } : { to: props.href }));

defineSlots<{
  /** After right numeric value, e.g. rankings Get button. */
  action?: () => unknown;
}>();

const messages = useUiMessages();
const locale = useUiLocale();

const trend = computed(() => {
  const change = props.change ?? 0;
  if (change > 0)
    return {
      icon: 'arrow-up',
      text: String(change),
      tone: 'text-status-success',
      label: interpolatePlural(messages.value.rank.up, { n: change }, change, locale.value)
    };
  if (change < 0)
    return {
      icon: 'arrow-down',
      text: String(-change),
      tone: 'text-status-danger',
      label: interpolatePlural(messages.value.rank.down, { n: -change }, -change, locale.value)
    };
  return { icon: null, text: '—', tone: 'text-ink-tertiary', label: messages.value.rank.same };
});
</script>

<template>
  <div
    class="relative box-border flex min-w-0 items-center gap-12px rounded-big border border-solid border-line-subtle bg-surface-card px-14px py-10px transition-colors duration-fast ease-standard hover:border-line-strong hover:bg-component-card-hover"
  >
    <span class="w-14px shrink-0 text-center text-12px font-600 text-ink-tertiary tabular-nums">{{ rank }}</span>
    <span
      v-if="change !== undefined"
      class="inline-flex w-36px shrink-0 items-center gap-2px text-11.5px font-600 tabular-nums"
      :class="trend.tone"
    >
      <OnIcon v-if="trend.icon" :name="trend.icon" :size="12" />
      <span aria-hidden="true">{{ trend.text }}</span>
      <span class="sr-only">{{ trend.label }}</span>
    </span>
    <OnAppIcon
      :kind="pkg.kind"
      :token="pkg.token"
      :name="pkg.displayName"
      :src="pkg.iconUrl"
      :variants="pkg.iconVariants"
      :accent="pkg.accentColor"
      :size="30"
    />
    <div class="flex min-w-0 flex-1 flex-col">
      <component
        :is="linkAs"
        v-if="href"
        v-bind="linkAttrs"
        class="on-stretched truncate text-13.5px font-600 text-ink-primary no-underline outline-none focus-visible:underline"
      >
        {{ pkg.displayName }}
      </component>
      <span v-else class="truncate text-13.5px font-600 text-ink-primary">{{ pkg.displayName }}</span>
      <span v-if="pkg.summary" class="truncate text-12px text-ink-tertiary">{{ pkg.summary }}</span>
    </div>
    <span class="shrink-0 text-12px font-600 text-ink-secondary tabular-nums">{{ value }}</span>
    <div v-if="$slots.action" class="relative z-1 shrink-0">
      <slot name="action" />
    </div>
  </div>
</template>
