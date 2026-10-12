<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { formatBytes } from '@opennavo/shared';
import { OnIcon, OnProgressMeter } from '@opennavo/ui';
import { useAppLocale } from '@/composables/useAppLocale';
import { freesNow, useCleanupStore } from '@/stores/cleanup';
import { CATEGORY_ICONS } from './meta';

// Running (cleanup proposal §06 step 5): one queued task; leaving the page does not stop it.
const { t } = useI18n();
const { appLocale } = useAppLocale();
const store = useCleanupStore();

const percent = computed(() => store.task?.percent ?? null);
const rows = computed(() =>
  store.categories.flatMap(category => {
    const items = store.chosen.filter(entry => entry.category.key === category.key).map(entry => entry.item);
    if (!items.length) return [];
    const command = items.find(item => item.kind === 'command')?.command;
    const how = command
      ? t('cleanup.run.command', { command })
      : items.every(freesNow)
        ? t('cleanup.run.delete')
        : t('cleanup.run.trash');
    const size = items.reduce((sum, item) => sum + (item.sizeBytes ?? 0), 0);
    return [{ key: category.key, how, size: formatBytes(size, { locale: appLocale.value }) }];
  })
);
</script>

<template>
  <section class="rounded-big border border-solid border-line-subtle bg-surface-card px-20px py-18px">
    <div class="flex items-center gap-10px" aria-live="polite">
      <OnIcon name="loader-circle" :size="16" class="cleanup-spin shrink-0 text-brand-coral" />
      <h2 class="m-0 flex-1 text-15px font-600 text-ink-primary">{{ t('cleanup.run.title') }}</h2>
      <span v-if="percent !== null" class="text-13px tabular-nums text-ink-secondary">{{ percent }}%</span>
    </div>
    <OnProgressMeter class="mt-12px" :value="percent" :label="t('cleanup.run.title')" />
  </section>

  <ul class="m-0 mt-12px list-none rounded-panel border border-solid border-line-subtle bg-surface-card px-16px py-2px">
    <li
      v-for="row in rows"
      :key="row.key"
      class="flex items-center gap-12px border-b border-b-solid border-line-subtle py-10px last:border-b-none"
    >
      <span class="grid h-26px w-26px shrink-0 place-items-center rounded-[7px] bg-surface-raised text-ink-secondary">
        <OnIcon :name="CATEGORY_ICONS[row.key]" :size="14" />
      </span>
      <span class="min-w-0 flex-1">
        <span class="block text-13px font-500 text-ink-primary">{{ t(`cleanup.categories.${row.key}`) }}</span>
        <span class="block truncate text-12px text-ink-tertiary">{{ row.how }}</span>
      </span>
      <span class="shrink-0 text-12.5px tabular-nums text-ink-secondary">{{ row.size }}</span>
    </li>
  </ul>
  <p class="m-0 mx-2px mt-12px text-12px leading-[1.55] text-ink-tertiary">
    {{ t('cleanup.run.hint') }}
  </p>
</template>

<style scoped>
.cleanup-spin {
  animation: cleanup-run-spin 1s linear infinite;
}
@keyframes cleanup-run-spin {
  to {
    transform: rotate(360deg);
  }
}
@media (prefers-reduced-motion: reduce) {
  .cleanup-spin {
    animation: none;
  }
}
</style>
