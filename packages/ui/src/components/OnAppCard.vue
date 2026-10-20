<script setup lang="ts">
import { useUiLocale } from '../composables/locale';
import { computed } from 'vue';
import type { Component } from 'vue';
import type { PackageSummary } from '@opennavo/api';
import { formatCount, formatVersion } from '@opennavo/shared';
import OnAppIcon from './OnAppIcon.vue';
import OnGetButton from './OnGetButton.vue';
import type { GetState } from './OnGetButton.vue';
import { useUiMessages } from '../composables/locale';

const formattingLocale = useUiLocale();

export type OnAppCardStat = 'installs30d' | 'version' | 'category';

export interface OnAppCardProps {
  pkg: PackageSummary;
  /** Host-derived state, always get on web. */
  state: GetState;
  progress?: number;
  /** Two stats, defaulting to 30-day installs and version. */
  stats?: readonly OnAppCardStat[];
  /** Detail URL; links the name and stretches across the card, otherwise card click emits open. */
  href?: string;
  /** Get-button text, e.g. web Get. */
  actionLabel?: string;
  /** Get tooltip for explanations too long for cards, e.g. Install Homebrew first. */
  actionTitle?: string;
  /** Link component (NuxtLink/RouterLink), href passed as to; native a by default. */
  linkAs?: string | Component;
  /** Router history state for the navigation (router links only; href must then be a plain path). */
  linkState?: Record<string, unknown>;
}

const props = withDefaults(defineProps<OnAppCardProps>(), { stats: () => ['installs30d', 'version'], linkAs: 'a' });

const emit = defineEmits<{
  /** Open details when href is absent. */
  open: [];
  /** Get/open/update click. */
  action: [];
  /** Cancel an active task. */
  cancel: [];
}>();

defineSlots<{
  /** Replace lower-right Get button, e.g. web Get menu. */
  action?: () => unknown;
}>();

const messages = useUiMessages();

const linkAttrs = computed(() => {
  if (props.linkAs === 'a') return { href: props.href };
  return { to: props.linkState && props.href ? { path: props.href, state: props.linkState } : props.href };
});

const statItems = computed(() =>
  props.stats.slice(0, 2).map(stat => {
    if (stat === 'installs30d')
      return {
        key: stat,
        label: messages.value.card.installs30d,
        value: formatCount(props.pkg.installs30d, { locale: formattingLocale.value })
      };
    if (stat === 'version')
      return { key: stat, label: messages.value.card.version, value: formatVersion(props.pkg.version) };
    return { key: stat, label: messages.value.card.category, value: props.pkg.primaryCategory?.name ?? '—' };
  })
);
</script>

<template>
  <article
    class="group relative box-border flex min-w-0 flex-col gap-12px rounded-big border border-solid border-line-subtle bg-surface-card p-14px transition-colors duration-fast ease-standard hover:border-line-strong hover:bg-component-card-hover"
  >
    <div class="flex min-w-0 items-start gap-12px">
      <OnAppIcon
        :kind="pkg.kind"
        :token="pkg.token"
        :name="pkg.displayName"
        :src="pkg.iconUrl"
        :variants="pkg.iconVariants"
        :accent="pkg.accentColor"
        :size="44"
      />
      <div class="flex min-w-0 flex-1 flex-col gap-2px">
        <!-- Stretched link makes the whole card clickable; Get button sits above it. -->
        <component
          :is="linkAs"
          v-if="href"
          v-bind="linkAttrs"
          class="on-stretched truncate text-14px font-600 text-ink-primary no-underline outline-none focus-visible:underline"
        >
          {{ pkg.displayName }}
        </component>
        <button
          v-else
          type="button"
          class="on-stretched m-0 truncate border-none bg-transparent p-0 text-left font-sans text-14px font-600 text-ink-primary outline-none focus-visible:underline"
          @click="emit('open')"
        >
          {{ pkg.displayName }}
        </button>
        <p v-if="pkg.summary" class="on-clamp-2 m-0 text-12px leading-18px text-ink-tertiary">{{ pkg.summary }}</p>
      </div>
    </div>
    <div
      class="mt-auto grid grid-cols-[repeat(3,minmax(0,1fr))] items-end gap-12px border-t border-t-solid border-line-subtle pt-12px"
    >
      <div v-for="item in statItems" :key="item.key" class="flex min-w-0 flex-col gap-2px">
        <span class="break-words text-10.5px text-ink-tertiary">{{ item.label }}</span>
        <span class="truncate text-12.5px font-600 text-ink-primary tabular-nums">{{ item.value }}</span>
      </div>
      <div class="relative z-1 col-start-3 flex justify-end">
        <slot name="action">
          <OnGetButton
            :state="state"
            :progress="progress"
            :label="actionLabel"
            :title="actionTitle"
            @click="emit('action')"
            @cancel="emit('cancel')"
          />
        </slot>
      </div>
    </div>
  </article>
</template>

<style>
.on-stretched::after {
  content: '';
  position: absolute;
  inset: 0;
  border-radius: inherit;
}

.on-clamp-2 {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  overflow: hidden;
}
</style>
