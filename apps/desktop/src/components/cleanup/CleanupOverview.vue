<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { formatBytes, formatRelativeTime } from '@opennavo/shared';
import { OnButton, OnChip, OnIcon } from '@opennavo/ui';
import type { CleanupCategory, CleanupRisk } from '@/ipc/bindings';
import { useAppLocale } from '@/composables/useAppLocale';
import { useCleanupStore } from '@/stores/cleanup';
import { CATEGORY_ICONS, RISK_TONES } from './meta';

// Before a scan (cleanup proposal §03 ①): what gets checked, by risk tier; the last scan stays visible.
const emit = defineEmits<{ scan: [] }>();
const { t } = useI18n();
const { appLocale } = useAppLocale();
const store = useCleanupStore();

/** P0 categories (§05); later phases add theirs when the backend reports them. */
const TILES: { key: CleanupCategory; risk: CleanupRisk }[] = [
  { key: 'app_caches', risk: 'rebuildable' },
  { key: 'logs', risk: 'rebuildable' },
  { key: 'browser_caches', risk: 'rebuildable' },
  { key: 'dev_caches', risk: 'rebuildable' },
  { key: 'homebrew', risk: 'rebuildable' },
  { key: 'leftovers', risk: 'review' }
];

const format = (bytes: number) => formatBytes(bytes, { locale: appLocale.value });
const last = computed(() => {
  const summary = store.summary;
  if (summary?.scannedAt == null) return null;
  return {
    time: formatRelativeTime(summary.scannedAt, { locale: appLocale.value }),
    size: format(summary.reclaimableBytes)
  };
});
</script>

<template>
  <section
    class="flex items-center gap-20px rounded-big border border-solid border-line-subtle bg-surface-card px-24px py-22px"
  >
    <span
      class="grid h-60px w-60px shrink-0 place-items-center rounded-[17px] bg-accent-coral-subtle text-brand-coral"
      aria-hidden="true"
    >
      <OnIcon name="eraser" :size="28" />
    </span>
    <div class="min-w-0 flex-1">
      <h2 class="m-0 text-18px font-600 tracking-[-0.01em] text-ink-primary">
        {{ last ? t('cleanup.overview.titleAgain') : t('cleanup.overview.title') }}
      </h2>
      <p class="m-0 mt-5px text-13px leading-[1.5] text-ink-tertiary">
        {{ t('cleanup.overview.body') }}
      </p>
    </div>
    <div class="flex shrink-0 flex-col items-end gap-7px">
      <OnButton variant="primary" icon="scan-search" @click="emit('scan')">{{ t('cleanup.overview.scan') }}</OnButton>
      <span class="text-11.5px text-ink-disabled">{{
        last ? t('cleanup.overview.lastScan', { time: last.time }) : t('cleanup.overview.never')
      }}</span>
    </div>
  </section>
  <p v-if="last" class="m-0 mx-2px mt-12px flex items-center gap-8px text-12.5px text-ink-tertiary">
    <OnIcon name="check" :size="14" class="text-status-success" />
    {{ t('cleanup.overview.lastResult', last) }}
  </p>

  <ul class="m-0 mt-16px grid list-none grid-cols-2 gap-10px p-0">
    <li
      v-for="tile in TILES"
      :key="tile.key"
      class="flex items-start gap-12px rounded-big border border-solid border-line-subtle bg-surface-card px-14px py-13px"
    >
      <span class="grid h-32px w-32px shrink-0 place-items-center rounded-[9px] bg-surface-raised text-ink-secondary">
        <OnIcon :name="CATEGORY_ICONS[tile.key]" :size="16" />
      </span>
      <span class="min-w-0 flex-1">
        <span class="block text-13px font-600 text-ink-primary">{{ t(`cleanup.categories.${tile.key}`) }}</span>
        <span class="mt-2px block text-12px leading-[1.5] text-ink-tertiary">{{
          t(`cleanup.categoryHints.${tile.key}`)
        }}</span>
      </span>
      <OnChip size="sm" :tone="RISK_TONES[tile.risk]" class="shrink-0">{{ t(`cleanup.risks.${tile.risk}`) }}</OnChip>
    </li>
  </ul>
</template>
