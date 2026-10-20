<script setup lang="ts">
import { computed } from 'vue';
import { useAppStore } from '@/store/modules/app';
import type { Schemas } from '@/typings/api/opennavo';
import { $t } from '@/locales';

const props = defineProps<{ geo: NonNullable<Schemas['SeoGeoSummary']> }>();
const appStore = useAppStore();
const count = (value: number | null | undefined) =>
  value == null ? $t('page.dashboard.seo.geo.unknown') : new Intl.NumberFormat(appStore.locale).format(value);
const ratio = (value: number | null | undefined, denominator?: number | null) =>
  `${count(value)} / ${count(denominator && denominator > 0 ? denominator : null)}`;
const rows = computed(() => {
  const { counts, mode } = props.geo;
  const summaryOnly = mode === 'summary_only';
  return [
    { key: 'planned', value: count(counts.planned) },
    {
      key: 'answered',
      value: ratio(summaryOnly ? counts.reported?.answered : counts.answered, summaryOnly ? null : counts.planned)
    },
    ...(['searchTriggered', 'mentioned', 'candidate', 'cited'] as const).map(key => ({
      key,
      value: summaryOnly
        ? ratio(key === 'searchTriggered' ? null : counts.reported?.[key])
        : ratio(counts[key]?.positive, counts[key]?.known)
    })),
    { key: 'citationComplete', value: summaryOnly ? count(null) : ratio(counts.citationComplete, counts.answered) }
  ] as const;
});
</script>

<template>
  <div class="flex-col gap-8px">
    <template v-if="geo.mode === 'summary_only'">
      <NTag size="small" type="warning">{{ $t('page.dashboard.seo.geo.reported') }}</NTag>
      <NText>{{ $t('page.dashboard.seo.geo.reported') }} · {{ $t('page.dashboard.seo.geo.noComparison') }}</NText>
      <NText v-if="geo.counts.reported?.missingFields.length" class="break-words">
        {{ $t('page.dashboard.seo.geo.missingFields') }}: {{ geo.counts.reported.missingFields.join(', ') }}
      </NText>
    </template>
    <dl class="m-0 grid grid-cols-1 gap-8px sm:grid-cols-2">
      <div v-for="row in rows" :key="row.key" :data-geo-count="row.key" class="flex flex-wrap justify-between gap-8px">
        <dt>{{ $t(`page.dashboard.seo.geo.${row.key}`) }}</dt>
        <dd class="m-0 tabular-nums">{{ row.value }}</dd>
      </div>
    </dl>
  </div>
</template>
