<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue';
import { formatList } from '@opennavo/shared';
import { fetchPackageSeo, updatePackageSeo } from '@/service/api';
import type { DataOf } from '@/typings/api/opennavo';
import { useAppStore } from '@/store/modules/app';
import { $t } from '@/locales';
import { CONTENT_LOCALES, DEFAULT_SOURCE_LOCALE, localeName } from '@/utils/content-locale';
import type { ContentLocale } from '@/utils/content-locale';
import { formatDateTime, isDryRun, isRevisionConflict, writeOwnerLabel } from '@/utils/opennavo';
import { SEO_DESCRIPTION_MAX, SEO_TITLE_MAX, hasMarkup, overrideBody, singleLine } from './seo-override';

defineOptions({ name: 'SeoOverride' });

// Per-language search title and description for this app's overview page only; any field left empty, and
// every other page, keeps the template. Traditional Chinese derives from Simplified Chinese, so it has no
// override of its own. Each language saves on its own, but one revision covers them all.
const props = defineProps<{ packageId: number; editable: boolean }>();

type View = DataOf<'getPackageSeo'>;
type Item = View['items'][number];
type Form = { title: string; description: string };

const appStore = useAppStore();
const view = ref<View>();
const forms = reactive(
  Object.fromEntries(CONTENT_LOCALES.map(code => [code, { title: '', description: '' }])) as Record<ContentLocale, Form>
);
const active = ref<ContentLocale>(DEFAULT_SOURCE_LOCALE);
const loading = ref(false);
const loadFailed = ref(false);
const saving = ref<ContentLocale | null>(null);
const saveError = ref<{ locale: ContentLocale; key: 'reloaded' | 'saveFailed' } | null>(null);
// Languages whose saved text someone else changed, found when a save lost a revision conflict.
const changed = ref<ContentLocale[]>([]);

const text = (key: string, values?: Record<string, unknown>) =>
  values
    ? $t(`page.catalog.packageDetail.index.snippet.${key}` as App.I18n.I18nKey, values)
    : $t(`page.catalog.packageDetail.index.snippet.${key}` as App.I18n.I18nKey);
const saved = (code: ContentLocale): Item | undefined => view.value?.items.find(item => item.locale === code);
const editableFor = (code: ContentLocale) => props.editable && code !== 'zh-Hant';
const dirty = (code: ContentLocale) =>
  forms[code].title.trim() !== (saved(code)?.title ?? '') ||
  forms[code].description.trim() !== (saved(code)?.description ?? '');
const invalid = (code: ContentLocale) => hasMarkup(forms[code].title) || hasMarkup(forms[code].description);
const tabLabel = (code: ContentLocale) =>
  saved(code) ? `${localeName(code)} · ${text('overridden')}` : localeName(code);

const updatedInfo = (item: Item) =>
  $t('page.shared.updatedBy', { name: writeOwnerLabel(item.updatedBy), time: formatDateTime(item.updatedAt) });
const changedNames = computed(() => formatList(changed.value.map(localeName), { locale: appStore.locale }));

/** A language's saved text in a server view, the way its form holds it. */
function savedText(value: View | undefined, code: ContentLocale): Form {
  const item = value?.items.find(entry => entry.locale === code);
  return { title: item?.title ?? '', description: item?.description ?? '' };
}

/**
 * Take the server's view. With `only`, drafts in other languages survive; the read-only Traditional
 * Chinese form, derived from Simplified Chinese on the server, always follows it.
 */
function apply(value: View, only?: ContentLocale) {
  view.value = value;
  for (const code of CONTENT_LOCALES)
    if (!only || code === only || code === 'zh-Hant') forms[code] = savedText(value, code);
}

async function load() {
  loading.value = true;
  loadFailed.value = false;
  try {
    const { data, error } = await fetchPackageSeo(props.packageId);
    if (error) loadFailed.value = true;
    else {
      apply(data);
      changed.value = [];
      saveError.value = null;
    }
  } finally {
    loading.value = false;
  }
}

/**
 * A lost conflict moves the shared revision on, so no draft may outlive the text it was written against:
 * every language whose saved text changed loses its draft and is flagged. The others, the one being saved
 * included, keep their drafts for the next save.
 */
async function reloadAfterConflict(code: ContentLocale) {
  const before = view.value;
  const { data, error } = await fetchPackageSeo(props.packageId);
  if (error) {
    loadFailed.value = true;
    saveError.value = { locale: code, key: 'saveFailed' };
    return;
  }
  const moved = CONTENT_LOCALES.filter(locale => {
    if (locale === 'zh-Hant') return false;
    const [old, now] = [savedText(before, locale), savedText(data, locale)];
    return old.title !== now.title || old.description !== now.description;
  });
  view.value = data;
  for (const locale of [...moved, 'zh-Hant' as const]) forms[locale] = savedText(data, locale);
  changed.value = [...new Set([...changed.value, ...moved])];
  saveError.value = moved.includes(code) ? null : { locale: code, key: 'reloaded' };
}

