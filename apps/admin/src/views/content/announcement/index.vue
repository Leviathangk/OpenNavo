<script setup lang="ts">
import { computed, reactive, ref } from 'vue';
import { fetchAnnouncement, updateAnnouncement } from '@/service/api';
import type { DataOf, Schemas } from '@/typings/api/opennavo';
import { useAuth } from '@/hooks/business/auth';
import {
  CONTENT_LOCALES,
  SOURCE_LOCALE_OPTIONS,
  DEFAULT_SOURCE_LOCALE,
  TRANSLATION_STATUS_TAG,
  localeName
} from '@/utils/content-locale';
import type { ContentLocale } from '@/utils/content-locale';
import { formatDateTime } from '@/utils/opennavo';
import { $t } from '@/locales';

defineOptions({ name: 'ContentAnnouncement' });

// Content → Announcements (formerly desktop.announcement remote configuration), displayed atop the desktop app.
// Write source text only; other languages translate automatically, with statuses shown below.
const { hasAuth } = useAuth();
const editable = computed(() => hasAuth('content:announcement:edit'));

const view = ref<DataOf<'getAnnouncement'> | null>(null);
const model = reactive<Schemas['AnnouncementUpdate']>({
  enabled: false,
  sourceLocale: DEFAULT_SOURCE_LOCALE,
  title: '',
  body: ''
});
const saving = ref(false);

function fill(data: DataOf<'getAnnouncement'>) {
  view.value = data;
  const source = data.i18n[data.sourceLocale]?.fields ?? {};
  Object.assign(model, {
    enabled: data.enabled,
    sourceLocale: data.sourceLocale,
    title: source.title ?? '',
    body: source.body ?? ''
  });
}

async function load() {
  const { data, error } = await fetchAnnouncement();
  if (!error) fill(data);
}
void load();

const rows = computed(() =>
  view.value
    ? CONTENT_LOCALES.flatMap(code => {
        const state = view.value!.i18n[code];
        return state ? [{ code, state }] : [];
      })
    : ([] as { code: ContentLocale; state: Schemas['ContentLocaleState'] }[])
);

async function save() {
  if (model.enabled && (!model.title.trim() || !model.body.trim())) {
    window.$message?.warning($t('page.content.announcement.required'));
    return;
  }
  saving.value = true;
  const { error } = await updateAnnouncement({ ...model, title: model.title.trim(), body: model.body.trim() });
  saving.value = false;
  if (error) return;
  window.$message?.success($t('page.content.announcement.saved'));
  await load();
}

const statusType = (status: Schemas['ContentLocaleState']['status']) =>
  (TRANSLATION_STATUS_TAG as Record<string, 'default' | 'success' | 'info' | 'primary' | 'warning' | 'error'>)[
    status
  ] ?? 'default';
</script>

<template>
  <div class="flex-col gap-16px">
    <NCard :title="$t('page.content.announcement.title')" :bordered="false" size="small" class="card-wrapper">
      <NSpin :show="!view">
        <NForm label-placement="left" label-width="auto" :disabled="!editable" class="max-w-760px">
          <NFormItem :label="$t('page.content.announcement.enabled')">
            <div class="flex items-center gap-12px">
              <NSwitch v-model:value="model.enabled" />
              <NText depth="3" class="text-12px">{{ $t('page.content.announcement.enabledHint') }}</NText>
            </div>
          </NFormItem>
          <NFormItem :label="$t('page.content.announcement.sourceLocale')">
            <NSelect v-model:value="model.sourceLocale" :options="SOURCE_LOCALE_OPTIONS" class="w-200px" />
          </NFormItem>
          <NFormItem :label="$t('page.content.announcement.titleField')">
            <NInput v-model:value="model.title" :maxlength="200" show-count />
          </NFormItem>
          <NFormItem :label="$t('page.content.announcement.body')">
            <NInput v-model:value="model.body" type="textarea" :rows="5" :maxlength="2000" show-count />
          </NFormItem>
        </NForm>
        <div class="max-w-760px flex items-center justify-between gap-12px">
          <NText depth="3" class="text-12px">{{ $t('page.content.announcement.translateHint') }}</NText>
          <PermissionGate code="content:announcement:edit">
            <NButton type="primary" :loading="saving" @click="save">{{ $t('page.content.announcement.save') }}</NButton>
          </PermissionGate>
        </div>
      </NSpin>
    </NCard>
    <NCard :title="$t('page.content.announcement.translations')" :bordered="false" size="small" class="card-wrapper">
      <NTable size="small" :single-line="false">
        <thead>
          <tr>
            <th class="w-120px">{{ $t('page.content.announcement.locale') }}</th>
            <th class="w-110px">{{ $t('page.content.announcement.status') }}</th>
            <th>{{ $t('page.content.announcement.content') }}</th>
            <th class="w-170px">{{ $t('page.content.announcement.updatedAt') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="row in rows" :key="row.code">
            <td>{{ localeName(row.code) }}</td>
            <td>
              <NTag size="small" :bordered="false" :type="statusType(row.state.status)">
                {{ $t(`page.content.announcement.statuses.${row.state.status}`) }}
              </NTag>
            </td>
            <td>
              <div class="flex-col gap-2px">
                <span class="font-600">{{ row.state.fields.title || '—' }}</span>
                <NEllipsis :line-clamp="2">{{ row.state.fields.body || '' }}</NEllipsis>
                <NText v-if="row.state.failReason" type="error" class="text-12px">{{ row.state.failReason }}</NText>
              </div>
            </td>
            <td>{{ formatDateTime(row.state.updatedAt) }}</td>
          </tr>
        </tbody>
      </NTable>
    </NCard>
  </div>
</template>
