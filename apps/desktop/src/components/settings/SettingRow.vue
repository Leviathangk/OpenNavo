<script setup lang="ts">
// Settings row: title/description left, control (toggle, select, button) right.
defineProps<{ label: string; hint?: string; labelId?: string }>();
defineSlots<{ default?: () => unknown }>();
</script>

<template>
  <div class="flex items-center gap-16px border-t border-t-solid border-line-subtle px-18px py-14px first:border-t-0">
    <div class="min-w-0 flex-1">
      <div :id="labelId" class="text-13.5px text-ink-primary">{{ label }}</div>
      <div v-if="hint" class="mt-2px text-pretty text-12px text-ink-tertiary">{{ hint }}</div>
    </div>
    <div class="flex shrink-0 items-center gap-8px">
      <slot />
    </div>
  </div>
</template>
