<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRouter } from 'vue-router';
import { formatBytes, formatRelativeTime } from '@opennavo/shared';
import { OnButton, OnCard, OnChip, OnIcon, OnRailPanel, OnRailSection, OnStackBar } from '@opennavo/ui';
import { useAppLocale } from '@/composables/useAppLocale';
import { useCleanupStore } from '@/stores/cleanup';

// Clean Up Mac rail (cleanup proposal §03): disk with the reclaimable share, last scan, protected list.
const { t } = useI18n();
const { appLocale } = useAppLocale();
const router = useRouter();
const store = useCleanupStore();

const format = (bytes: number) => formatBytes(bytes, { locale: appLocale.value });
const volume = computed(() => store.volume);
// While reviewing, the open report (protecting items updates it at once); otherwise the summary, refreshed after cleaning.
const reclaimable = computed(() => {
  if (store.report && (store.stage === 'review' || store.stage === 'submitting')) return store.totals.reclaimable;
  return store.summary?.scannedAt != null ? store.summary.reclaimableBytes : 0;
});
const parts = computed(() => {
  const disk = volume.value;
  if (!disk) return [];
  const used = Math.max(0, disk.totalBytes - disk.freeBytes);
  return [
    { key: 'used', label: t('cleanup.rail.used'), value: Math.max(0, used - reclaimable.value) },
    ...(reclaimable.value
      ? [{ key: 'reclaimable', label: t('cleanup.rail.reclaimable'), value: reclaimable.value }]
      : []),
    { key: 'free', label: t('cleanup.rail.available'), value: disk.freeBytes }
  ].map(part => ({ ...part, display: format(part.value) }));
});
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
  <OnRailPanel>
    <OnRailSection
      :title="t('cleanup.rail.disk')"
      :subtitle="volume ? t('cleanup.rail.diskSub', { name: volume.name, size: format(volume.totalBytes) }) : undefined"
    >
      <OnCard padding="md">
        <template v-if="volume">
          <p class="m-0 mb-12px text-26px font-600 leading-none text-ink-primary">
            {{ t('cleanup.rail.free', { size: format(volume.freeBytes) }) }}
          </p>
          <OnStackBar :items="parts" :caption="t('cleanup.rail.disk')" :format="format" />
        </template>
        <p v-else class="m-0 text-12.5px leading-[1.55] text-ink-tertiary">
          {{ t('cleanup.rail.diskUnknown') }}
        </p>
      </OnCard>
    </OnRailSection>

    <OnCard padding="md" class="flex flex-col gap-10px">
      <h2 class="m-0 flex items-center gap-7px text-14px font-600 text-ink-primary">
        <OnIcon name="clock" :size="15" />{{ t('cleanup.rail.last') }}
      </h2>
      <p class="m-0 text-12.5px leading-[1.55] text-ink-tertiary">
        {{ last ? t('cleanup.rail.lastScan', last) : t('cleanup.rail.lastNone') }}
      </p>
      <OnButton variant="secondary" @click="router.push('/history')">{{ t('cleanup.rail.history') }}</OnButton>
    </OnCard>

    <OnCard padding="md" class="flex flex-col gap-10px">
      <h2 class="m-0 flex items-center gap-7px text-14px font-600 text-ink-primary">
        <OnIcon name="shield" :size="15" />{{ t('cleanup.rail.protected') }}
        <OnChip size="sm" class="ml-auto">{{
          t('cleanup.rail.protectedCount', { count: store.rules.length }, { plural: store.rules.length })
        }}</OnChip>
      </h2>
      <p class="m-0 text-12.5px leading-[1.55] text-ink-tertiary">
        {{ t('cleanup.rail.protectedBody') }}
      </p>
      <OnButton variant="secondary" @click="router.push('/settings/cleanup')">{{ t('cleanup.rail.manage') }}</OnButton>
    </OnCard>
  </OnRailPanel>
</template>
