<script setup lang="ts">
import { computed, ref } from 'vue';
import { RouterLink } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { formatCountCompact } from '@opennavo/shared';
import { OnRankingsView, OnEmpty } from '@opennavo/ui';
import type { OnRankingsEntry } from '@opennavo/ui';
import PackageGetButton from '@/components/package/PackageGetButton.vue';
import { commands, unwrap } from '@/ipc/client';
import type { LocalItem } from '@/ipc/client';
import { useAppLocale } from '@/composables/useAppLocale';
import { useCatalogLoader } from '@/composables/useCatalogLoader';
import { usePackageSummary } from '@/composables/usePackageSummary';

// Rankings (local, offline): app installs over 30 / 90 / 365 days (Cask-only, ADR-018); show unavailable statistics for old snapshots missing 90/365-day data.
type Period = 'installs30d' | 'installs90d' | 'installs365d';

const { t } = useI18n();
const { appLocale } = useAppLocale();
const { toSummary } = usePackageSummary();

const period = ref<Period>('installs30d');

const { data, error } = useCatalogLoader(
  async () => {
    const sort = period.value;
    const result = await unwrap(
      commands.catalogList({
        kind: 'cask',
        category: null,
        sort,
        includeFonts: true,
        includeLibraries: false,
        includeDisabled: false,
        limit: 100,
        offset: 0
      })
    );
    return { period: sort, items: result.items };
  },
  [period, appLocale],
  result => result.items,
  // Switching periods keeps the current ranking on screen until the next one lands.
  []
);

function installs(item: LocalItem, shown: Period): number | null {
  if (shown === 'installs30d') return item.installs30d;
  return (shown === 'installs90d' ? item.installs90d : item.installs365d) ?? null;
}

// Counts come from the period the shown list was ranked by, which trails the tabs until the next list lands.
// A failed read keeps a list of the selected period on screen, never one of another period.
const entries = computed<OnRankingsEntry[] | null>(() => {
  const shown = data.value && (!error.value || data.value.period === period.value) ? data.value : undefined;
  if (!shown) return error.value ? [] : null;
  return shown.items.map((item, index) => {
    const value = installs(item, shown.period);
    const pkg = toSummary(item);
    return {
      rank: index + 1,
      pkg,
      value: value === null ? t('rankings.noStats') : formatCountCompact(value, { locale: appLocale.value }),
      href: `/package/cask/${pkg.token}`
    };
  });
});

const periods = computed(() =>
  (['installs30d', 'installs90d', 'installs365d'] as const).map(value => ({
    value,
    label: t(`rankings.periods.${value}`)
  }))
);
</script>

<template>
  <OnRankingsView
    v-model:period="period"
    :title="t('rankings.title')"
    :subtitle="t('rankings.subtitle')"
    :periods="periods"
    :period-label="t('rankings.periodLabel')"
    :entries="entries"
    :link-as="RouterLink"
  >
    <template #empty><OnEmpty v-if="!error" :title="t('common.empty')" /></template>
    <template #action="{ entry }">
      <PackageGetButton :kind="entry.pkg.kind" :token="entry.pkg.token" :disabled="entry.pkg.disabled" />
    </template>
  </OnRankingsView>
</template>
