<script setup lang="ts">
import type { PublicComponents } from '@opennavo/api';
import { formatCount, formatCountCompact } from '@opennavo/shared';
import { installTrend, monthlyInstalls, OnHBarChart } from '@opennavo/ui';

const { locale: useI18nLocale } = useI18n();
const formattingLocale = computed(() => toAppLocale(useI18nLocale.value));

// Monthly average installs (08 §11.7): compare averages from 30/90/365-day aggregates.
const props = defineProps<{ installs: PublicComponents['schemas']['InstallStats'] }>();

const { t, locale } = useI18n();
const appLocale = computed(() => toAppLocale(locale.value));

const items = computed(() =>
  monthlyInstalls(props.installs).map(row => ({
    key: row.key,
    label: t(`package.monthly.${row.key}`),
    value: row.value,
    display: formatCountCompact(row.value, { locale: appLocale.value }),
    full: t(
      'package.monthly.full',
      { count: formatCount(row.value, { locale: formattingLocale.value }) },
      { plural: row.value }
    ),
    emphasis: row.key === 'd30'
  }))
);
</script>

<template>
  <section
    class="rounded-big border border-solid border-line-subtle bg-surface-card px-16px pb-12px pt-14px"
    aria-labelledby="monthly-installs-title"
  >
    <!-- The trend is a paragraph after the heading, not part of it (C3). -->
    <h2 id="monthly-installs-title" class="m-0 text-13px font-600 text-ink-primary">
      {{ t('package.monthly.title') }}
    </h2>
    <p class="m-0 mt-2px text-11.5px text-ink-tertiary">{{ t(`package.monthly.trend.${installTrend(installs)}`) }}</p>
    <OnHBarChart class="mt-12px" :caption="t('package.monthly.title')" :items="items" />
  </section>
</template>
