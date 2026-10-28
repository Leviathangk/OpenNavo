<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { OnIcon } from '@opennavo/ui';

// Preview the installed-page features while the shared permission gate is locked.
const { t } = useI18n();

const points = computed(() => [
  { key: 'footprint', icon: 'hard-drive', text: t('fullDiskGate.points.footprint') },
  { key: 'leftovers', icon: 'scan-search', text: t('fullDiskGate.points.leftovers') },
  { key: 'undo', icon: 'undo-2', text: t('fullDiskGate.points.undo') }
]);
</script>

<template>
  <section class="flex w-full max-w-560px flex-col gap-16px pt-4px" data-testid="full-disk-gate">
    <ul class="m-0 flex list-none flex-col gap-8px p-0">
      <li
        v-for="point in points"
        :key="point.key"
        class="flex items-start gap-9px text-13px leading-[1.5] text-ink-secondary"
      >
        <OnIcon :name="point.icon" :size="14" class="mt-3px shrink-0 text-ink-tertiary" />
        <span class="min-w-0">{{ point.text }}</span>
      </li>
    </ul>
    <p class="m-0 flex items-center gap-6px text-12px text-ink-disabled">
      <OnIcon name="lock" :size="12" class="shrink-0" />{{ t('fullDiskGate.privacy') }}
    </p>
  </section>
</template>
