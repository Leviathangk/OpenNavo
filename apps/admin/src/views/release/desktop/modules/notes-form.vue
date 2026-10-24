<script setup lang="ts">
import { reactive, ref, watch } from 'vue';
import { updateDesktopRelease } from '@/service/api';
import type { RecordOf } from '@/typings/api/opennavo';
import MarkdownEditor from '@/components/opennavo/markdown-editor.vue';
import {
  APP_CONTENT_LOCALES,
  APP_SOURCE_LOCALE_OPTIONS,
  DEFAULT_SOURCE_LOCALE,
  fromView,
  localeName,
  localizedBody
} from '@/utils/content-locale';
import type { AppContentLocale, LocalizedStatuses, LocalizedTexts } from '@/utils/content-locale';
import { $t } from '@/locales';

defineOptions({ name: 'NotesForm' });

// Edit release Markdown notes (source plus individually correctable AI translations) and minimum OS (07 §7.12).
// Changing published notes also refreshes the backend manifest.
const props = defineProps<{ release: RecordOf<'listDesktopReleases'> | null }>();
const show = defineModel<boolean>('show', { required: true });
const emit = defineEmits<{ saved: [] }>();

const TEXT_KEYS = ['notes'] as const;

const form = reactive({
  minMacos: '13.0',
  sourceLocale: DEFAULT_SOURCE_LOCALE as AppContentLocale,
  notes: Object.fromEntries(APP_CONTENT_LOCALES.map(code => [code, ''])) as Record<AppContentLocale, string>
});
// Initial notes/statuses: submit only source and modified languages (04 §9.1).
const original = ref<LocalizedTexts>({});
const statuses = ref<LocalizedStatuses>({});
// Open on the source-language tab.
const activeLocale = ref<AppContentLocale>(DEFAULT_SOURCE_LOCALE as AppContentLocale);
const saving = ref(false);

watch(show, open => {
  if (!open || !props.release) return;
  const view = fromView(props.release.i18n, TEXT_KEYS);
  original.value = view.texts;
  statuses.value = view.statuses;
  Object.assign(form, {
    minMacos: props.release.minMacos,
    sourceLocale: props.release.sourceLocale ?? DEFAULT_SOURCE_LOCALE,
    notes: Object.fromEntries(APP_CONTENT_LOCALES.map(code => [code, view.texts[code]?.notes ?? '']))
  });
  activeLocale.value = form.sourceLocale;
});

function tabLabel(code: AppContentLocale) {
  if (code === form.sourceLocale) return `${localeName(code)} · ${$t('page.shared.translationStatus.source')}`;
  const status = statuses.value[code];
  return status ? `${localeName(code)} · ${$t(`page.shared.translationStatus.${status}`)}` : localeName(code);
}

async function save() {
  if (!props.release) return;
  if (!form.notes[form.sourceLocale].trim()) {
    window.$message?.warning($t('page.shared.i18n.required'));
    return;
  }
  saving.value = true;
  const { error } = await updateDesktopRelease(props.release.id, {
    minMacos: form.minMacos.trim() || '13.0',
    sourceLocale: form.sourceLocale,
    i18n: localizedBody({
      texts: Object.fromEntries(APP_CONTENT_LOCALES.map(code => [code, { notes: form.notes[code] }])),
      original: original.value,
      sourceLocale: form.sourceLocale,
      keys: TEXT_KEYS,
      entry: text => ({ notes: text.notes ?? '' })
    })
  });
  saving.value = false;
  if (error) return;
  window.$message?.success($t('page.shared.saved'));
  show.value = false;
  emit('saved');
}
</script>

<template>
  <NModal
    v-model:show="show"
    preset="card"
    :title="release ? $t('page.release.desktop.notesTitle', { version: release.version }) : ''"
    class="w-760px"
  >
    <NForm label-placement="top">
      <NFormItem :label="$t('page.release.desktop.minMacos')">
        <NInput v-model:value="form.minMacos" class="w-160px font-mono" />
      </NFormItem>
      <NFormItem :label="$t('page.shared.i18n.sourceLocale')">
        <div class="w-full flex flex-wrap items-center gap-12px">
          <NSelect v-model:value="form.sourceLocale" :options="APP_SOURCE_LOCALE_OPTIONS" class="w-200px" />
          <NText depth="3" class="text-12px">{{ $t('page.shared.i18n.sourceHint') }}</NText>
        </div>
      </NFormItem>
      <NFormItem :label="$t('page.release.desktop.notes')">
        <NTabs v-model:value="activeLocale" type="line" size="small" class="w-full">
          <NTabPane v-for="code in APP_CONTENT_LOCALES" :key="code" :name="code" :tab="tabLabel(code)">
            <NText v-if="code === 'zh-Hant'" depth="3">{{ $t('page.shared.i18n.derivedHint') }}</NText>
            <MarkdownEditor v-model="form.notes[code]" :disabled="code === 'zh-Hant'" :rows="6" />
          </NTabPane>
        </NTabs>
      </NFormItem>
    </NForm>
    <template #footer>
      <div class="flex justify-end gap-12px">
        <NButton @click="show = false">{{ $t('common.cancel') }}</NButton>
        <NButton type="primary" :loading="saving" @click="save">{{ $t('common.confirm') }}</NButton>
      </div>
    </template>
  </NModal>
</template>
