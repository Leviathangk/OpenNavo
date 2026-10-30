<script setup lang="tsx">
import { computed as computedFormattingLocale } from 'vue';
import { useAppStore as useFormattingAppStore } from '@/store/modules/app';
import { reactive } from 'vue';
import { NButton, NEllipsis, NTag, NText } from 'naive-ui';
import { formatCount } from '@opennavo/shared';
import { fetchPackageList, resyncPackage } from '@/service/api';
import type { QueryOf, RecordOf } from '@/typings/api/opennavo';
import { useAppStore } from '@/store/modules/app';
import { useAuth } from '@/hooks/business/auth';
import { useRouterPush } from '@/hooks/common/router';
import { defaultTransform, useNaivePaginatedTable } from '@/hooks/common/table';
import PackageIcon from '@/components/opennavo/package-icon.vue';
import { useLocalizedPackage } from '@/hooks/business/localized';
import { compactParams, formatDateTime, toBoolean } from '@/utils/opennavo';
import { TRANSLATION_STATUS_TAG } from '@/utils/content-locale';
import { $t } from '@/locales';
import { createFilters } from './modules/filters';
import PackageSearch from './modules/package-search.vue';

const formattingAppStore = useFormattingAppStore();
const formattingLocale = computedFormattingLocale(() => formattingAppStore.locale);

defineOptions({ name: 'CatalogPackage' });

type Row = RecordOf<'listAdminPackages'>;

// Packages (07 §7.2): filter card and remote pagination; edit opens details, More actions follow permissions.
const appStore = useAppStore();
const { hasAuth } = useAuth();
const { routerPushByKey } = useRouterPush();
const { nameOf, summaryOf } = useLocalizedPackage();

const filters = reactive(createFilters());
const page = reactive({ current: 1, size: 20 });

function query() {
  return compactParams({
    current: page.current,
    size: page.size,
    q: filters.q.trim(),
    kind: filters.kind,
    categoryId: filters.categoryId,
    translationStatus: filters.translationStatus,
    sort: filters.sort,
    hasIcon: toBoolean(filters.hasIcon),
    hidden: toBoolean(filters.hidden),
    editorChoice: toBoolean(filters.editorChoice),
    deprecated: toBoolean(filters.deprecated),
    disabled: toBoolean(filters.disabled),
    isFont: toBoolean(filters.isFont),
    isLibrary: toBoolean(filters.isLibrary)
  }) as QueryOf<'listAdminPackages'>;
}

const CHANGELOG_TAG = { none: 'default', ok: 'success', error: 'error' } as const;

function openDetail(row: Row) {
  void routerPushByKey('catalog_package-detail', { params: { id: String(row.id) } });
}

async function resync(row: Row) {
  const { error } = await resyncPackage(row.id);
  if (!error) window.$message?.success($t('page.catalog.package.resyncQueued', { token: row.token }));
}

