<script setup lang="tsx">
import { computed, reactive, ref } from 'vue';
import { NButton, NDropdown, NTag, NText } from 'naive-ui';
import { fetchReleaseList, retranslateRelease, updateRelease } from '@/service/api';
import type { QueryOf, RecordOf, Schemas } from '@/typings/api/opennavo';
import { useAppStore } from '@/store/modules/app';
import { useAuth } from '@/hooks/business/auth';
import { defaultTransform, useNaivePaginatedTable } from '@/hooks/common/table';
import PackagePicker from '@/components/opennavo/package-picker.vue';
import ReleaseEditor from '@/components/opennavo/release-editor.vue';
import { useLocalizedPackage } from '@/hooks/business/localized';
import { booleanOptions, compactParams, formatDateTime, toBoolean } from '@/utils/opennavo';
import { TRANSLATION_STATUS_TAG } from '@/utils/content-locale';
import { $t } from '@/locales';
import VersionsPanel from './modules/versions-panel.vue';

defineOptions({ name: 'ChangelogRelease' });

type Row = RecordOf<'listAdminReleases'>;
type TranslationStatus = Schemas['ReleaseTranslationStatus'];

// Release list (07 §7.7): package, keyword, source, translation, hidden filters; translation drawer and retranslation (translation:review),
// and hide/show (changelog:release:edit).
// No translation review (12): source, machine, manual, queued, failed, plus no-text/skipped states.
const STATUSES: TranslationStatus[] = ['none', 'source', 'machine', 'manual', 'pending', 'failed', 'skipped'];
// editorial means our authored release notes (11 §3.2), not a scraped upstream source.
const SOURCES = ['editorial', 'github_release', 'sparkle', 'webpage', 'manual'] as const;

const appStore = useAppStore();
const { hasAuth } = useAuth();
const { nameOf } = useLocalizedPackage();
const filters = reactive({
  packageId: null as number | null,
  packageName: '',
  q: '',
  source: null as Schemas['ReleaseSource'] | null,
  translationStatus: null as TranslationStatus | null,
  hidden: null as string | null
});
// Open Cataloged versions by default because authoring notes is the main workflow; the original notes list is the second tab.
const view = ref<'versions' | 'releases'>('versions');
const page = reactive({ current: 1, size: 20 });
const editorOpen = ref(false);
const editingId = ref<number | null>(null);

const sourceOptions = computed(() =>
  SOURCES.map(value => ({ value, label: $t(`page.changelog.release.sources.${value}`) }))
);
const statusOptions = computed(() =>
  STATUSES.map(value => ({ value, label: $t(`page.changelog.release.translationStatuses.${value}`) }))
);

function query() {
  return compactParams({
    current: page.current,
    size: page.size,
    packageId: filters.packageId,
    q: filters.q.trim(),
    source: filters.source,
    translationStatus: filters.translationStatus,
    hidden: toBoolean(filters.hidden)
  }) as QueryOf<'listAdminReleases'>;
}

function openEditor(row: Row) {
  editingId.value = row.id;
  editorOpen.value = true;
}

const { columns, data, loading, getData, getDataByPage, mobilePagination } = useNaivePaginatedTable({
  api: () => fetchReleaseList(query()),
  transform: response => defaultTransform(response),
  paginationProps: { pageSizes: [20, 50, 100] },
  onPaginationParamsChange: params => {
    page.current = params.page ?? 1;
    page.size = params.pageSize ?? 20;
  },
  columns: () => [
    {
      key: 'package',
      title: $t('page.changelog.release.columns.package'),
      minWidth: 180,
      render: row => (
        <div class="flex-col">
          <span class="font-600">{row.packageName}</span>
          <NText depth={3} class="font-mono text-12px">
            {row.token}
          </NText>
        </div>
      )
    },
    {
      key: 'version',
      title: $t('page.changelog.release.columns.version'),
      width: 130,
      render: row => (
        <div class="flex items-center gap-6px">
          <span class="font-mono">{row.buildVersion ?? row.version}</span>
          {row.hidden && (
            <NTag size="small" bordered={false}>
              {$t('page.changelog.release.hidden')}
            </NTag>
          )}
        </div>
      )
    },
    {
      key: 'title',
      title: $t('page.changelog.release.columns.title'),
      minWidth: 180,
      ellipsis: { tooltip: true },
      render: row => row.title ?? '—'
    },
    {
      key: 'publishedAt',
      title: $t('page.changelog.release.columns.publishedAt'),
      width: 170,
      render: row => formatDateTime(row.publishedAt)
    },
    {
      key: 'source',
      title: $t('page.changelog.release.columns.source'),
      width: 150,
      render: row => {
        const tag = (
          <NTag size="small" bordered={false}>
            {$t(`page.changelog.release.sources.${row.source}`)}
          </NTag>
        );
        return row.sourceUrl ? (
          <a href={row.sourceUrl} target="_blank" rel="noopener noreferrer" class="inline-flex items-center gap-4px">
            {tag}
            <span class="text-primary">↗</span>
          </a>
        ) : (
          tag
        );
      }
    },
    {
      key: 'translationStatus',
      title: $t('page.changelog.release.columns.translation'),
      width: 100,
      render: row => (
        <NTag size="small" bordered={false} type={TRANSLATION_STATUS_TAG[row.translationStatus]}>
          {$t(`page.changelog.release.translationStatuses.${row.translationStatus}`)}
        </NTag>
      )
    },
    {
      key: 'fetchedAt',
      title: $t('page.changelog.release.columns.fetchedAt'),
      width: 170,
      render: row => formatDateTime(row.fetchedAt)
    },
    {
      key: 'operate',
      title: $t('common.operate'),
      width: 190,
      align: 'center',
      fixed: 'right',
      render: row => {
        const options = [
          ...(hasAuth('translation:review') ? [{ key: 'retranslate', label: $t('page.changelog.release.retranslate') }] : []),
          ...(hasAuth('changelog:release:edit')
            ? [{ key: row.hidden ? 'show' : 'hide', label: row.hidden ? $t('page.changelog.release.show') : $t('page.changelog.release.hide') }]
            : [])
        ];
        return (
          <div class="flex-center gap-8px">
            <NButton size="small" type="primary" ghost onClick={() => openEditor(row)}>
              {$t('page.changelog.release.editTranslation')}
            </NButton>
            {options.length > 0 && (
              <NDropdown trigger="click" options={options} onSelect={(key: string) => runMore(key, row)}>
                <NButton size="small">{$t('page.changelog.release.more')}</NButton>
              </NDropdown>
            )}
          </div>
        );
      }
    }
  ]
});

