<script setup lang="ts">
import { computed } from 'vue';
import type { Component } from 'vue';
import OnAppIcon from './OnAppIcon.vue';
import type { OnAppIconKind } from './OnAppIcon.vue';

export interface OnAppRowProps {
  kind: OnAppIconKind;
  token: string;
  name: string;
  src?: string | null;
  accent?: string | null;
  /** Secondary information beneath name, e.g. 1.139.1 → 1.140.0. */
  meta?: string;
  description?: string | null;
  /** Name link URL, stretched across the row. */
  href?: string;
  /** Link component (NuxtLink/RouterLink), href passed as to; native a by default. */
  linkAs?: string | Component;
}

const props = withDefaults(defineProps<OnAppRowProps>(), { linkAs: 'a' });

const linkAttrs = computed(() => (props.linkAs === 'a' ? { href: props.href } : { to: props.href }));

const emit = defineEmits<{
  /** Name-link click, used for web search-click reporting. */
  navigate: [];
}>();

defineSlots<{
  /** Right actions, e.g. Get and More. */
  actions?: () => unknown;
  /** Badge after name. */
  badges?: () => unknown;
}>();
</script>

<template>
  <div
    class="relative box-border flex min-h-64px min-w-0 items-center gap-12px border-b border-b-solid border-line-subtle py-12px last:border-b-0"
  >
    <OnAppIcon :kind="kind" :token="token" :name="name" :src="src" :accent="accent" :size="40" />
    <div class="flex min-w-0 flex-1 flex-col gap-2px">
      <!-- Badges wrap to their own line rather than squeezing the name away (several long ones in es/pt/ru). -->
      <div class="flex min-w-0 flex-wrap items-center gap-x-6px gap-y-2px">
        <component
          :is="linkAs"
          v-if="href"
          v-bind="linkAttrs"
          class="on-stretched max-w-full shrink-0 truncate text-14px font-600 text-ink-primary no-underline outline-none focus-visible:underline"
          @click="emit('navigate')"
        >
          {{ name }}
        </component>
        <span v-else class="max-w-full shrink-0 truncate text-14px font-600 text-ink-primary">{{ name }}</span>
        <slot name="badges" />
      </div>
      <span v-if="meta" class="truncate text-12px text-ink-secondary tabular-nums">{{ meta }}</span>
      <span v-if="description" class="truncate text-12px text-ink-tertiary">{{ description }}</span>
    </div>
    <div v-if="$slots.actions" class="relative z-1 flex shrink-0 items-center gap-8px">
      <slot name="actions" />
    </div>
  </div>
</template>