const { columns, columnChecks, data, getData, getDataByPage, loading, mobilePagination } = useNaivePaginatedTable({
  api: () => fetchPackageList(query()),
  transform: response => defaultTransform(response),
  paginationProps: { pageSizes: [20, 50, 100] },
  onPaginationParamsChange: params => {
    page.current = params.page ?? 1;
    page.size = params.pageSize ?? 20;
  },
  columns: () => [
    {
      key: 'icon',
      title: $t('page.catalog.package.columns.icon'),
      width: 64,
      align: 'center',
      render: row => <PackageIcon url={row.iconUrl} name={nameOf(row)} kind={row.kind} />
    },
    {
      key: 'name',
      title: $t('page.catalog.package.columns.name'),
      minWidth: 200,
      render: row => (
        <div class="flex-col">
          <span class="font-600">{nameOf(row)}</span>
          <NText depth={3} class="font-mono text-12px">
            {row.token}
          </NText>
        </div>
      )
    },
    {
      key: 'version',
      title: $t('page.catalog.package.columns.version'),
      width: 130,
      ellipsis: { tooltip: true },
      render: row => <span class="font-mono text-12px">{row.version}</span>
    },
    {
      key: 'installs30d',
      title: $t('page.catalog.package.columns.installs30d'),
      width: 110,
      align: 'right',
      render: row => <span class="tabular-nums">{formatCount(row.installs30d, { locale: formattingLocale.value })}</span>
    },
    {
      key: 'rank30d',
      title: $t('page.catalog.package.columns.rank'),
      width: 72,
      align: 'right',
      render: row => (row.rank30d ? <span class="tabular-nums">#{row.rank30d}</span> : '—')
    },
    {
      key: 'primaryCategory',
      title: $t('page.catalog.package.columns.category'),
      width: 120,
      render: row => row.primaryCategory?.name ?? '—'
    },
    {
      key: 'summary',
      title: $t('page.catalog.package.columns.summary'),
      minWidth: 260,
      render: row => (
        <div class="flex items-center gap-8px">
          <NEllipsis class="min-w-0 flex-1">{summaryOf(row) ?? '—'}</NEllipsis>
          <NTag size="small" bordered={false} type={TRANSLATION_STATUS_TAG[row.translationStatus]}>
            {$t(`page.shared.translationStatus.${row.translationStatus}`)}
          </NTag>
        </div>
      )
    },
    {
      key: 'changelogStatus',
      title: $t('page.catalog.package.columns.changelog'),
      width: 96,
      render: row => (
        <NTag size="small" bordered={false} type={CHANGELOG_TAG[row.changelogStatus]}>
          {$t(`page.shared.changelogStatus.${row.changelogStatus}`)}
        </NTag>
      )
    },
    {
      key: 'flags',
      title: $t('page.catalog.package.columns.flags'),
      width: 150,
      render: row => {
        const flags = [
          row.private && { type: 'warning', label: $t('page.catalog.packageDetail.basic.private') },
          row.hidden && { type: 'default', label: $t('page.shared.flags.hidden') },
          row.editorChoice && { type: 'primary', label: $t('page.shared.flags.editorChoice') },
          row.deprecated && { type: 'warning', label: $t('page.shared.flags.deprecated') },
          row.disabled && { type: 'error', label: $t('page.shared.flags.disabled') }
        ].filter(Boolean) as { type: 'default' | 'primary' | 'warning' | 'error'; label: string }[];
        return flags.length ? (
          <div class="flex flex-wrap gap-4px">
            {flags.map(flag => (
              <NTag size="small" bordered={false} type={flag.type}>
                {flag.label}
              </NTag>
            ))}
          </div>
        ) : (
          '—'
        );
      }
    },
    {
      key: 'updateTime',
      title: $t('page.catalog.package.columns.updateTime'),
      width: 170,
      render: row => formatDateTime(row.updateTime)
    },
    {
      key: 'operate',
      title: $t('common.operate'),
      width: 150,
      align: 'center',
      fixed: 'right',
      render: row => (
        <div class="flex-center gap-8px">
          <NButton size="small" type="primary" ghost onClick={() => openDetail(row)}>
            {$t('common.edit')}
          </NButton>
          {hasAuth('catalog:package:resync') && (
            <NButton size="small" onClick={() => resync(row)}>
              {$t('page.catalog.package.resync')}
            </NButton>
          )}
        </div>
      )
    }
  ]
});

function search() {
  void getDataByPage(1);
}

function reset() {
  Object.assign(filters, createFilters());
  search();
}
</script>

<template>
  <div class="min-h-500px flex-col-stretch gap-16px overflow-hidden lt-sm:overflow-auto">
    <PackageSearch v-model:filters="filters" @search="search" @reset="reset" />
    <NCard
      :title="$t('page.catalog.package.title')"
      :bordered="false"
      size="small"
      class="card-wrapper sm:flex-1-hidden"
    >
      <template #header-extra>
        <TableHeaderOperation v-model:columns="columnChecks" :loading="loading" @refresh="getData">
          <template #default>
            <span></span>
          </template>
        </TableHeaderOperation>
      </template>
      <NDataTable
        :columns="columns"
        :data="data"
        size="small"
        :flex-height="!appStore.isMobile"
        :scroll-x="1660"
        :loading="loading"
        remote
        :row-key="row => row.id"
        :pagination="mobilePagination"
        class="sm:h-full"
      />
    </NCard>
  </div>
</template>
