<script setup lang="ts">
import { computed, ref } from 'vue';
import { fetchIndexReport } from '@/service/api';
import type { DataOf, QueryOf } from '@/typings/api/opennavo';
import { $t } from '@/locales';
import { formatDateTime } from '@/utils/opennavo';

defineOptions({ name: 'IndexReport' });

// Impact report (WP11): apps the rule finds ineligible or that carry an override, with counts per
// reason. Review it in "Observe only" before enforcing.
type Reason = NonNullable<QueryOf<'listIndexReport'>['reason']>;
const REASONS: { reason: Reason; count: keyof DataOf<'listIndexReport'>['counts'] }[] = [
  { reason: 'missing_summary', count: 'missingSummary' },
  { reason: 'thin', count: 'thin' },
  { reason: 'unavailable', count: 'unavailable' },
  { reason: 'hidden', count: 'hidden' },
  { reason: 'forced_noindex', count: 'forcedNoindex' },
  { reason: 'forced_index', count: 'forcedIndex' }
];
const SIZE = 20;
const report = ref<DataOf<'listIndexReport'>>();
const filter = ref<Reason | null>(null);
const current = ref(1);
const loading = ref(false);
const failed = ref(false);
const pageCount = computed(() => Math.max(1, Math.ceil((report.value?.total ?? 0) / SIZE)));

const text = (key: string) => $t(`page.system.seo.report.${key}` as App.I18n.I18nKey);
const reasonLabel = (value: string) => $t(`page.system.seo.reasons.${value}` as App.I18n.I18nKey);
const overrideLabel = (value: string) => $t(`page.catalog.packageDetail.index.overrides.${value}` as App.I18n.I18nKey);

async function load() {
  loading.value = true;
  failed.value = false;
  try {
    const { data, error } = await fetchIndexReport({
      current: current.value,
      size: SIZE,
      ...(filter.value ? { reason: filter.value } : {})
    });
    if (error) failed.value = true;
    else report.value = data;
  } finally {
    loading.value = false;
  }
}

function choose(reason: Reason | null) {
  filter.value = filter.value === reason ? null : reason;
  current.value = 1;
  void load();
}

function turn(page: number) {
  current.value = page;
  void load();
}

void load();
</script>

<template>
  <div class="w-full">
    <NText depth="3" class="mb-12px block">{{ text('hint') }}</NText>
    <NAlert v-if="failed" type="error" class="mb-12px">{{ text('loadFailed') }}</NAlert>
    <div v-if="report" class="mb-12px flex flex-wrap gap-8px" data-testid="index-report-counts">
      <NTag
        v-for="item in REASONS"
        :key="item.reason"
        size="small"
        checkable
        :checked="filter === item.reason"
        @update:checked="choose(item.reason)"
      >
        {{ reasonLabel(item.reason) }} · {{ report.counts[item.count] }}
      </NTag>
    </div>
    <NSpin :show="loading">
      <NTable v-if="report?.records.length" size="small" :single-line="false">
        <thead>
          <tr>
            <th>{{ text('app') }}</th>
            <th>{{ text('reason') }}</th>
            <th>{{ text('override') }}</th>
            <th>{{ text('enforced') }}</th>
            <th>{{ text('firstSeen') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="row in report.records" :key="row.id">
            <td>
              <RouterLink
                :to="{ name: 'catalog_package-detail', params: { id: String(row.id) }, query: { tab: 'index' } }"
                class="text-primary"
              >
                {{ row.displayName }}
              </RouterLink>
              <NText depth="3" class="block text-12px">{{ row.token }}</NText>
            </td>
            <td>{{ row.reason ? reasonLabel(row.reason) : reasonLabel('forced_index') }}</td>
            <td>
              {{ overrideLabel(row.override) }}
              <NText v-if="row.overrideReason" depth="3" class="block text-12px">{{ row.overrideReason }}</NText>
            </td>
            <td>
              <NTag size="small" :type="row.enforced ? 'warning' : 'default'">
                {{ row.enforced ? text('enforcedYes') : text('enforcedNo') }}
              </NTag>
            </td>
            <td>{{ formatDateTime(row.firstSeenAt) }}</td>
          </tr>
        </tbody>
      </NTable>
      <NEmpty v-else-if="report" :description="text('empty')" class="py-24px" />
    </NSpin>
    <div v-if="pageCount > 1" class="mt-12px flex justify-end">
      <NPagination :page="current" :page-count="pageCount" @update:page="turn" />
    </div>
  </div>
</template>
