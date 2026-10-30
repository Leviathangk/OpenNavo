<script setup lang="ts">
import OnChip from './OnChip.vue';
import OnIcon from './OnIcon.vue';
import OnTooltip from './OnTooltip.vue';

export interface OnPackageSpecRow {
  key: string;
  label: string;
  value?: string | null;
  href?: string;
  mono?: boolean;
  stars?: { label: string; href?: string; checked?: string };
  badge?: { label: string; tone?: 'neutral' | 'info' | 'success' | 'warning' | 'danger' };
}

export interface OnPackageSpecCardProps {
  rows: readonly OnPackageSpecRow[];
  title?: string;
  checkedAt?: { iso: string; label: string } | null;
  checkedLabel?: string;
}

withDefaults(defineProps<OnPackageSpecCardProps>(), {
  title: undefined,
  checkedAt: null,
  checkedLabel: undefined
});

const isHttps = (href: string | undefined): href is string => Boolean(href && /^https:\/\//i.test(href));
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
          <a
            v-if="isHttps(row.href)"
            :href="row.href"
            target="_blank"
            rel="noopener noreferrer"
            class="inline-flex max-w-full items-center gap-4px text-ink-primary no-underline hover:underline"
          >
            <span class="min-w-0">{{ row.value }}</span>
            <OnIcon name="lucide:arrow-up-right" :size="13" />
          </a>
          <template v-else>{{ row.value }}</template>
          <span v-if="row.stars" class="inline-flex items-center gap-4px">
            <span aria-hidden="true"> · </span>
            <a
              v-if="isHttps(row.stars.href ?? row.href)"
              :href="row.stars.href ?? row.href"
              target="_blank"
              rel="noopener noreferrer"
              class="inline-flex max-w-full items-center gap-4px text-ink-primary no-underline hover:underline"
              >{{ row.stars.label }}<OnIcon name="lucide:arrow-up-right" :size="13"
            /></a>
            <span v-else>{{ row.stars.label }}</span>
            <OnTooltip v-if="row.stars.checked" v-slot="{ describedBy }" :content="row.stars.checked" toggle-on-click>
              <button
                type="button"
                :aria-label="checkedLabel"
                :aria-describedby="describedBy"
                class="inline-flex min-h-24px min-w-24px cursor-pointer items-center justify-center rounded-tiny border-none bg-transparent p-2px text-ink-tertiary outline-none hover:text-ink-primary focus-visible:shadow-focus-ring"
              >
                <OnIcon name="info" :size="13" />
              </button>
            </OnTooltip>
          </span>
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