async function write(code: ContentLocale, form: Form) {
  const current = view.value;
  if (!current || saving.value) return;
  saving.value = code;
  saveError.value = null;
  try {
    const { data, error, response } = await updatePackageSeo(
      props.packageId,
      overrideBody(code, form),
      current.revision
    );
    if (!error && !isDryRun(data)) {
      apply(data, code);
      changed.value = changed.value.filter(locale => locale !== code);
      window.$message?.success($t('page.shared.saved'));
    } else if (isRevisionConflict(response)) await reloadAfterConflict(code);
    else saveError.value = { locale: code, key: 'saveFailed' };
  } finally {
    saving.value = null;
  }
}

watch(
  () => props.packageId,
  () => load(),
  { immediate: true }
);
</script>

<template>
  <div data-testid="seo-override">
    <NText depth="3" class="mb-12px block">{{ text('hint') }}</NText>
    <NAlert v-if="loadFailed" type="error" class="mb-12px">
      {{ text('loadFailed') }}
      <NButton size="small" :loading="loading" @click="load()">{{ $t('common.refresh') }}</NButton>
    </NAlert>
    <NSkeleton v-if="loading && !view" text :repeat="3" />
    <NAlert v-if="changed.length" type="warning" class="mb-12px" data-testid="seo-override-changed">
      {{ text('changedList', { locales: changedNames }) }}
    </NAlert>
    <NTabs v-if="view" v-model:value="active" type="line" size="small">
      <NTabPane v-for="code in CONTENT_LOCALES" :key="code" :name="code" :tab="tabLabel(code)">
        <div class="flex-col gap-12px pt-8px" :data-locale="code">
          <NText v-if="code === 'zh-Hant'" depth="3">{{ $t('page.shared.i18n.derivedHint') }}</NText>
          <NAlert v-if="changed.includes(code)" type="warning">{{ text('changed') }}</NAlert>
          <NForm :model="forms[code]" :disabled="!editableFor(code) || saving !== null" label-placement="top">
            <NFormItem
              :label="text('title')"
              :validation-status="hasMarkup(forms[code].title) ? 'error' : undefined"
              :feedback="hasMarkup(forms[code].title) ? text('markup') : undefined"
            >
              <NInput
                :value="forms[code].title"
                :maxlength="SEO_TITLE_MAX"
                show-count
                clearable
                :placeholder="text('titlePlaceholder')"
                :input-props="{ 'aria-label': `${text('title')} · ${localeName(code)}` }"
                @update:value="value => (forms[code].title = singleLine(value))"
              />
            </NFormItem>
            <NFormItem
              :label="text('description')"
              :validation-status="hasMarkup(forms[code].description) ? 'error' : undefined"
              :feedback="hasMarkup(forms[code].description) ? text('markup') : undefined"
            >
              <NInput
                :value="forms[code].description"
                type="textarea"
                :autosize="{ minRows: 2, maxRows: 4 }"
                :maxlength="SEO_DESCRIPTION_MAX"
                show-count
                clearable
                :placeholder="text('descriptionPlaceholder')"
                :input-props="{ 'aria-label': `${text('description')} · ${localeName(code)}` }"
                @update:value="value => (forms[code].description = singleLine(value))"
              />
            </NFormItem>
          </NForm>
          <NAlert v-if="saveError?.locale === code" :type="saveError.key === 'reloaded' ? 'warning' : 'error'">
            {{ text(saveError.key) }}
          </NAlert>
          <div class="flex flex-wrap items-center gap-12px">
            <template v-if="editableFor(code)">
              <NButton
                type="primary"
                :loading="saving === code"
                :disabled="!dirty(code) || invalid(code) || saving !== null"
                @click="write(code, forms[code])"
              >
                {{ text('save') }}
              </NButton>
              <NPopconfirm v-if="saved(code)" @positive-click="write(code, { title: '', description: '' })">
                <template #trigger>
                  <NButton :disabled="saving !== null">{{ text('clear') }}</NButton>
                </template>
                {{ text('clearConfirm', { locale: localeName(code) }) }}
              </NPopconfirm>
            </template>
            <NText v-if="saved(code)" depth="3">{{ updatedInfo(saved(code)!) }}</NText>
          </div>
        </div>
      </NTabPane>
    </NTabs>
  </div>
</template>
