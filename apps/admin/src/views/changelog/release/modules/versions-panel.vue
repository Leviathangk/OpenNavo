<script setup lang="tsx">
import { computed, reactive, ref } from 'vue';
import { NButton, NTag, NText } from 'naive-ui';
import { fetchAdminVersions, upsertReleaseNotes } from '@/service/api';
import type { QueryOf, RecordOf } from '@/typings/api/opennavo';
import { useAppStore } from '@/store/modules/app';
import { useAuth } from '@/hooks/business/auth';
import { useRouterPush } from '@/hooks/common/router';
import { defaultTransform, useNaivePaginatedTable } from '@/hooks/common/table';
import PackagePicker from '@/components/opennavo/package-picker.vue';
import ReleaseEditor from '@/components/opennavo/release-editor.vue';
import { booleanOptions, compactParams, formatDateTime, toBoolean } from '@/utils/opennavo';
import { $t } from '@/locales';
import NotesEditor from './notes-editor.vue';
import { notesVersion, ownsNotes } from './notes';

defineOptions({ name: 'ChangelogVersionsPanel' });

type Row = RecordOf<'listAdminVersions'>;

// Cataloged versions (07 §10.3): all versions, including those without notes; write/edit/clear our editorial notes.
// A build (0.1,49) gets notes of its own; until then it shows its base version's shared note, tagged as shared.
// Existing notes open the translation drawer; clearing removes only editorial notes, recoverable through recycle bin/revisions.
const appStore = useAppStore();
const { hasAuth } = useAuth();
const { routerPushByKey } = useRouterPush();
const canWrite = computed(() => hasAuth('changelog:release:edit'));
const filters = reactive({
  packageId: null as number | null,
  packageName: '',
  q: '',
  hasEditorial: null as string | null,
  sort: 'updated' as NonNullable<QueryOf<'listAdminVersions'>['sort']>
});
const page = reactive({ current: 1, size: 20 });
const editing = ref<Row | null>(null);
const editorOpen = ref(false);
const translationId = ref<number | null>(null);
const translationOpen = ref(false);
const sortOptions = computed(() =>
  (['updated', 'popular'] as const).map(value => ({ value, label: $t(`page.changelog.notes.sort.${value}`) }))
);

const { columns, data, loading, getData, getDataByPage, mobilePagination } = useNaivePaginatedTable({
  api: () =>
    fetchAdminVersions(
      compactParams({
        packageId: filters.packageId,
        q: filters.q.trim() || null,
        hasEditorial: toBoolean(filters.hasEditorial),
        sort: filters.sort,
        current: page.current,
        size: page.size
      }) as QueryOf<'listAdminVersions'>
    ),
  transform: response => defaultTransform(response),
  onPaginationParamsChange: params => {
    page.current = params.page ?? 1;
    page.size = params.pageSize ?? 20;
  },
  columns: () => [
    {
      key: 'token',
      title: $t('page.changelog.notes.columns.app'),
      minWidth: 180,
      render: row => (
        <NButton text type="primary" onClick={() => routerPushByKey('catalog_package-detail', { params: { id: String(row.packageId) } })}>
          {row.token}
        </NButton>
      )
    },
    { key: 'version', title: $t('page.changelog.notes.columns.version'), width: 160, render: row => <span class="font-mono">{row.version}</span> },
    { key: 'rank30d', title: $t('page.changelog.notes.columns.rank'), width: 80, align: 'right', render: row => (row.rank30d ? `#${row.rank30d}` : '—') },
    { key: 'brewCommittedAt', title: $t('page.changelog.notes.columns.committed'), width: 170, render: row => formatDateTime(row.brewCommittedAt) },
    { key: 'firstSeenAt', title: $t('page.changelog.notes.columns.firstSeen'), width: 170, render: row => formatDateTime(row.firstSeenAt) },
    {
      key: 'notes',
      title: $t('page.changelog.notes.columns.notes'),
      width: 180,
      render: row => (
        <div class="flex flex-wrap gap-4px">
          <NTag size="small" bordered={false} type={ownsNotes(row) ? 'success' : row.hasEditorial ? 'info' : 'default'}>
            {ownsNotes(row)
              ? $t('page.changelog.notes.written')
              : row.hasEditorial
                ? $t('page.changelog.notes.shared', { version: row.versionBase })
                : $t('page.changelog.notes.notWritten')}
          </NTag>
          {row.historicalReleaseId && (
            <NTag size="small" bordered={false}>
              {$t('page.changelog.notes.historical')}
            </NTag>
          )}
        </div>
      )
    },
    {
      key: 'operate',
      title: $t('common.operate'),
      width: 320,
      align: 'center',
      fixed: 'right',
      render: row => (
        <div class="flex flex-wrap items-center justify-center gap-6px">
          {canWrite.value && (
            <NButton size="small" type="primary" ghost onClick={() => openEditor(row)}>
              {ownsNotes(row) ? $t('page.changelog.notes.edit') : $t('page.changelog.notes.write')}
            </NButton>
          )}
          {row.editorialReleaseId && (
            <NButton size="small" onClick={() => openTranslations(row.editorialReleaseId!)}>
              {$t('page.changelog.notes.translations')}
            </NButton>
          )}
          {canWrite.value && ownsNotes(row) && (
            <NButton size="small" type="error" quaternary onClick={() => clear(row)}>
              {$t('page.changelog.notes.clear')}
            </NButton>
          )}
        </div>
      )
    }
  ]
});

