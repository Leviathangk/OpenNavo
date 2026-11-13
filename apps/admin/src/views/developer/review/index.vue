<script setup lang="tsx">
import { computed, onMounted, reactive, ref } from 'vue';
import { NAlert, NButton, NDataTable, NDrawer, NDrawerContent, NForm, NFormItem, NInput, NInputNumber, NSelect, NSwitch, NTag, useMessage } from 'naive-ui';
import type { DataTableColumns } from 'naive-ui';
import {
  fetchDeveloperPicks,
  fetchDeveloperSubmissions,
  updateDeveloperSubmission,
  type DeveloperPickRow,
  type DeveloperSubmission,
  type DeveloperSubmissionPatch
} from '@/service/api/developer';
import { $t } from '@/locales';
import { formatDateTime } from '@/utils/opennavo';

defineOptions({ name: 'DeveloperReview' });

const message = useMessage();
const rows = ref<DeveloperSubmission[]>([]);
const loading = ref(false);
const statusFilter = ref<string | null>(null);
const searchText = ref('');
const selected = ref<DeveloperSubmission | null>(null);
const picks = ref<DeveloperPickRow[]>([]);
const open = ref(false);
const saving = ref(false);
const saveError = ref('');
const payloadText = ref('');
const form = reactive({
  status: 'submitted' as DeveloperSubmission['status'],
  packageKind: 'cask',
  packageToken: '',
  feature: false,
  quote: '',
  sort: 0,
  decisionNote: ''
});

const STATUSES = ['submitted', 'in_review', 'changes_requested', 'approved', 'rejected', 'published'] as const;
const statusOptions = computed(() => STATUSES.map(value => ({ value, label: $t(`page.developerReview.statuses.${value}`) })));
const statusType = (status: string) =>
  status === 'published' || status === 'approved' ? 'success' : status === 'rejected' ? 'error' : status === 'in_review' ? 'info' : 'default';

async function load() {
  loading.value = true;
  const [submissions, pickRows] = await Promise.all([
    fetchDeveloperSubmissions({ status: statusFilter.value || undefined, q: searchText.value.trim() || undefined, limit: 100 }),
    fetchDeveloperPicks()
  ]);
  if (submissions.error) {
    message.error($t('page.developerReview.loadFailed'));
  } else {
    rows.value = submissions.data ?? [];
  }
  // The drawer shows the current pick, so saving an already-published submission
  // keeps its quote and order instead of clearing them.
  picks.value = pickRows.error ? [] : (pickRows.data ?? []);
  loading.value = false;
}

// Only a pick this submission owns belongs in the drawer. A pick curated on the picks
// page (no submission) is not this submission's to republish: prefilling it would
// hand it over on the next publish and let a later rejection switch it off.
function pickFor(row: DeveloperSubmission) {
  return picks.value.find(pick => pick.submissionId === row.id);
}

function openRow(row: DeveloperSubmission) {
  const pick = pickFor(row);
  selected.value = row;
  payloadText.value = JSON.stringify(row.payload ?? {}, null, 2);
  form.status = row.status;
  form.packageKind = 'cask';
  form.packageToken = '';
  form.feature = pick?.active ?? false;
  form.quote = pick?.quote ?? '';
  form.sort = pick?.sort ?? 0;
  form.decisionNote = row.decisionNote ?? '';
  saveError.value = '';
  open.value = true;
}

async function save(publish = false) {
  if (!selected.value) return;
  let payload: Record<string, unknown>;
  try {
    payload = JSON.parse(payloadText.value || '{}');
  } catch {
    message.error($t('page.developerReview.invalidJson'));
    return;
  }
  const body: DeveloperSubmissionPatch = {
    status: publish ? 'published' : form.status,
    decisionNote: form.decisionNote || null,
    payload,
    feature: form.feature,
    quote: form.quote,
    sort: form.sort
  };
  if (form.packageToken.trim()) {
    body.packageKind = form.packageKind;
    body.packageToken = form.packageToken.trim();
  }
  saving.value = true;
  saveError.value = '';
  const { error } = await updateDeveloperSubmission(selected.value.id, body);
  if (error) {
    // Keep the drawer open and show why the review was rejected: a missing
    // category, an unknown city or a non-HTTPS link is fixable right here.
    saveError.value = error.response?.data?.msg || error.message;
    if (!saveError.value) message.error($t('page.developerReview.saveFailed'));
  } else {
    message.success($t('page.developerReview.saved'));
    open.value = false;
    await load();
  }
  saving.value = false;
}

