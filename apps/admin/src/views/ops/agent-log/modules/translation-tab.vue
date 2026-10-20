<script setup lang="tsx">
import { computed, reactive } from 'vue';
import { NButton, NTag, NText, NTooltip } from 'naive-ui';
import { fetchTranslationLogs } from '@/service/api';
import type { QueryOf, RecordOf, Schemas } from '@/typings/api/opennavo';
import { defaultTransform, useNaivePaginatedTable } from '@/hooks/common/table';
import { CONTENT_LOCALE_OPTIONS } from '@/utils/content-locale';
import { compactParams, formatDateTime } from '@/utils/opennavo';
import { $t } from '@/locales';
import { actorLabel, contentRefLabel, rangeParams } from './shared';

defineOptions({ name: 'AgentTranslationTab' });

type Row = RecordOf<'listTranslationLogs'>;

// AI translations (90 days, read-only history): language pair, model, tokens, duration, failure. Hermes
// writes translations now (S8), so there is no retry here.
const STATUS_TAG = { succeeded: 'success', failed: 'error', stale: 'default' } as const;
const filters = reactive<{ status: Row['status'] | null; locale: Schemas['Locale'] | null; range: [number, number] | null }>({
  status: null,
  locale: null,
  range: null
});
const page = reactive({ current: 1, size: 20 });
const statusOptions = computed(() =>
  (['succeeded', 'failed', 'stale'] as const).map(value => ({ value, label: $t(`page.ops.agentLog.translationStatus.${value}`) }))
);

const { columns, data, loading, getData, getDataByPage, mobilePagination } = useNaivePaginatedTable({
  api: () =>
    fetchTranslationLogs(
      compactParams({
        status: filters.status,
        locale: filters.locale,
        ...rangeParams(filters.range),
        current: page.current,
        size: page.size
      }) as QueryOf<'listTranslationLogs'>
    ),
  transform: response => defaultTransform(response),
  onPaginationParamsChange: params => {
    page.current = params.page ?? 1;
    page.size = params.pageSize ?? 20;
  },
  columns: () => [
    { key: 'createdAt', title: $t('page.ops.agentLog.columns.time'), width: 170, render: row => formatDateTime(row.createdAt) },
    {
      key: 'ref',
      title: $t('page.ops.agentLog.columns.object'),
      minWidth: 180,
      render: row => (
        <div class="flex-col">
          <span>{contentRefLabel(row.ref)}</span>
          <NText depth={3} class="text-12px">
            {row.fields.join(', ')}
          </NText>
        </div>
      )
    },
    {
      key: 'locales',
      title: $t('page.ops.agentLog.columns.locales'),
      width: 220,
      render: row => <span class="font-mono text-12px">{`${row.sourceLocale} → ${row.targetLocales.join(' ')}`}</span>
    },
    { key: 'model', title: $t('page.ops.agentLog.columns.model'), width: 140, ellipsis: { tooltip: true } },
    {
      key: 'tokens',
      title: $t('page.ops.agentLog.columns.tokens'),
      width: 150,
      render: row => (
        <NTooltip>
          {{
            trigger: () => <span>{row.promptTokens + row.completionTokens}</span>,
            default: () =>
              $t('page.ops.agentLog.tokenDetail', {
                prompt: row.promptTokens,
                completion: row.completionTokens,
                reasoning: row.reasoningTokens,
                cached: row.cachedTokens
              })
          }}
        </NTooltip>
      )
    },
    { key: 'latencyMs', title: $t('page.ops.agentLog.columns.duration'), width: 90, align: 'right', render: row => `${row.latencyMs} ms` },
    {
      key: 'status',
      title: $t('page.ops.agentLog.columns.result'),
      width: 160,
      render: row => {
        const tag = (
          <NTag size="small" bordered={false} type={STATUS_TAG[row.status]}>
            {$t(`page.ops.agentLog.translationStatus.${row.status}`)}
          </NTag>
        );
        return row.reason ? <NTooltip>{{ trigger: () => tag, default: () => row.reason }}</NTooltip> : tag;
      }
    },
    { key: 'actor', title: $t('page.ops.agentLog.columns.actor'), width: 220, ellipsis: { tooltip: true }, render: row => actorLabel(row.actor) },
  ]
});

defineExpose({ refresh: getData });
</script>

<template>
  <div class="flex-col gap-12px">
    <div class="flex flex-wrap items-center gap-8px">
      <NSelect v-model:value="filters.status" :options="statusOptions" :placeholder="$t('page.ops.agentLog.filters.status')" clearable size="small" class="w-140px" @update:value="getDataByPage(1)" />
      <NSelect v-model:value="filters.locale" :options="CONTENT_LOCALE_OPTIONS" :placeholder="$t('page.ops.agentLog.filters.locale')" clearable size="small" class="w-160px" @update:value="getDataByPage(1)" />
      <NDatePicker v-model:value="filters.range" type="daterange" clearable size="small" class="w-260px" @update:value="getDataByPage(1)" />
      <NButton size="small" :loading="loading" @click="getData">{{ $t('common.refresh') }}</NButton>
    </div>
    <NDataTable
      :columns="columns"
      :data="data"
      size="small"
      :scroll-x="1500"
      :loading="loading"
      remote
      :row-key="row => row.id"
      :pagination="mobilePagination"
    />
  </div>
</template>
