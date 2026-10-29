<script setup lang="ts">
import type { Component } from 'vue';
import OnChip from './OnChip.vue';

export interface OnPackageSpecRow {
  key: string;
  label: string;
  value?: string | null;
  href?: string;
  mono?: boolean;
  badge?: { label: string; tone?: 'neutral' | 'info' | 'success' | 'warning' | 'danger' };
}

export interface OnPackageSpecCardProps {
  rows: readonly OnPackageSpecRow[];
  title?: string;
  checkedAt?: { iso: string; label: string } | null;
  checkedLabel?: string;
  linkAs?: string | Component;
}

const props = withDefaults(defineProps<OnPackageSpecCardProps>(), {
  title: undefined,
  checkedAt: null,
  checkedLabel: undefined,
  linkAs: 'a'
});

const linkAttrs = (href: string) => (props.linkAs === 'a' ? { href } : { to: href });
</script>

<template>
  <section
    v-if="rows.length || checkedAt"
    class="rounded-big border border-solid border-line-subtle bg-surface-card px-16px py-14px"
    data-testid="package-spec-card"
    :aria-label="title"
  >
    <h2 v-if="title" class="m-0 text-13.5px font-600 text-ink-primary">{{ title }}</h2>
    <dl
      class="m-0 grid grid-cols-1 gap-x-16px gap-y-2px text-12.5px leading-[1.6] sm:grid-cols-[max-content_minmax(0,1fr)] sm:gap-y-6px"
      :class="title ? 'mt-10px' : ''"
    >
      <template v-for="row in rows" :key="row.key">
        <dt class="mt-6px text-ink-tertiary first:mt-0 sm:mt-0">{{ row.label }}</dt>
        <dd
          class="m-0 min-w-0 text-ink-primary [overflow-wrap:anywhere]"
          :class="row.mono ? 'font-mono text-12px' : ''"
        >
          <component
            :is="linkAs"
            v-if="row.href"
            v-bind="linkAttrs(row.href)"
            class="text-ink-primary underline-offset-2 hover:underline"
          >
            {{ row.value }}
          </component>
          <template v-else>{{ row.value }}</template>
          <OnChip v-if="row.badge" class="ml-6px align-middle" size="sm" :tone="row.badge.tone">
            {{ row.badge.label }}
          </OnChip>
        </dd>
      </template>
    </dl>
    <p v-if="checkedAt" class="m-0 mt-10px text-11.5px text-ink-tertiary">
      {{ checkedLabel }} <time :datetime="checkedAt.iso">{{ checkedAt.label }}</time>
    </p>
  </section>
</template>
