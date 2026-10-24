<script setup lang="ts">
import { computed, watch } from 'vue';
import type { FormItemRule } from 'naive-ui';
import { $t } from '@/locales';
import { CONTENT_LOCALES, SOURCE_LOCALE_OPTIONS, TRANSLATION_STATUS_TAG, localeName } from '@/utils/content-locale';
import type { ContentLocale, LocalizedStatuses, LocalizedTexts } from '@/utils/content-locale';

defineOptions({ name: 'LocalizedFields' });

// Multilingual editor (12, 04 §9.1): choose source language, enter/validate source, auto-translate five other languages,
// with expandable manual corrections. Editing only; utils/content-locale localizedBody assembles source/changed-language writes.
export interface LocalizedField {
  key: string;
  label: string;
  /** Single line by default. */
  type?: 'input' | 'textarea';
  rows?: number;
  maxlength?: number;
  /** Required in source language. */
  required?: boolean;
}

const props = withDefaults(
  defineProps<{
    fields: readonly LocalizedField[];
    /** Loaded translation statuses; empty for new content. */
    statuses?: LocalizedStatuses;
    /** Text-map path in NForm model for validation. */
    path?: string;
    /** Show corrections for other languages; short captions may edit source only. */
    showTranslations?: boolean;
  }>(),
  { statuses: () => ({}), path: 'i18n', showTranslations: true }
);

const sourceLocale = defineModel<ContentLocale>('sourceLocale', { required: true });
const texts = defineModel<LocalizedTexts>('texts', { required: true });

// Switching source language may require creating its field group.
watch(
  sourceLocale,
  code => {
    if (!texts.value[code]) texts.value[code] = Object.fromEntries(props.fields.map(field => [field.key, null]));
  },
  { immediate: true }
);

const others = computed(() =>
  CONTENT_LOCALES.filter(code => code !== sourceLocale.value).map(code => ({ code, status: props.statuses[code] }))
);
const translated = computed(() => others.value.filter(tab => tab.status || texts.value[tab.code]).length);

const requiredRule: FormItemRule = {
  required: true,
  message: $t('page.shared.i18n.required'),
  trigger: ['blur', 'input']
};

function valueOf(code: ContentLocale, key: string): string {
  return texts.value[code]?.[key] ?? '';
}

function update(code: ContentLocale, key: string, value: string) {
  const entry = texts.value[code] ?? Object.fromEntries(props.fields.map(field => [field.key, null]));
  entry[key] = value;
  texts.value[code] = entry;
}

function tabLabel(code: ContentLocale) {
  const status = props.statuses[code];
  return status ? `${localeName(code)} · ${$t(`page.shared.translationStatus.${status}`)}` : localeName(code);
}
</script>

<template>
  <NFormItem :label="$t('page.shared.i18n.sourceLocale')">
    <div class="w-full flex flex-wrap items-center gap-12px">
      <NSelect v-model:value="sourceLocale" :options="SOURCE_LOCALE_OPTIONS" class="w-200px" />
      <NText depth="3" class="text-12px">{{ $t('page.shared.i18n.sourceHint') }}</NText>
    </div>
  </NFormItem>
  <NFormItem
    v-for="field in fields"
    :key="field.key"
    :label="field.label"
    :path="`${path}.${sourceLocale}.${field.key}`"
    :rule="field.required ? requiredRule : undefined"
  >
    <NInput
      :value="valueOf(sourceLocale, field.key)"
      :type="field.type === 'textarea' ? 'textarea' : 'text'"
      :rows="field.rows ?? 2"
      :maxlength="field.maxlength"
      :show-count="Boolean(field.maxlength)"
      @update:value="value => update(sourceLocale, field.key, value)"
    />
  </NFormItem>
  <NFormItem v-if="showTranslations" :label="$t('page.shared.i18n.translations')">
    <NCollapse class="w-full">
      <NCollapseItem
        :title="
          $t(
            'page.shared.i18n.translationsSummary',
            { done: translated, total: others.length },
            { plural: others.length }
          )
        "
        name="others"
      >
        <NTabs type="line" size="small" animated>
          <NTabPane v-for="tab in others" :key="tab.code" :name="tab.code" :tab="tabLabel(tab.code)">
            <div class="flex-col gap-10px">
              <NText v-if="tab.code === 'zh-Hant'" depth="3">{{ $t('page.shared.i18n.derivedHint') }}</NText>
              <NTag
                v-if="tab.status"
                size="small"
                :bordered="false"
                :type="TRANSLATION_STATUS_TAG[tab.status]"
                class="self-start"
              >
                {{ $t(`page.shared.translationStatus.${tab.status}`) }}
              </NTag>
              <div v-for="field in fields" :key="field.key" class="flex-col gap-4px">
                <NText depth="3" class="text-12px">{{ field.label }}</NText>
                <NInput
                  :value="valueOf(tab.code, field.key)"
                  :readonly="tab.code === 'zh-Hant'"
                  :type="field.type === 'textarea' ? 'textarea' : 'text'"
                  :rows="field.rows ?? 2"
                  :maxlength="field.maxlength"
                  :placeholder="$t('page.shared.i18n.autoPlaceholder')"
                  @update:value="value => update(tab.code, field.key, value)"
                />
              </div>
            </div>
          </NTabPane>
        </NTabs>
      </NCollapseItem>
    </NCollapse>
  </NFormItem>
</template>
