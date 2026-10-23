<script setup lang="ts">
import { computed as computedFormattingLocale } from 'vue';
import { useAppStore as useFormattingAppStore } from '@/store/modules/app';
import { computed } from 'vue';
import { formatCount, formatPercent } from '@opennavo/shared';
import type { DataOf } from '@/typings/api/opennavo';
import { $t } from '@/locales';

const formattingAppStore = useFormattingAppStore();
const formattingLocale = computedFormattingLocale(() => formattingAppStore.locale);

defineOptions({ name: 'CoverageCard' });

const props = defineProps<{ coverage: DataOf<'getDashboardOverview'>['coverage'] }>();

const keys = ['primaryCategory', 'icon', 'latestVersionNotes', 'sixLocaleContent', 'siteLocaleContent'] as const;

// A metric the API does not return yet (e.g. the admin deploys before the API) is skipped instead of breaking the page.
const rows = computed(() =>
  keys.flatMap(key => {
    const ratio = props.coverage[key] as (typeof props.coverage)[typeof key] | undefined;
    if (!ratio) return [];
    const { done, total } = ratio;
    return {
      key,
      label: $t(`page.dashboard.coverageItems.${key}`),
      done,
      total,
      percent: total ? Math.round((done / total) * 1000) / 10 : 0
    };
  })
);
</script>

<template>
  <NCard :bordered="false" size="small" class="card-wrapper h-full" :title="$t('page.dashboard.coverage')">
    <NText depth="3" tag="p" class="mb-12px mt-0 text-12px">{{ $t('page.dashboard.coverageHint') }}</NText>
    <ul class="m-0 flex-col gap-14px p-0">
      <li v-for="row in rows" :key="row.key" class="grid grid-cols-[160px_minmax(0,1fr)_140px] items-center gap-12px">
        <span class="text-13px">{{ row.label }}</span>
        <NProgress type="line" :percentage="row.percent" :show-indicator="false" :height="8" />
        <NText depth="2" class="text-right text-12px tabular-nums">
          {{ formatCount(row.done, { locale: formattingLocale }) }} /
          {{ formatCount(row.total, { locale: formattingLocale }) }} ·
          {{ formatPercent(row.percent / 100, { locale: formattingLocale, digits: 1 }) }}
        </NText>
      </li>
    </ul>
    <NText depth="3" class="mt-12px block text-12px">{{ $t('page.dashboard.localeContentHint') }}</NText>
  </NCard>
</template>