function openEditor(row: Row) {
  editing.value = row;
  editorOpen.value = true;
}

function openTranslations(id: number) {
  translationId.value = id;
  translationOpen.value = true;
}

function clear(row: Row) {
  window.$dialog?.warning({
    title: $t('page.changelog.notes.clear'),
    content: $t('page.changelog.notes.clearConfirm', { token: row.token, version: notesVersion(row) }),
    positiveText: $t('common.confirm'),
    negativeText: $t('common.cancel'),
    onPositiveClick: async () => {
      const { error } = await upsertReleaseNotes(row.packageId, notesVersion(row), { clear: true });
      if (error) return;
      window.$message?.success($t('page.changelog.notes.cleared'));
      await getData();
    }
  });
}

function pickPackage(item: { id: number; token: string }) {
  filters.packageId = item.id;
  filters.packageName = item.token;
  void getDataByPage(1);
}

function clearPackage() {
  filters.packageId = null;
  filters.packageName = '';
  void getDataByPage(1);
}

function reset() {
  Object.assign(filters, { packageId: null, packageName: '', q: '', hasEditorial: null, sort: 'updated' });
  void getDataByPage(1);
}
</script>

<template>
  <div class="min-h-500px flex-col-stretch gap-16px overflow-hidden lt-sm:overflow-auto">
    <NCard :bordered="false" size="small" class="card-wrapper">
      <NForm label-placement="left" :label-width="80" :show-feedback="false">
        <NGrid responsive="screen" item-responsive :x-gap="16" :y-gap="12">
          <NFormItemGi span="24 s:12 m:6" :label="$t('page.changelog.release.package')">
            <NTag v-if="filters.packageId" closable :bordered="false" type="primary" @close="clearPackage">
              {{ filters.packageName }}
            </NTag>
            <PackagePicker v-else :placeholder="$t('page.changelog.release.packagePlaceholder')" class="w-full" @pick="pickPackage" />
          </NFormItemGi>
          <NFormItemGi span="24 s:12 m:6" :label="$t('page.changelog.release.keyword')">
            <NInput v-model:value="filters.q" :placeholder="$t('page.changelog.notes.keywordPlaceholder')" clearable @keyup.enter="getDataByPage(1)" />
          </NFormItemGi>
          <NFormItemGi span="24 s:12 m:4" :label="$t('page.changelog.notes.columns.notes')">
            <NSelect
              v-model:value="filters.hasEditorial"
              :options="booleanOptions($t('page.changelog.notes.written'), $t('page.changelog.notes.notWritten'))"
              :placeholder="$t('page.shared.any')"
              clearable
            />
          </NFormItemGi>
          <NFormItemGi span="24 s:12 m:4" :label="$t('page.changelog.notes.sortLabel')">
            <NSelect v-model:value="filters.sort" :options="sortOptions" />
          </NFormItemGi>
          <NGi span="24 m:4" class="flex items-center justify-end gap-12px">
            <NButton @click="reset">{{ $t('common.reset') }}</NButton>
            <NButton type="primary" ghost @click="getDataByPage(1)">{{ $t('common.search') }}</NButton>
          </NGi>
        </NGrid>
      </NForm>
    </NCard>
    <NCard :title="$t('page.changelog.notes.title')" :bordered="false" size="small" class="card-wrapper sm:flex-1-hidden">
      <template #header-extra>
        <div class="flex items-center gap-12px">
          <NText depth="3" class="text-12px lt-md:hidden">{{ $t('page.changelog.notes.hint') }}</NText>
          <NButton size="small" :loading="loading" @click="getData">{{ $t('common.refresh') }}</NButton>
        </div>
      </template>
      <NDataTable
        :columns="columns"
        :data="data"
        size="small"
        :flex-height="!appStore.isMobile"
        :scroll-x="1380"
        :loading="loading"
        remote
        :row-key="row => row.packageVersionId"
        :pagination="mobilePagination"
        class="sm:h-full"
      />
    </NCard>
    <NotesEditor v-model:show="editorOpen" :version="editing" @saved="getData" />
    <ReleaseEditor v-model:show="translationOpen" :release-id="translationId" @saved="getData" />
  </div>
</template>