const columns = computed<DataTableColumns<DeveloperSubmission>>(() => [
  { key: 'id', title: 'ID', width: 70 },
  { key: 'kind', title: $t('page.developerReview.kind'), width: 90 },
  {
    key: 'status',
    title: $t('page.developerReview.status'),
    width: 130,
    render: row => <NTag type={statusType(row.status)} size="small">{row.status}</NTag>
  },
  { key: 'contactEmail', title: $t('page.developerReview.contact'), minWidth: 180 },
  { key: 'packageId', title: 'Package', width: 90, render: row => row.packageId ?? '—' },
  { key: 'createdAt', title: $t('page.developerReview.created'), width: 170, render: row => formatDateTime(row.createdAt) },
  {
    key: 'actions',
    title: $t('common.action'),
    width: 100,
    render: row => (
      <NButton size="small" onClick={() => openRow(row)}>
        {$t('page.developerReview.review')}
      </NButton>
    )
  }
]);

onMounted(load);
</script>

<template>
  <div>
    <div class="flex flex-col gap-16px">
      <div class="flex flex-wrap items-center justify-between gap-10px">
        <h2 class="m-0 text-18px font-600">{{ $t('page.developerReview.title') }}</h2>
        <div class="flex items-center gap-8px">
          <NInput v-model:value="searchText" clearable :placeholder="$t('page.developerReview.searchPlaceholder')" class="w-240px" @keyup.enter="load" />
          <NButton @click="load">{{ $t('common.search') }}</NButton>
          <NSelect v-model:value="statusFilter" clearable :options="statusOptions" :placeholder="$t('page.developerReview.allStatuses')" class="w-180px" @update:value="load" />
          <NButton @click="load">{{ $t('common.refresh') }}</NButton>
        </div>
      </div>
      <NDataTable :columns="columns" :data="rows" :loading="loading" :row-key="row => row.id" />
    </div>

    <NDrawer v-model:show="open" :width="560" placement="right">
      <NDrawerContent v-if="selected" :title="`${$t('page.developerReview.review')} #${selected.id}`" closable>
        <div class="flex flex-col gap-12px">
          <NAlert v-if="saveError" type="error" :bordered="false" closable @close="saveError = ''">{{ saveError }}</NAlert>
          <div class="rounded-8px bg-gray-50 p-10px text-12px leading-1.7 dark:bg-gray-900">
            <div><b>{{ $t('page.developerReview.kindLabel') }}:</b> {{ selected.kind }} · <b>{{ $t('page.developerReview.contact') }}:</b> {{ selected.contactEmail }}</div>
            <div v-if="selected.sourceUrl"><b>{{ $t('page.developerReview.sourceLabel') }}:</b> {{ selected.sourceUrl }}</div>
            <div v-if="selected.proof"><b>{{ $t('page.developerReview.proofLabel') }}:</b> {{ selected.proof }}</div>
            <div v-if="selected.publicToken" class="break-all"><b>{{ $t('page.developerReview.statusToken') }}:</b> {{ selected.publicToken }}</div>
          </div>
          <NForm label-placement="top">
            <NFormItem :label="$t('page.developerReview.status')">
              <NSelect v-model:value="form.status" :options="statusOptions" />
            </NFormItem>
            <div class="grid grid-cols-2 gap-8px">
              <NFormItem :label="$t('page.developerReview.packageKind')"><NSelect v-model:value="form.packageKind" :options="[{ label: 'cask', value: 'cask' }, { label: 'link', value: 'link' }]" /></NFormItem>
              <NFormItem :label="$t('page.developerReview.packageToken')"><NInput v-model:value="form.packageToken" :placeholder="$t('page.developerReview.packageTokenPlaceholder')" /></NFormItem>
            </div>
            <NFormItem :label="$t('page.developerReview.payload')">
              <NInput v-model:value="payloadText" type="textarea" :autosize="{ minRows: 8, maxRows: 18 }" class="font-mono" />
            </NFormItem>
            <NFormItem :label="$t('page.developerReview.decisionNote')">
              <NInput v-model:value="form.decisionNote" type="textarea" :rows="2" />
            </NFormItem>
            <div class="grid grid-cols-2 gap-8px">
              <NFormItem :label="$t('page.developerReview.feature')"><NSwitch v-model:value="form.feature" /></NFormItem>
              <NFormItem :label="$t('page.developerReview.sort')"><NInputNumber v-model:value="form.sort" class="w-full" /></NFormItem>
            </div>
            <NFormItem :label="$t('page.developerReview.quote')"><NInput v-model:value="form.quote" type="textarea" :rows="2" /></NFormItem>
          </NForm>
        </div>
        <template #footer>
          <div class="flex justify-end gap-8px">
            <NButton :loading="saving" @click="save(false)">{{ $t('page.developerReview.save') }}</NButton>
            <NButton type="primary" :loading="saving" @click="save(true)">{{ $t('page.developerReview.publish') }}</NButton>
          </div>
        </template>
      </NDrawerContent>
    </NDrawer>
  </div>
</template>
