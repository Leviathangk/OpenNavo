<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue';
import { fetchReleaseDetail, upsertReleaseNotes } from '@/service/api';
import type { RecordOf } from '@/typings/api/opennavo';
import MarkdownEditor from '@/components/opennavo/markdown-editor.vue';
import { CONTENT_LOCALE_OPTIONS } from '@/utils/content-locale';
import { $t } from '@/locales';
import { NOTES_LIMITS, buildNotesBody, emptyNotes, notesFromRelease, notesVersion, ownsNotes, validateNotes } from './notes';
import type { NotesForm } from './notes';

defineOptions({ name: 'ReleaseNotesEditor' });

// Author notes (11 §3.2, 07 §10.3) for any cataloged version: summary, up to eight groups,
// six highlights each, optional title/date/body; write source only, then automatically translate the other five locales.
const props = defineProps<{ version: RecordOf<'listAdminVersions'> | null }>();
const show = defineModel<boolean>('show', { required: true });
const emit = defineEmits<{ saved: [] }>();

const form = reactive<NotesForm>(emptyNotes());
const loading = ref(false);
const saving = ref(false);
const title = computed(() =>
  props.version ? $t('page.changelog.notes.editorTitle', { token: props.version.token, version: notesVersion(props.version) }) : ''
);

watch(show, async open => {
  if (!open || !props.version) return;
  Object.assign(form, emptyNotes());
  // A build still showing its base version's shared note starts empty: saving writes notes of its own.
  if (!props.version.editorialReleaseId || !ownsNotes(props.version)) return;
  loading.value = true;
  const { data, error } = await fetchReleaseDetail(props.version.editorialReleaseId);
  loading.value = false;
  if (!error) Object.assign(form, notesFromRelease(data));
}, { immediate: true });

function addGroup() {
  if (form.groups.length >= NOTES_LIMITS.groups) return;
  form.groups.push({ area: '', items: [''] });
}

function addItem(index: number) {
  const group = form.groups[index];
  if (group.items.length < NOTES_LIMITS.items) group.items.push('');
}

async function save() {
  if (!props.version) return;
  const problem = validateNotes(form);
  if (problem) {
    window.$message?.warning($t(problem as App.I18n.I18nKey, NOTES_LIMITS));
    return;
  }
  saving.value = true;
  const { error } = await upsertReleaseNotes(props.version.packageId, notesVersion(props.version), buildNotesBody(form));
  saving.value = false;
  if (error) return;
  window.$message?.success($t('page.changelog.notes.saved'));
  show.value = false;
  emit('saved');
}
</script>

<template>
  <NDrawer v-model:show="show" :width="760" placement="right">
    <NDrawerContent :title="title" closable>
      <NSpin :show="loading">
        <NForm label-placement="top" :show-feedback="false" class="flex-col gap-16px">
          <NGrid :x-gap="16" :cols="2">
            <NFormItemGi :label="$t('page.changelog.notes.sourceLocale')">
              <NSelect v-model:value="form.sourceLocale" :options="CONTENT_LOCALE_OPTIONS" />
            </NFormItemGi>
            <NFormItemGi :label="$t('page.changelog.notes.publishedAt')">
              <NDatePicker v-model:value="form.publishedAt" type="datetime" clearable class="w-full" />
            </NFormItemGi>
          </NGrid>
          <NFormItem :label="$t('page.changelog.notes.titleField')">
            <NInput v-model:value="form.title" :maxlength="NOTES_LIMITS.title" :placeholder="$t('page.changelog.notes.titlePlaceholder')" clearable />
          </NFormItem>
          <NFormItem :label="$t('page.changelog.notes.summary')" required>
            <NInput v-model:value="form.summary" type="textarea" :rows="3" :maxlength="NOTES_LIMITS.summary" show-count />
          </NFormItem>
          <div class="flex-col gap-8px">
            <div class="flex items-center justify-between">
              <span class="text-14px font-600">{{ $t('page.changelog.notes.sections') }}</span>
              <NButton size="small" :disabled="form.groups.length >= NOTES_LIMITS.groups" @click="addGroup">
                {{ $t('page.changelog.notes.addGroup') }}
              </NButton>
            </div>
            <NText depth="3" class="text-12px">{{ $t('page.changelog.notes.sectionsHint', NOTES_LIMITS) }}</NText>
            <div v-for="(group, index) in form.groups" :key="index" class="flex-col gap-6px rounded-default bg-layout p-12px">
              <div class="flex items-center gap-8px">
                <NInput v-model:value="group.area" :maxlength="NOTES_LIMITS.area" :placeholder="$t('page.changelog.notes.areaPlaceholder')" class="!w-200px" />
                <NButton size="small" quaternary type="error" class="ml-auto" @click="form.groups.splice(index, 1)">
                  {{ $t('page.changelog.notes.removeGroup') }}
                </NButton>
              </div>
              <div v-for="(_, itemIndex) in group.items" :key="itemIndex" class="flex items-center gap-8px">
                <NInput v-model:value="group.items[itemIndex]" :maxlength="NOTES_LIMITS.item" :placeholder="$t('page.changelog.notes.itemPlaceholder')" />
                <NButton size="small" quaternary @click="group.items.splice(itemIndex, 1)">×</NButton>
              </div>
              <NButton size="small" dashed :disabled="group.items.length >= NOTES_LIMITS.items" @click="addItem(index)">
                {{ $t('page.changelog.notes.addItem') }}
              </NButton>
            </div>
          </div>
          <NFormItem :label="$t('page.changelog.notes.body')">
            <MarkdownEditor v-model="form.bodyMarkdown" :rows="8" :placeholder="$t('page.changelog.notes.bodyPlaceholder')" />
          </NFormItem>
        </NForm>
      </NSpin>
      <template #footer>
        <div class="w-full flex items-center justify-between gap-12px">
          <NText depth="3" class="text-12px">{{ $t('page.changelog.notes.translateHint') }}</NText>
          <div class="flex gap-12px">
            <NButton @click="show = false">{{ $t('common.cancel') }}</NButton>
            <NButton type="primary" :loading="saving" @click="save">{{ $t('page.changelog.notes.save') }}</NButton>
          </div>
        </div>
      </template>
    </NDrawerContent>
  </NDrawer>
</template>
