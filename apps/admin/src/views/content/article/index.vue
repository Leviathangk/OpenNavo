<script setup lang="tsx">
import { computed, reactive, ref } from 'vue';
import { NButton, NTag, NText } from 'naive-ui';
import type { FormInst, FormRules } from 'naive-ui';
import { createArticle, fetchArticleList } from '@/service/api';
import type { QueryOf, Schemas } from '@/typings/api/opennavo';
import { useAppStore } from '@/store/modules/app';
import { useRouterPush } from '@/hooks/common/router';
import { defaultTransform, useNaivePaginatedTable } from '@/hooks/common/table';
import { compactParams, formatDateTime, isDryRun } from '@/utils/opennavo';
import { CONTENT_LOCALE_OPTIONS, DEFAULT_SOURCE_LOCALE } from '@/utils/content-locale';
import { $t } from '@/locales';
import { ARTICLE_SLUG, ARTICLE_TYPES } from '../article-detail/modules/article';
import type { ArticleType } from '../article-detail/modules/article';

defineOptions({ name: 'ContentArticle' });

// Articles (WP16): drafts, reviews, published and withdrawn articles; editing opens the editor.
const appStore = useAppStore();
const { routerPushByKey } = useRouterPush();
const filters = reactive<{ q: string; status: Schemas['ArticleStatus'] | null; type: ArticleType | null }>({
  q: '',
  status: null,
  type: null
});
const page = reactive({ current: 1, size: 20 });
const text = (key: string, values?: Record<string, unknown>) =>
  values
    ? $t(`page.content.article.${key}` as App.I18n.I18nKey, values)
    : $t(`page.content.article.${key}` as App.I18n.I18nKey);

const STATUS_TAG: Record<Schemas['ArticleStatus'], 'default' | 'warning' | 'success' | 'error'> = {
  draft: 'default',
  review: 'warning',
  published: 'success',
  archived: 'error'
};
const statusOptions = computed(() =>
  (['draft', 'review', 'published', 'archived'] as const).map(value => ({ value, label: text(`statuses.${value}`) }))
);
const typeOptions = computed(() => ARTICLE_TYPES.map(value => ({ value, label: text(`types.${value}`) })));
const openDetail = (id: number) => routerPushByKey('content_article-detail', { params: { id: String(id) } });

const { columns, data, loading, getData, getDataByPage, mobilePagination } = useNaivePaginatedTable({
  api: () =>
    fetchArticleList(
      compactParams({ ...filters, q: filters.q.trim(), current: page.current, size: page.size }) as QueryOf<'listAdminArticles'>
    ),
  transform: response => defaultTransform(response),
  onPaginationParamsChange: params => {
    page.current = params.page ?? 1;
    page.size = params.pageSize ?? 20;
  },
  columns: () => [
    {
      key: 'title',
      title: text('columns.title'),
      minWidth: 260,
      render: row => (
        <div class="flex-col">
          <span class="font-600">{row.title || row.slug}</span>
          <NText depth={3} class="font-mono text-12px">
            {row.slug}
          </NText>
        </div>
      )
    },
    { key: 'type', title: text('columns.type'), width: 110, render: row => text(`types.${row.type}`) },
    {
      key: 'status',
      title: text('columns.status'),
      width: 170,
      render: row => (
        <div class="flex flex-wrap items-center gap-4px">
          <NTag size="small" bordered={false} type={STATUS_TAG[row.status]}>
            {text(`statuses.${row.status}`)}
          </NTag>
          {row.status !== 'draft' && row.status !== 'review' && row.hasPendingChanges && (
            <NTag size="small" bordered={false} type={row.submittedAt ? 'warning' : 'info'}>
              {row.submittedAt ? text('pendingReview') : text('pendingDraft')}
            </NTag>
          )}
        </div>
      )
    },
    { key: 'author', title: text('columns.author'), width: 140, render: row => row.authorName ?? text('editorial') },
    {
      key: 'published',
      title: text('columns.published'),
      width: 170,
      render: row => formatDateTime(row.firstPublishedAt)
    },
    { key: 'updated', title: text('columns.updated'), width: 170, render: row => formatDateTime(row.updatedAt) },
    {
      key: 'operate',
      title: $t('common.operate'),
      width: 100,
      align: 'center',
      fixed: 'right',
      render: row => (
        <NButton size="small" type="primary" ghost onClick={() => openDetail(row.id)}>
          {$t('common.edit')}
        </NButton>
      )
    }
  ]
});

