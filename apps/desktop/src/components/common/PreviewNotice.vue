<script setup lang="ts">
import { OnIcon } from '@opennavo/ui';

// Preview deep links (F1, F5): what a link from the web asked for and what OpenNavo found on this Mac. The
// notice never acts; the page's own buttons and confirmations below it do.
withDefaults(defineProps<{ title: string; body?: string; tone?: 'info' | 'warning' }>(), { tone: 'info' });
defineSlots<{ default?: () => unknown }>();
</script>

<template>
  <section
    role="status"
    data-testid="deeplink-preview"
    class="flex flex-col gap-8px rounded-big px-14px py-12px"
    :class="tone === 'warning' ? 'bg-status-warning-subtle' : 'bg-status-info-subtle'"
  >
    <p class="m-0 flex items-start gap-8px text-13px font-600 leading-[1.5] text-ink-primary">
      <OnIcon
        :name="tone === 'warning' ? 'circle-alert' : 'info'"
        :size="15"
        class="mt-2px shrink-0"
        :class="tone === 'warning' ? 'text-status-warning' : 'text-status-info'"
      />
      <span class="min-w-0 break-words">{{ title }}</span>
    </p>
    <p v-if="body" class="m-0 pl-23px text-12.5px leading-[1.6] text-ink-secondary">{{ body }}</p>
    <div v-if="$slots.default" class="pl-23px"><slot /></div>
  </section>
</template>
