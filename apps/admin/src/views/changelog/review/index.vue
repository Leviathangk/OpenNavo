<script setup lang="tsx">
import { computed, reactive, ref } from 'vue';
import { NButton, NTag, NText, NTooltip } from 'naive-ui';
import { fetchTranslations } from '@/service/api';
import type { QueryOf, RecordOf, Schemas } from '@/typings/api/opennavo';
import { useAppStore } from '@/store/modules/app';
import { useAuth } from '@/hooks/business/auth';
import { defaultTransform, useNaivePaginatedTable } from '@/hooks/common/table';
import { CONTENT_LOCALES, CONTENT_LOCALE_OPTIONS, TRANSLATION_STATUS_TAG, localeName } from '@/utils/content-locale';
import type { ContentLocale } from '@/utils/content-locale';
import { booleanOptions, compactParams, formatDateTime, toBoolean } from '@/utils/opennavo';
import { $t } from '@/locales';
import FixDrawer from './modules/fix-drawer.vue';

defineOptions({ name: 'ChangelogReview' });

type Item = RecordOf<'listTranslations'>;
type LocaleStatus = Schemas['ContentLocaleState']['status'];

// Translation management (07 §7.8, M9-02), replacing review: six-language state per object, retranslation of selected languages,
// and manual corrections. AI translations take effect immediately; corrections become manual.
const ENTITIES: Schemas['ContentEntity'][] = [
  'package',
  'release',
  'category',
  'collection',
  'collection_item',
  'feature',
  'screenshot',
  'desktop_release',
  'mirror',
  'announcement'
];
const STATUSES: LocaleStatus[] = ['source', 'machine', 'manual', 'pending', 'failed', 'missing', 'skipped'];

const appStore = useAppStore();
const { hasAuth } = useAuth();
const canReview = computed(() => hasAuth('translation:review'));
const filters = reactive<{
  entity: Schemas['ContentEntity'] | null;
  locale: ContentLocale | null;
  status: LocaleStatus | null;
  stale: string | null;
  q: string;
}>({ entity: null, locale: null, status: null, stale: null, q: '' });
const page = reactive({ current: 1, size: 20 });
const fixing = ref<Item | null>(null);
const fixOpen = ref(false);

const entityOptions = computed(() => ENTITIES.map(value => ({ value, label: $t(`page.changelog.translations.entities.${value}`) })));
const statusOptions = computed(() => STATUSES.map(value => ({ value, label: $t(`page.changelog.translations.statuses.${value}`) })));
const staleOptions = computed(() =>
  booleanOptions($t('page.changelog.translations.staleOnly'), $t('page.changelog.translations.upToDate'))
);

const statusType = (status: LocaleStatus) =>
  (TRANSLATION_STATUS_TAG as Record<string, 'default' | 'success' | 'info' | 'primary' | 'warning' | 'error'>)[status] ?? 'default';

const { columns, data, loading, getData, getDataByPage, mobilePagination } = useNaivePaginatedTable({
  api: () =>
    fetchTranslations(
      compactParams({
        ...filters,
        stale: toBoolean(filters.stale),
        q: filters.q.trim() || null,
        current: page.current,
        size: page.size
      }) as QueryOf<'listTranslations'>
    ),
  transform: response => defaultTransform(response),
  onPaginationParamsChange: params => {
    page.current = params.page ?? 1;
    page.size = params.pageSize ?? 20;
  },
  columns: () => [
    {
      key: 'label',
      title: $t('page.changelog.translations.columns.object'),
      minWidth: 220,
      render: row => (
        <div class="flex-col">
          {row.webUrl ? (
            <a href={row.webUrl} target="_blank" rel="noopener noreferrer" class="text-primary">
              {row.label}
            </a>
          ) : (
            <span>{row.label}</span>
          )}
          <NText depth={3} class="text-12px">
            {$t(`page.changelog.translations.entities.${row.ref.entity}`)} · {localeName(row.sourceLocale)}
          </NText>
        </div>
      )
    },
    ...CONTENT_LOCALES.map(code => ({
      key: code,
      title: code,
      width: 96,
      render: (row: Item) => {
        const state = row.i18n[code];
        if (!state) return '—';
        const tag = (
          <NTag size="small" bordered={false} type={statusType(state.status)}>
            {$t(`page.changelog.translations.statuses.${state.status}`)}
            {state.stale ? ' *' : ''}
          </NTag>
        );
        return state.failReason ? <NTooltip>{{ trigger: () => tag, default: () => state.failReason }}</NTooltip> : tag;
      }
    })),
    { key: 'updatedAt', title: $t('page.changelog.translations.columns.updatedAt'), width: 160, render: row => formatDateTime(row.updatedAt) },
    {
      key: 'operate',
      title: $t('common.operate'),
      width: 220,
      align: 'center',
      fixed: 'right',
      render: row =>
        canReview.value ? (
          <div class="flex flex-wrap items-center justify-center gap-6px">
            <NButton size="small" type="primary" ghost onClick={() => openFix(row)}>
              {$t('page.changelog.translations.fix')}
            </NButton>
          </div>
        ) : (
          '—'
        )
    }
  ]
});

function openFix(row: Item) {
  fixing.value = row;
  fixOpen.value = true;
}
</script>

<template>
  <div class="min-h-500px flex-col-stretch gap-16px overflow-hidden lt-sm:overflow-auto">
    <NCard
      :title="$t('page.changelog.translations.title')"
      :bordered="false"
      size="small"
      class="card-wrapper sm:flex-1-hidden"
      content-class="flex-col"
    >
      <template #header-extra>
        <div class="flex flex-wrap items-center gap-8px">
          <NInput v-model:value="filters.q" :placeholder="$t('page.changelog.translations.search')" clearable size="small" class="!w-180px" @change="getDataByPage(1)" />
          <NSelect v-model:value="filters.entity" :options="entityOptions" :placeholder="$t('page.changelog.translations.entity')" clearable size="small" class="w-140px" @update:value="getDataByPage(1)" />
          <NSelect v-model:value="filters.locale" :options="CONTENT_LOCALE_OPTIONS" :placeholder="$t('page.changelog.translations.locale')" clearable size="small" class="w-140px" @update:value="getDataByPage(1)" />
          <NSelect v-model:value="filters.status" :options="statusOptions" :placeholder="$t('page.changelog.translations.status')" clearable size="small" class="w-120px" @update:value="getDataByPage(1)" />
          <NSelect v-model:value="filters.stale" :options="staleOptions" :placeholder="$t('page.changelog.translations.stale')" clearable size="small" class="w-130px" @update:value="getDataByPage(1)" />
          <NButton size="small" :loading="loading" @click="getData">{{ $t('common.refresh') }}</NButton>
        </div>
      </template>
      <NText depth="3" class="mb-8px block text-12px">{{ $t('page.changelog.translations.hint') }}</NText>
      <NDataTable
        :columns="columns"
        :data="data"
        size="small"
        :flex-height="!appStore.isMobile"
        :scroll-x="1350"
        :loading="loading"
        remote
        :row-key="row => `${row.ref.entity}:${row.ref.id ?? ''}:${row.ref.secondaryId ?? ''}`"
        :pagination="mobilePagination"
        class="sm:flex-1-hidden"
      />
    </NCard>
    <FixDrawer v-model:show="fixOpen" :item="fixing" @saved="getData" />
  </div>
</template>
