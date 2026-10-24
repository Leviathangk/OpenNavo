<script setup lang="tsx">
import { computed, reactive, ref } from 'vue';
import { NButton, NTag, NText } from 'naive-ui';
import { deleteGlossaryTerm, fetchGlossary } from '@/service/api';
import type { QueryOf, RecordOf } from '@/typings/api/opennavo';
import { useAppStore } from '@/store/modules/app';
import { useAuth } from '@/hooks/business/auth';
import { defaultTransform, useNaivePaginatedTable } from '@/hooks/common/table';
import { CONTENT_LOCALES } from '@/utils/content-locale';
import { booleanOptions, compactParams, formatDateTime, toBoolean } from '@/utils/opennavo';
import { $t } from '@/locales';
import GlossaryForm from './modules/glossary-form.vue';

defineOptions({ name: 'ContentGlossary' });

type Row = RecordOf<'listGlossary'>;

// Content → Glossary (M9-02): fixed translations or no-translate terms for automated translation; Hermes can maintain it; deletions enter recycle bin.
const appStore = useAppStore();
const { hasAuth } = useAuth();
const editable = computed(() => hasAuth('content:glossary:edit'));
const filters = reactive<{ q: string | null; doNotTranslate: string | null }>({ q: null, doNotTranslate: null });
const page = reactive({ current: 1, size: 20 });
const formOpen = ref(false);
const editing = ref<Row | null>(null);
const typeOptions = computed(() =>
  booleanOptions($t('page.content.glossary.doNotTranslate'), $t('page.content.glossary.fixedTranslation'))
);

const { columns, data, loading, getData, getDataByPage, mobilePagination } = useNaivePaginatedTable({
  api: () =>
    fetchGlossary(
      compactParams({
        q: filters.q?.trim() || null,
        doNotTranslate: toBoolean(filters.doNotTranslate),
        current: page.current,
        size: page.size
      }) as QueryOf<'listGlossary'>
    ),
  transform: response => defaultTransform(response),
  onPaginationParamsChange: params => {
    page.current = params.page ?? 1;
    page.size = params.pageSize ?? 20;
  },
  columns: () => [
    { key: 'term', title: $t('page.content.glossary.term'), width: 200, render: row => <span class="font-600">{row.term}</span> },
    {
      key: 'translations',
      title: $t('page.content.glossary.translations'),
      minWidth: 360,
      render: row =>
        row.doNotTranslate ? (
          <NTag size="small" bordered={false} type="info">
            {$t('page.content.glossary.keepOriginal')}
          </NTag>
        ) : (
          <div class="flex flex-wrap gap-x-12px gap-y-2px">
            {CONTENT_LOCALES.filter(code => row.translations[code]).map(code => (
              <span key={code}>
                <NText depth={3} class="font-mono text-12px">
                  {code}
                </NText>{' '}
                {row.translations[code]}
              </span>
            ))}
          </div>
        )
    },
    { key: 'updatedAt', title: $t('page.content.glossary.updatedAt'), width: 170, render: row => formatDateTime(row.updatedAt) },
    {
      key: 'operate',
      title: $t('common.operate'),
      width: 200,
      align: 'center',
      fixed: 'right',
      render: row =>
        editable.value ? (
          <div class="flex flex-wrap items-center justify-center gap-6px">
            <NButton size="small" onClick={() => openForm(row)}>
              {$t('common.edit')}
            </NButton>
            <NButton size="small" type="error" ghost onClick={() => remove(row)}>
              {$t('common.delete')}
            </NButton>
          </div>
        ) : (
          '—'
        )
    }
  ]
});

function openForm(row: Row | null) {
  editing.value = row;
  formOpen.value = true;
}

function remove(row: Row) {
  window.$dialog?.warning({
    title: $t('common.delete'),
    content: $t('page.content.glossary.deleteConfirm', { term: row.term }),
    positiveText: $t('common.confirm'),
    negativeText: $t('common.cancel'),
    onPositiveClick: async () => {
      const { error } = await deleteGlossaryTerm(row.id);
      if (error) return;
      window.$message?.success($t('page.content.glossary.deleted'));
      await getData();
    }
  });
}
</script>

<template>
  <div class="min-h-500px flex-col-stretch gap-16px overflow-hidden lt-sm:overflow-auto">
    <NCard
      :title="$t('page.content.glossary.title')"
      :bordered="false"
      size="small"
      class="card-wrapper sm:flex-1-hidden"
    >
      <template #header-extra>
        <div class="flex items-center gap-8px">
          <NInput
            v-model:value="filters.q"
            :placeholder="$t('page.content.glossary.search')"
            clearable
            size="small"
            class="!w-200px"
            @change="getDataByPage(1)"
          />
          <NSelect
            v-model:value="filters.doNotTranslate"
            :options="typeOptions"
            :placeholder="$t('page.content.glossary.type')"
            clearable
            size="small"
            class="w-140px"
            @update:value="getDataByPage(1)"
          />
          <NButton size="small" :loading="loading" @click="getData">{{ $t('common.refresh') }}</NButton>
          <PermissionGate code="content:glossary:edit">
            <NButton size="small" type="primary" @click="openForm(null)">{{ $t('page.content.glossary.add') }}</NButton>
          </PermissionGate>
        </div>
      </template>
      <NDataTable
        :columns="columns"
        :data="data"
        size="small"
        :flex-height="!appStore.isMobile"
        :scroll-x="950"
        :loading="loading"
        remote
        :row-key="row => row.id"
        :pagination="mobilePagination"
        class="sm:h-full"
      />
    </NCard>
    <GlossaryForm v-model:show="formOpen" :term="editing" @saved="getData" />
  </div>
</template>