function runMore(key: string, row: Row) {
  if (key === 'retranslate') {
    window.$dialog?.warning({
      title: $t('page.changelog.release.retranslate'),
      content: $t('page.changelog.release.retranslateConfirm', { name: row.packageName, version: row.version }),
      positiveText: $t('common.confirm'),
      negativeText: $t('common.cancel'),
      onPositiveClick: async () => {
        const { error } = await retranslateRelease(row.id);
        if (error) return;
        window.$message?.success($t('page.changelog.release.retranslateQueued'));
        await getData();
      }
    });
    return;
  }
  void (async () => {
    const hide = key === 'hide';
    const { error } = await updateRelease(row.id, { hidden: hide });
    if (error) return;
    window.$message?.success(hide ? $t('page.changelog.release.hiddenDone') : $t('page.changelog.release.shownDone'));
    await getData();
  })();
}

function pickPackage(row: RecordOf<'listAdminPackages'>) {
  filters.packageId = row.id;
  filters.packageName = nameOf(row);
  void getDataByPage(1);
}

function clearPackage() {
  filters.packageId = null;
  filters.packageName = '';
  void getDataByPage(1);
}

function reset() {
  Object.assign(filters, { packageId: null, packageName: '', q: '', source: null, translationStatus: null, hidden: null });
  void getDataByPage(1);
}
</script>

<template>
  <div class="min-h-500px flex-col-stretch gap-16px overflow-hidden lt-sm:overflow-auto">
    <NRadioGroup v-model:value="view" class="self-start">
      <NRadioButton value="versions">{{ $t('page.changelog.notes.tabVersions') }}</NRadioButton>
      <NRadioButton value="releases">{{ $t('page.changelog.notes.tabReleases') }}</NRadioButton>
    </NRadioGroup>
    <VersionsPanel v-if="view === 'versions'" class="flex-1-hidden" />
    <NCard v-if="view === 'releases'" :bordered="false" size="small" class="card-wrapper">
      <NForm label-placement="left" :label-width="80" :show-feedback="false">
        <NGrid responsive="screen" item-responsive :x-gap="16" :y-gap="12">
          <NFormItemGi span="24 s:12 m:8" :label="$t('page.changelog.release.package')">
            <NTag v-if="filters.packageId" closable :bordered="false" type="primary" @close="clearPackage">
              {{ filters.packageName }}
            </NTag>
            <PackagePicker
              v-else
              :placeholder="$t('page.changelog.release.packagePlaceholder')"
              class="w-full"
              @pick="pickPackage"
            />
          </NFormItemGi>
          <NFormItemGi span="24 s:12 m:8" :label="$t('page.changelog.release.keyword')">
            <NInput
              v-model:value="filters.q"
              :placeholder="$t('page.changelog.release.keywordPlaceholder')"
              clearable
              @keyup.enter="getDataByPage(1)"
            />
          </NFormItemGi>
          <NFormItemGi span="24 s:12 m:8" :label="$t('page.changelog.release.source')">
            <NSelect
              v-model:value="filters.source"
              :options="sourceOptions"
              :placeholder="$t('page.shared.any')"
              clearable
            />
          </NFormItemGi>
          <NFormItemGi span="24 s:12 m:8" :label="$t('page.changelog.release.translationStatus')">
            <NSelect
              v-model:value="filters.translationStatus"
              :options="statusOptions"
              :placeholder="$t('page.shared.any')"
              clearable
            />
          </NFormItemGi>
          <NFormItemGi span="24 s:12 m:8" :label="$t('page.changelog.release.hidden')">
            <NSelect
              v-model:value="filters.hidden"
              :options="booleanOptions($t('page.shared.yes'), $t('page.shared.no'))"
              :placeholder="$t('page.shared.any')"
              clearable
            />
          </NFormItemGi>
          <NGi span="24 m:8" class="flex items-center justify-end gap-12px">
            <NButton @click="reset">{{ $t('common.reset') }}</NButton>
            <NButton type="primary" ghost @click="getDataByPage(1)">{{ $t('common.search') }}</NButton>
          </NGi>
        </NGrid>
      </NForm>
    </NCard>
    <NCard
      v-if="view === 'releases'"
      :title="$t('page.changelog.release.title')"
      :bordered="false"
      size="small"
      class="card-wrapper sm:flex-1-hidden"
    >
      <template #header-extra>
        <NButton size="small" :loading="loading" @click="getData">{{ $t('common.refresh') }}</NButton>
      </template>
      <NDataTable
        :columns="columns"
        :data="data"
        size="small"
        :flex-height="!appStore.isMobile"
        :scroll-x="1400"
        :loading="loading"
        remote
        :row-key="row => row.id"
        :pagination="mobilePagination"
        class="sm:h-full"
      />
    </NCard>
    <ReleaseEditor v-model:show="editorOpen" :release-id="editingId" @saved="getData" />
  </div>
</template>