// New drafts start with the address, type, title and source language; everything else is in the editor.
const creating = ref(false);
const saving = ref(false);
const formRef = ref<FormInst | null>(null);
const draft = reactive({ slug: '', type: 'tutorial' as ArticleType, title: '', sourceLocale: DEFAULT_SOURCE_LOCALE });
const rules = computed<FormRules>(() => ({
  slug: [
    { required: true, message: $t('form.required'), trigger: ['blur', 'input'] },
    { pattern: ARTICLE_SLUG, message: text('slugInvalid'), trigger: ['blur', 'input'] }
  ],
  title: [{ required: true, message: $t('form.required'), trigger: ['blur', 'input'] }]
}));

async function create() {
  await formRef.value?.validate();
  saving.value = true;
  const { data: created, error } = await createArticle({
    slug: draft.slug.trim(),
    type: draft.type,
    title: draft.title.trim(),
    sourceLocale: draft.sourceLocale
  });
  saving.value = false;
  if (error || isDryRun(created)) return;
  creating.value = false;
  Object.assign(draft, { slug: '', title: '' });
  openDetail(created.id);
}
</script>

<template>
  <div class="min-h-500px flex-col-stretch gap-16px overflow-hidden lt-sm:overflow-auto">
    <NCard :title="text('title')" :bordered="false" size="small" class="card-wrapper sm:flex-1-hidden">
      <template #header-extra>
        <div class="flex flex-wrap items-center gap-8px">
          <NInput
            v-model:value="filters.q"
            :placeholder="text('keyword')"
            clearable
            size="small"
            class="w-200px"
            @keyup.enter="getDataByPage(1)"
            @clear="getDataByPage(1)"
          />
          <NSelect
            v-model:value="filters.type"
            :options="typeOptions"
            :placeholder="text('columns.type')"
            clearable
            size="small"
            class="w-130px"
            @update:value="getDataByPage(1)"
          />
          <NSelect
            v-model:value="filters.status"
            :options="statusOptions"
            :placeholder="text('columns.status')"
            clearable
            size="small"
            class="w-130px"
            @update:value="getDataByPage(1)"
          />
          <NButton size="small" type="primary" @click="creating = true">{{ text('create') }}</NButton>
          <NButton size="small" :loading="loading" @click="getData">{{ $t('common.refresh') }}</NButton>
        </div>
      </template>
      <NDataTable
        :columns="columns"
        :data="data"
        size="small"
        :flex-height="!appStore.isMobile"
        :scroll-x="1100"
        :loading="loading"
        remote
        :row-key="row => row.id"
        :pagination="mobilePagination"
        class="sm:h-full"
      />
    </NCard>
    <NModal v-model:show="creating" preset="card" :title="text('create')" class="max-w-520px">
      <NForm ref="formRef" :model="draft" :rules="rules" label-placement="top">
        <NFormItem :label="text('fields.title')" path="title">
          <NInput v-model:value="draft.title" :maxlength="200" :input-props="{ 'aria-label': text('fields.title') }" />
        </NFormItem>
        <NFormItem :label="text('fields.slug')" path="slug">
          <NInput
            v-model:value="draft.slug"
            :maxlength="100"
            class="font-mono"
            :placeholder="text('slugHint')"
            :input-props="{ 'aria-label': text('fields.slug') }"
          />
        </NFormItem>
        <div class="grid grid-cols-2 gap-x-16px">
          <NFormItem :label="text('columns.type')">
            <NSelect v-model:value="draft.type" :options="typeOptions" />
          </NFormItem>
          <NFormItem :label="$t('page.shared.i18n.sourceLocale')">
            <NSelect v-model:value="draft.sourceLocale" :options="CONTENT_LOCALE_OPTIONS" />
          </NFormItem>
        </div>
        <NButton type="primary" :loading="saving" @click="create">{{ text('createSubmit') }}</NButton>
      </NForm>
    </NModal>
  </div>
</template>
