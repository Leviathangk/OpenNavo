<script setup lang="ts">
import { formatBytes } from '@opennavo/shared';
import { OnIcon } from '@opennavo/ui';

// Clean Up Mac demo (desktop cleanup review): reclaimable space by category with risk tiers, then the cleanup
// runs and reports what it freed. Sizes are illustrative.
const DURATIONS = [3000, 1300, 2800] as const;
const ROWS = [
  { key: 'app_caches', risk: 'rebuildable', bytes: 6.42e9, color: 'bg-brand-coral' },
  { key: 'dev_caches', risk: 'rebuildable', bytes: 5.18e9, color: 'bg-brand-salmon' },
  { key: 'installers', risk: 'review', bytes: 3.96e9, color: 'bg-brand-violet' },
  { key: 'leftovers', risk: 'review', bytes: 2.31e9, color: 'bg-status-info' }
] as const;

const { t, locale } = useI18n();
const formattingLocale = computed(() => toAppLocale(locale.value));
const root = ref<HTMLElement>();
const step = useDemoLoop(root, DURATIONS);

const totalBytes = ROWS.reduce((sum, row) => sum + row.bytes, 0);
const total = computed(() => formatBytes(totalBytes, { locale: formattingLocale.value }));
const size = (bytes: number) => formatBytes(bytes, { locale: formattingLocale.value });
</script>

<template>
  <div ref="root" class="mx-auto flex w-full max-w-330px flex-col gap-10px" inert aria-hidden="true">
    <div class="flex items-end justify-between gap-12px">
      <span class="pb-4px text-12px text-ink-tertiary">{{ t('landing.toolkit.demo.reclaimable') }}</span>
      <span class="text-28px font-600 leading-none tracking-[-0.02em] text-ink-primary tabular-nums">{{ total }}</span>
    </div>
    <div class="flex h-6px gap-2px overflow-hidden rounded-full">
      <span
        v-for="row in ROWS"
        :key="row.key"
        class="h-full transition-opacity duration-slow ease-standard"
        :class="[row.color, step === 2 ? 'opacity-20' : '']"
        :style="{ flex: row.bytes }"
      ></span>
    </div>
    <ul class="m-0 flex list-none flex-col gap-2px p-0">
      <li v-for="row in ROWS" :key="row.key" class="flex h-26px items-center gap-8px text-12.5px">
        <span class="h-7px w-7px shrink-0 rounded-full" :class="row.color"></span>
        <span class="min-w-0 flex-1 truncate text-ink-secondary">{{
          t(`landing.toolkit.demo.categories.${row.key}`)
        }}</span>
        <span
          class="inline-flex h-18px shrink-0 items-center rounded-full px-6px text-10.5px font-500"
          :class="
            row.risk === 'rebuildable'
              ? 'bg-status-success-subtle text-status-success'
              : 'bg-status-warning-subtle text-status-warning'
          "
          >{{ t(`landing.toolkit.demo.risks.${row.risk}`) }}</span
        >
        <span class="w-60px shrink-0 text-right text-ink-tertiary tabular-nums">{{ size(row.bytes) }}</span>
      </li>
    </ul>
    <span
      class="flex h-32px items-center justify-center gap-6px rounded-default text-12.5px font-600 transition-colors duration-base ease-standard"
      :class="
        step === 0
          ? 'bg-button-primary-bg text-button-primary-text'
          : step === 1
            ? 'bg-button-disabled-bg text-button-disabled-text'
            : 'bg-status-success-subtle text-status-success'
      "
    >
      <OnIcon v-if="step === 2" name="check" :size="14" />
      <span class="tabular-nums">{{
        step === 2 ? t('landing.toolkit.demo.freed', { size: total }) : t('landing.toolkit.demo.clean', { size: total })
      }}</span>
    </span>
  </div>
</template>
