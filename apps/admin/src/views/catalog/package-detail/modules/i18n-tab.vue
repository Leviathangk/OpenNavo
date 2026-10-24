<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue';
import { updatePackageI18n } from '@/service/api';
import type { DataOf } from '@/typings/api/opennavo';
import { useAuth } from '@/hooks/business/auth';
import MarkdownEditor from '@/components/opennavo/markdown-editor.vue';
import { CONTENT_LOCALES, DEFAULT_SOURCE_LOCALE, TRANSLATION_STATUS_TAG, localeName } from '@/utils/content-locale';
import type { ContentLocale } from '@/utils/content-locale';
import { formatDateTime } from '@/utils/opennavo';
import { $t } from '@/locales';

defineOptions({ name: 'I18nTab' });

// Text/translations (12, 04 §9.1): six tabs for name, summary, and Markdown introduction shown in public overview.
// Source edits trigger retranslation; edits in other tabs are manual corrections without review. Save each tab separately.
const props = defineProps<{ detail: DataOf<'getAdminPackage'> }>();
const emit = defineEmits<{ saved: [] }>();

// Suggested Chinese summary length matches LLM rules; exceedance only warns (API limit 120 characters).
const ZH_SUMMARY_SUGGESTED = 30;

const { hasAuth } = useAuth();
// Source edits require catalog:package:edit; manual translation corrections require translation:review (07 §10.1).
const canEditSource = computed(() => hasAuth('catalog:package:edit'));
const canFix = computed(() => hasAuth('translation:review'));

const sourceLocale = computed<ContentLocale>(() => props.detail.sourceLocale ?? DEFAULT_SOURCE_LOCALE);
const active = ref<ContentLocale>(sourceLocale.value);
const editableFor = (code: ContentLocale) =>
  code !== 'zh-Hant' && (code === sourceLocale.value ? canEditSource.value : canFix.value);

type TextForm = { displayName: string; summary: string; description: string };
const forms = reactive(
  Object.fromEntries(CONTENT_LOCALES.map(code => [code, { displayName: '', summary: '', description: '' }])) as Record<
    ContentLocale,
    TextForm
  >
);
// Selecting this on a non-source tab saves that language as source; API requires sourceLocale to match the URL locale.
const makeSource = ref(false);
const saving = ref<ContentLocale | null>(null);

const views = computed(
  () =>
    Object.fromEntries(
      CONTENT_LOCALES.map(code => [code, props.detail.i18n.find(item => item.locale === code) ?? null])
    ) as Record<ContentLocale, DataOf<'getAdminPackage'>['i18n'][number] | null>
);

watch(
  () => props.detail,
  detail => {
    for (const code of CONTENT_LOCALES) {
      const view = detail.i18n.find(item => item.locale === code);
      forms[code] = {
        displayName: view?.displayName ?? '',
        summary: view?.summary ?? '',
        description: view?.description ?? ''
      };
    }
    active.value = detail.sourceLocale ?? active.value;
  },
  { immediate: true }
);

watch(active, () => {
  makeSource.value = false;
});

const tooLong = (code: ContentLocale) => code === 'zh-CN' && [...forms[code].summary].length > ZH_SUMMARY_SUGGESTED;
const orNull = (value: string) => (value.trim() ? value.trim() : null);

function tabLabel(code: ContentLocale) {
  if (code === sourceLocale.value) return `${localeName(code)} · ${$t('page.shared.translationStatus.source')}`;
  const status = views.value[code]?.status;
  return status ? `${localeName(code)} · ${$t(`page.shared.translationStatus.${status}`)}` : localeName(code);
}

async function save(code: ContentLocale) {
  saving.value = code;
  const form = forms[code];
  const { error } = await updatePackageI18n(props.detail.id, code, {
    displayName: orNull(form.displayName),
    summary: orNull(form.summary),
    description: orNull(form.description),
    ...(makeSource.value && code !== sourceLocale.value ? { sourceLocale: code } : {})
  });
  saving.value = null;
  if (error) return;
  window.$message?.success($t('page.catalog.packageDetail.i18n.saved'));
  emit('saved');
}
</script>

<template>
  <div class="flex-col gap-12px">
    <NText depth="3" class="text-12px">
      {{ $t('page.catalog.packageDetail.i18n.hint', { locale: localeName(sourceLocale) }) }}
    </NText>
    <NTabs v-model:value="active" type="line" size="small">
      <NTabPane v-for="code in CONTENT_LOCALES" :key="code" :name="code" :tab="tabLabel(code)">
        <NText v-if="code === 'zh-Hant'" depth="3">{{ $t('page.shared.i18n.derivedHint') }}</NText>
        <div class="flex-col gap-12px pt-8px" :data-locale="code">
          <div v-if="views[code]" class="flex flex-wrap items-center gap-8px">
            <NTag size="small" :bordered="false" :type="TRANSLATION_STATUS_TAG[views[code].status]">
              {{ $t(`page.shared.translationStatus.${views[code].status}`) }}
            </NTag>
            <NTag size="small" :bordered="false">
              {{ $t(`page.catalog.packageDetail.i18n.sources.${views[code].source}`) }}
            </NTag>
            <NText v-if="views[code].model" depth="3" class="text-12px">
              {{ $t('page.catalog.packageDetail.i18n.model', { model: views[code].model }) }}
            </NText>
            <NText v-if="views[code].translatedAt" depth="3" class="text-12px">
              {{
                $t('page.catalog.packageDetail.i18n.translatedAt', { time: formatDateTime(views[code].translatedAt) })
              }}
            </NText>
          </div>
          <NAlert v-if="views[code]?.stale" type="warning" :bordered="false">
            {{ $t('page.catalog.packageDetail.i18n.stale') }}
          </NAlert>
          <NForm :model="forms[code]" :disabled="!editableFor(code)" label-placement="top">
            <NFormItem :label="$t('page.catalog.packageDetail.i18n.displayName')">
              <NInput v-model:value="forms[code].displayName" :maxlength="60" show-count clearable />
            </NFormItem>
            <NFormItem
              :label="$t('page.catalog.packageDetail.i18n.summary')"
              :validation-status="tooLong(code) ? 'warning' : undefined"
              :feedback="
                tooLong(code)
                  ? $t('page.catalog.packageDetail.i18n.summaryTooLong')
                  : code === 'zh-CN'
                    ? $t('page.catalog.packageDetail.i18n.summaryHint')
                    : undefined
              "
            >
              <NInput v-model:value="forms[code].summary" :maxlength="120" show-count clearable />
            </NFormItem>
            <NFormItem :label="$t('page.catalog.packageDetail.i18n.description')">
              <MarkdownEditor
                v-model="forms[code].description"
                :maxlength="20000"
                :rows="12"
                :disabled="!editableFor(code)"
              />
            </NFormItem>
          </NForm>
          <div v-if="editableFor(code)" class="flex flex-wrap items-center justify-end gap-12px">
            <NCheckbox v-if="code !== sourceLocale && canEditSource" v-model:checked="makeSource">
              {{ $t('page.catalog.packageDetail.i18n.makeSource') }}
            </NCheckbox>
            <NButton type="primary" :loading="saving === code" @click="save(code)">
              {{ $t('page.catalog.packageDetail.i18n.save') }}
            </NButton>
          </div>
        </div>
      </NTabPane>
    </NTabs>
  </div>
</template>
