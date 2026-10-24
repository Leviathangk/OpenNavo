<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue';
import { upsertGlossaryTerm } from '@/service/api';
import type { Schemas } from '@/typings/api/opennavo';
import { CONTENT_LOCALES, localeName } from '@/utils/content-locale';
import type { ContentLocale } from '@/utils/content-locale';
import { $t } from '@/locales';

defineOptions({ name: 'GlossaryForm' });

// Glossary terms: fixed translations or no-translate markers for brands/commands; effective on the next translation.
// API replaces translations by natural term key, so the term itself is immutable during editing.
const props = defineProps<{ term: Schemas['GlossaryTerm'] | null }>();
const show = defineModel<boolean>('show', { required: true });
const emit = defineEmits<{ saved: [] }>();

const writableLocales = CONTENT_LOCALES.filter(code => code !== 'zh-Hant');

const model = reactive({
  term: '',
  doNotTranslate: false,
  translations: Object.fromEntries(writableLocales.map(code => [code, ''])) as Record<ContentLocale, string>
});
const saving = ref(false);
const editing = computed(() => Boolean(props.term));

watch(
  show,
  open => {
    if (!open) return;
    model.term = props.term?.term ?? '';
    model.doNotTranslate = props.term?.doNotTranslate ?? false;
    for (const code of writableLocales) model.translations[code] = props.term?.translations[code] ?? '';
  },
  { immediate: true }
);

async function save() {
  const term = model.term.trim();
  if (!term) {
    window.$message?.warning($t('page.content.glossary.termRequired'));
    return;
  }
  saving.value = true;
  const translations = Object.fromEntries(
    writableLocales.map(code => [code, model.doNotTranslate ? null : model.translations[code].trim() || null])
  ) as Schemas['GlossaryTranslations'];
  const { error } = await upsertGlossaryTerm({ term, translations, doNotTranslate: model.doNotTranslate });
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
    :title="editing ? $t('page.content.glossary.editTitle') : $t('page.content.glossary.addTitle')"
    class="w-600px"
  >
    <NForm label-placement="left" label-width="auto">
      <NFormItem :label="$t('page.content.glossary.term')" required>
        <NInput
          v-model:value="model.term"
          :maxlength="200"
          :disabled="editing"
          :placeholder="$t('page.content.glossary.termPlaceholder')"
        />
      </NFormItem>
      <NFormItem :label="$t('page.content.glossary.doNotTranslate')">
        <div class="flex items-center gap-12px">
          <NSwitch v-model:value="model.doNotTranslate" />
          <NText depth="3" class="text-12px">{{ $t('page.content.glossary.doNotTranslateHint') }}</NText>
        </div>
      </NFormItem>
      <NFormItem v-for="code in writableLocales" :key="code" :label="localeName(code)">
        <NInput v-model:value="model.translations[code]" :maxlength="200" :disabled="model.doNotTranslate" clearable />
      </NFormItem>
    </NForm>
    <NText depth="3" class="text-12px">{{ $t('page.content.glossary.formHint') }}</NText>
    <template #footer>
      <div class="flex justify-end gap-12px">
        <NButton @click="show = false">{{ $t('common.cancel') }}</NButton>
        <NButton type="primary" :loading="saving" @click="save">{{ $t('common.confirm') }}</NButton>
      </div>
    </template>
  </NModal>
</template>
