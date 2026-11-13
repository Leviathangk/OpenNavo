<script setup lang="tsx">
import { computed, onMounted, ref } from 'vue';
import { NButton, NDataTable, NInput, NInputNumber, NSwitch, NTag, useMessage } from 'naive-ui';
import type { DataTableColumns } from 'naive-ui';
import { fetchDeveloperPicks, setDeveloperPick, type DeveloperPickRow } from '@/service/api/developer';
import { $t } from '@/locales';
import { formatDateTime } from '@/utils/opennavo';

defineOptions({ name: 'DeveloperPicks' });

const message = useMessage();
const rows = ref<DeveloperPickRow[]>([]);
const loading = ref(false);
const savingId = ref<number | null>(null);

async function load() {
  loading.value = true;
  const { data, error } = await fetchDeveloperPicks();
  if (error) message.error($t('page.developerPicks.loadFailed'));
  else rows.value = data ?? [];
  loading.value = false;
}

async function save(row: DeveloperPickRow) {
  savingId.value = row.packageId;
  const { error } = await setDeveloperPick({
    packageKind: row.kind,
    packageToken: row.token,
    quote: row.quote,
    sort: row.sort,
    active: row.active
  });
  if (error) message.error($t('page.developerPicks.saveFailed'));
  else message.success($t('page.developerPicks.saved'));
  savingId.value = null;
}

const columns = computed<DataTableColumns<DeveloperPickRow>>(() => [
  {
    key: 'name',
    title: $t('page.developerPicks.app'),
    minWidth: 220,
    render: row => (
      <div class="flex items-center gap-8px">
        <span class="grid h-26px w-26px place-items-center overflow-hidden rounded-6px bg-gray-100 dark:bg-gray-800">
          {row.iconUrl ? <img src={row.iconUrl} class="h-full w-full object-cover" /> : row.name.slice(0, 1)}
        </span>
        <span class="min-w-0">
          <b class="block truncate text-12px">{row.name}</b>
          <span class="block truncate text-11px text-gray-500">{row.kind}/{row.token}</span>
        </span>
      </div>
    )
  },
  {
    key: 'quote',
    title: $t('page.developerPicks.quote'),
    minWidth: 260,
    render: row => <NInput v-model:value={row.quote} size="small" />
  },
  {
    key: 'sort',
    title: $t('page.developerPicks.sort'),
    width: 110,
    render: row => <NInputNumber v-model:value={row.sort} size="small" class="w-full" />
  },
  {
    key: 'active',
    title: $t('page.developerPicks.active'),
    width: 100,
    render: row => <NSwitch v-model:value={row.active} size="small" />
  },
  {
    key: 'publishedAt',
    title: $t('page.developerPicks.publishedAt'),
    width: 170,
    render: row => formatDateTime(row.publishedAt)
  },
  {
    key: 'actions',
    title: $t('common.action'),
    width: 90,
    render: row => (
      <NButton size="small" type="primary" loading={savingId.value === row.packageId} onClick={() => save(row)}>
        {$t('page.developerPicks.save')}
      </NButton>
    )
  }
]);

onMounted(load);
</script>

<template>
  <div class="flex flex-col gap-16px">
    <div class="flex flex-wrap items-center justify-between gap-10px">
      <div>
        <h2 class="m-0 text-18px font-600">{{ $t('page.developerPicks.title') }}</h2>
        <p class="m-0 mt-4px text-12px text-gray-500">{{ $t('page.developerPicks.hint') }}</p>
      </div>
      <div class="flex items-center gap-8px">
        <NTag v-if="rows.length" size="small">{{ rows.filter(row => row.active).length }} / {{ rows.length }}</NTag>
        <NButton @click="load">{{ $t('common.refresh') }}</NButton>
      </div>
    </div>
    <NDataTable :columns="columns" :data="rows" :loading="loading" :row-key="row => row.packageId" />
  </div>
</template>
