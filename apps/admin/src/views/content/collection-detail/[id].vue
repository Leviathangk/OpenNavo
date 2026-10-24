<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue';
import { useRoute } from 'vue-router';
import type { FormInst, FormRules, UploadCustomRequestOptions } from 'naive-ui';
import { OnCollectionCard } from '@opennavo/ui';
import {
  createCollection,
  fetchCollectionDetail,
  publishCollection,
  setCollectionItems,
  unpublishCollection,
  updateCollection,
  uploadAsset
} from '@/service/api';
import type { BodyOf, DataOf } from '@/typings/api/opennavo';
import { useAuth } from '@/hooks/business/auth';
import { useRouterPush } from '@/hooks/common/router';
import MarkdownEditor from '@/components/opennavo/markdown-editor.vue';
import UiPreview from '@/components/opennavo/ui-preview.vue';
import { PUBLISH_STATUS_TAG, formatDateTime, isDryRun } from '@/utils/opennavo';
import {
  CONTENT_LOCALES,
  CONTENT_LOCALE_OPTIONS,
  SOURCE_LOCALE_OPTIONS,
  DEFAULT_SOURCE_LOCALE,
  fromView,
  localeName,
  localizedBody
} from '@/utils/content-locale';
import type { ContentLocale, LocalizedStatuses, LocalizedTexts } from '@/utils/content-locale';
import { $t } from '@/locales';
import ItemsEditor from './modules/items-editor.vue';
import type { EditableItem } from './modules/types';

defineOptions({ name: 'ContentCollectionDetail' });

// Collection editor (07 §7.5): metadata, items, publication (including scheduling; content:collection:publish), live public-card preview on right.
// Route ID new means creation: save metadata to obtain an ID before editing items.
const route = useRoute();
const { hasAuth } = useAuth();
const { routerPushByKey } = useRouterPush();

const SLUG = /^[a-z0-9][a-z0-9-]*$/;
const TEXT_KEYS = ['title', 'subtitle', 'body', 'seoTitle', 'seoDescription'] as const;

const id = computed(() => (route.params.id === 'new' ? null : Number(route.params.id)));
const detail = ref<DataOf<'getAdminCollection'> | null>(null);
const loading = ref(false);
const saving = ref(false);
const formRef = ref<FormInst | null>(null);
const previewLocale = ref<ContentLocale>(DEFAULT_SOURCE_LOCALE);
const scheduleAt = ref<number | null>(null);

// String fields for all six languages simplify input binding; localizedBody trims empty strings away on save.
function fullTexts(texts: LocalizedTexts): Record<ContentLocale, Record<string, string>> {
  return Object.fromEntries(
    CONTENT_LOCALES.map(code => [code, Object.fromEntries(TEXT_KEYS.map(key => [key, texts[code]?.[key] ?? '']))])
  ) as Record<ContentLocale, Record<string, string>>;
}

const model = reactive({
  slug: '',
  sort: 0,
  unpublishAt: null as number | null,
  coverAssetId: null as number | null,
  coverUrl: null as string | null,
  sourceLocale: DEFAULT_SOURCE_LOCALE as ContentLocale,
  i18n: fullTexts({})
});
// Loaded text/statuses: submit only source and changed languages (04 §9.1).
const original = ref<LocalizedTexts>({});
const statuses = ref<LocalizedStatuses>({});
// Open on the source-language tab.
const activeLocale = ref<ContentLocale>(DEFAULT_SOURCE_LOCALE);
const items = ref<EditableItem[]>([]);

async function load() {
  if (id.value === null) {
    detail.value = null;
    original.value = {};
    statuses.value = {};
    Object.assign(model, {
      slug: '',
      sort: 0,
      unpublishAt: null,
      coverAssetId: null,
      coverUrl: null,
      sourceLocale: DEFAULT_SOURCE_LOCALE,
      i18n: fullTexts({})
    });
    items.value = [];
    activeLocale.value = DEFAULT_SOURCE_LOCALE;
    return;
  }
  loading.value = true;
  const { data, error } = await fetchCollectionDetail(id.value);
  loading.value = false;
  if (error) return;
  detail.value = data;
  const view = fromView(data.i18n, TEXT_KEYS);
  original.value = view.texts;
  statuses.value = view.statuses;
  Object.assign(model, {
    slug: data.slug,
    sort: data.sort,
    unpublishAt: data.unpublishAt ? new Date(data.unpublishAt).getTime() : null,
    coverAssetId: data.coverAssetId ?? null,
    coverUrl: data.coverUrl ?? null,
    sourceLocale: data.sourceLocale ?? DEFAULT_SOURCE_LOCALE,
    i18n: fullTexts(view.texts)
  });
  previewLocale.value = model.sourceLocale;
  activeLocale.value = model.sourceLocale;
  items.value = [...(data.items ?? [])]
    .sort((a, b) => a.sort - b.sort)
    .map(item => {
      const notes = fromView(item.i18n, ['note']).texts;
      return {
        packageId: item.packageId,
        kind: item.kind,
        token: item.token,
        name: item.name,
        iconUrl: item.iconUrl ?? null,
        disabled: Boolean(item.disabled),
        sourceLocale: item.sourceLocale ?? model.sourceLocale,
        notes: structuredClone(notes),
        original: notes
      };
    });
}

watch(id, load, { immediate: true });

const rules = computed<FormRules>(() => ({
  slug: [
    { required: true, message: $t('form.required'), trigger: ['blur', 'input'] },
    { pattern: SLUG, message: $t('page.content.collectionDetail.slugInvalid'), trigger: ['blur', 'input'] }
  ],
  // Only the source title is required; AI translates other languages.
  [`i18n.${model.sourceLocale}.title`]: [{ required: true, message: $t('form.required'), trigger: ['blur', 'input'] }]
}));

function body(): BodyOf<'createCollection'> {
  return {
    slug: model.slug.trim(),
    sort: model.sort,
    unpublishAt: model.unpublishAt ? new Date(model.unpublishAt).toISOString() : null,
    coverAssetId: model.coverAssetId,
    sourceLocale: model.sourceLocale,
    // Non-source collection languages accept null to clear translations (04 §9.1).
    i18n: localizedBody({
      texts: model.i18n,
      original: original.value,
      sourceLocale: model.sourceLocale,
      keys: TEXT_KEYS,
      entry: text => ({
        title: text.title ?? '',
        subtitle: text.subtitle,
        body: text.body,
        seoTitle: text.seoTitle,
        seoDescription: text.seoDescription
      }),
      clearable: true
    })
  };
}

// Item recommendations: omit i18n/sourceLocale when absent (04 §9.1); otherwise send source and changed languages only.
function itemBody(item: EditableItem) {
  const note = item.notes[item.sourceLocale]?.note?.trim();
  const hadNotes = Object.values(item.original).some(entry => entry?.note);
  if (!note && !hadNotes) return { packageId: item.packageId };
  return {
    packageId: item.packageId,
    sourceLocale: item.sourceLocale,
    i18n: localizedBody({
      texts: item.notes,
      original: item.original,
      sourceLocale: item.sourceLocale,
      keys: ['note'],
      entry: text => ({ note: text.note })
    })
  };
}

/** Title fallback: source language → any populated language. */
const titleOf = () =>
  model.i18n[model.sourceLocale].title || CONTENT_LOCALES.map(code => model.i18n[code].title).find(Boolean) || '';

function tabLabel(code: ContentLocale) {
  if (code === model.sourceLocale) return `${localeName(code)} · ${$t('page.shared.translationStatus.source')}`;
  const status = statuses.value[code];
  return status ? `${localeName(code)} · ${$t(`page.shared.translationStatus.${status}`)}` : localeName(code);
}

async function save() {
  await formRef.value?.validate();
  saving.value = true;
  let target = id.value;
  if (target === null) {
    const created = await createCollection(body());
    if (created.error || isDryRun(created.data)) {
      saving.value = false;
      return;
    }
    target = created.data.id;
  } else {
    const updated = await updateCollection(target, body());
    if (updated.error) {
      saving.value = false;
      return;
    }
  }
  const { error } = await setCollectionItems(target, { items: items.value.map(itemBody) });
  saving.value = false;
  if (error) return;
  window.$message?.success($t('page.content.collectionDetail.saved'));
  if (id.value === null) await routerPushByKey('content_collection-detail', { params: { id: String(target) } });
  else await load();
}

function publish(scheduled: boolean) {
  if (id.value === null || !detail.value) return;
  const target = id.value;
  const title = titleOf();
  window.$dialog?.warning({
    title: $t('page.content.collection.publish'),
    content: scheduled
      ? `${$t('page.content.collection.publishConfirm', { title })} ${$t('page.content.collection.scheduledAt', { time: formatDateTime(scheduleAt.value) })}`
      : $t('page.content.collection.publishConfirm', { title }),
    positiveText: $t('common.confirm'),
    negativeText: $t('common.cancel'),
    onPositiveClick: async () => {
      const at = scheduled && scheduleAt.value ? new Date(scheduleAt.value).toISOString() : null;
      const { error } = await publishCollection(target, { publishAt: at });
      if (error) return;
      window.$message?.success($t('page.content.collection.published'));
      await load();
    }
  });
}

function unpublish() {
  if (id.value === null) return;
  const target = id.value;
  window.$dialog?.warning({
    title: $t('page.content.collection.unpublish'),
    content: $t('page.content.collection.unpublishConfirm', { title: titleOf() }),
    positiveText: $t('common.confirm'),
    negativeText: $t('common.cancel'),
    onPositiveClick: async () => {
      const { error } = await unpublishCollection(target);
      if (error) return;
      window.$message?.success($t('page.content.collection.unpublished'));
      await load();
    }
  });
}

async function uploadCover({ file, onFinish, onError }: UploadCustomRequestOptions) {
  if (!file.file) return onError();
  const { data, error } = await uploadAsset(file.file, 'cover');
  if (error || isDryRun(data)) return onError();
  model.coverAssetId = data.id;
  model.coverUrl = data.url;
  onFinish();
}

// Preview wrapper currently maps Chinese to Chinese and other locales to English.
const previewUiLocale = computed(() => (previewLocale.value === 'zh-CN' ? 'zh-CN' : 'en-US'));
const preview = computed(() => {
  const chosen = model.i18n[previewLocale.value];
  const text = chosen.title.trim() ? chosen : model.i18n[model.sourceLocale];
  return {
    title: text.title || '—',
    subtitle: text.subtitle || null,
    count: items.value.length,
    icons: items.value
      .slice(0, 5)
      .map(item => ({ kind: item.kind, token: item.token, name: item.name, src: item.iconUrl }))
  };
});
const live = computed(() => detail.value?.status === 'published' || detail.value?.status === 'scheduled');
</script>

<template>
  <NSpin :show="loading">
    <NGrid :x-gap="16" :y-gap="16" responsive="screen" item-responsive>
      <NGi span="24 l:16">
        <div class="flex-col gap-16px">
          <NCard :bordered="false" size="small" class="card-wrapper">
            <template #header>
              <div class="flex items-center gap-8px">
                <span>
                  {{ id === null ? $t('page.content.collectionDetail.newTitle') : titleOf() }}
                </span>
                <NTag v-if="detail" size="small" :bordered="false" :type="PUBLISH_STATUS_TAG[detail.status]">
                  {{ $t(`page.shared.publishStatus.${detail.status}`) }}
                </NTag>
                <NText v-if="detail?.publishAt" depth="3" class="text-12px">
                  {{ formatDateTime(detail.publishAt) }}
                </NText>
                <!-- Only published/scheduled collections have a public URL. -->
                <NButton
                  v-if="detail?.webUrl"
                  tag="a"
                  :href="detail.webUrl"
                  target="_blank"
                  rel="noopener noreferrer"
                  size="tiny"
                  quaternary
                  type="primary"
                >
                  {{ $t('page.shared.viewOnSite') }} ↗
                </NButton>
              </div>
            </template>
            <template #header-extra>
              <div class="flex items-center gap-8px">
                <template v-if="detail && hasAuth('content:collection:publish')">
                  <NButton v-if="live" size="small" @click="unpublish">
                    {{ $t('page.content.collection.unpublish') }}
                  </NButton>
                  <template v-else>
                    <NDatePicker
                      v-model:value="scheduleAt"
                      type="datetime"
                      size="small"
                      clearable
                      :placeholder="$t('page.content.collectionDetail.publishAt')"
                      class="w-200px"
                    />
                    <NButton size="small" type="primary" ghost @click="publish(Boolean(scheduleAt))">
                      {{
                        scheduleAt
                          ? $t('page.content.collectionDetail.schedule')
                          : $t('page.content.collectionDetail.publishNow')
                      }}
                    </NButton>
                  </template>
                </template>
                <NButton size="small" type="primary" :loading="saving" @click="save">
                  {{ $t('page.content.collectionDetail.save') }}
                </NButton>
              </div>
            </template>
            <NForm ref="formRef" :model="model" :rules="rules" label-placement="left" :label-width="96">
              <!-- Wrap by card width: with the preview column, three inline fields would truncate dates. -->
              <NGrid :x-gap="16" responsive="self" item-responsive>
                <NFormItemGi span="24 640:12 1024:8" :label="$t('page.content.collectionDetail.slug')" path="slug">
                  <NInput v-model:value="model.slug" :maxlength="64" class="font-mono" />
                </NFormItemGi>
                <NFormItemGi span="24 640:12 1024:7" :label="$t('page.content.collectionDetail.sort')">
                  <NInputNumber v-model:value="model.sort" :min="0" :show-button="false" class="w-full" />
                </NFormItemGi>
                <NFormItemGi span="24 640:12 1024:9" :label="$t('page.content.collectionDetail.unpublishAt')">
                  <NDatePicker v-model:value="model.unpublishAt" type="datetime" clearable class="w-full" />
                </NFormItemGi>
                <NFormItemGi span="24" :label="$t('page.content.collectionDetail.cover')">
                  <div class="flex items-center gap-12px">
                    <NImage v-if="model.coverUrl" :src="model.coverUrl" width="160" height="90" object-fit="cover" />
                    <NUpload
                      accept="image/png,image/jpeg,image/webp"
                      :show-file-list="false"
                      :custom-request="uploadCover"
                      class="w-auto"
                    >
                      <NButton size="small">{{ $t('page.content.collectionDetail.uploadCover') }}</NButton>
                    </NUpload>
                    <NButton
                      v-if="model.coverAssetId"
                      size="small"
                      quaternary
                      type="error"
                      @click="((model.coverAssetId = null), (model.coverUrl = null))"
                    >
                      {{ $t('page.content.collectionDetail.removeCover') }}
                    </NButton>
                  </div>
                </NFormItemGi>
              </NGrid>
              <NFormItem :label="$t('page.shared.i18n.sourceLocale')">
                <div class="w-full flex flex-wrap items-center gap-12px">
                  <NSelect v-model:value="model.sourceLocale" :options="SOURCE_LOCALE_OPTIONS" class="w-200px" />
                  <NText depth="3" class="text-12px">{{ $t('page.shared.i18n.sourceHint') }}</NText>
                </div>
              </NFormItem>
              <NTabs v-model:value="activeLocale" type="line" size="small" class="mt-8px">
                <NTabPane v-for="code in CONTENT_LOCALES" :key="code" :name="code" :tab="tabLabel(code)">
                  <NText v-if="code === 'zh-Hant'" depth="3">{{ $t('page.shared.i18n.derivedHint') }}</NText>
                  <div class="flex-col pt-12px" :data-locale="code">
                    <NFormItem
                      :label="$t('page.content.collectionDetail.titleField')"
                      :path="code === model.sourceLocale ? `i18n.${code}.title` : undefined"
                    >
                      <NInput
                        v-model:value="model.i18n[code].title"
                        :disabled="code === 'zh-Hant'"
                        :maxlength="40"
                        show-count
                        :placeholder="code === model.sourceLocale ? '' : $t('page.shared.i18n.autoPlaceholder')"
                      />
                    </NFormItem>
                    <NFormItem :label="$t('page.content.collectionDetail.subtitle')">
                      <NInput
                        v-model:value="model.i18n[code].subtitle"
                        :disabled="code === 'zh-Hant'"
                        :maxlength="80"
                        show-count
                      />
                    </NFormItem>
                    <NFormItem :label="$t('page.content.articleDetail.seoTitle')">
                      <NInput
                        v-model:value="model.i18n[code].seoTitle"
                        :disabled="code === 'zh-Hant'"
                        :maxlength="120"
                        show-count
                      />
                    </NFormItem>
                    <NFormItem :label="$t('page.content.articleDetail.seoDescription')">
                      <NInput
                        v-model:value="model.i18n[code].seoDescription"
                        :disabled="code === 'zh-Hant'"
                        type="textarea"
                        :maxlength="300"
                        show-count
                      />
                    </NFormItem>
                    <NFormItem :label="$t('page.content.collectionDetail.body')">
                      <MarkdownEditor
                        v-model="model.i18n[code].body"
                        :disabled="code === 'zh-Hant'"
                        :maxlength="10000"
                        :rows="8"
                      />
                    </NFormItem>
                  </div>
                </NTabPane>
              </NTabs>
            </NForm>
          </NCard>
          <NCard :title="$t('page.content.collectionDetail.items')" :bordered="false" size="small" class="card-wrapper">
            <ItemsEditor v-model="items" :source-locale="model.sourceLocale" />
          </NCard>
        </div>
      </NGi>
      <NGi span="24 l:8">
        <NCard
          :title="$t('page.content.collectionDetail.preview')"
          :bordered="false"
          size="small"
          class="card-wrapper sticky top-0"
        >
          <template #header-extra>
            <NSelect v-model:value="previewLocale" :options="CONTENT_LOCALE_OPTIONS" size="small" class="w-160px" />
          </template>
          <UiPreview :locale="previewUiLocale">
            <OnCollectionCard v-bind="preview" href="#" />
          </UiPreview>
        </NCard>
      </NGi>
    </NGrid>
  </NSpin>
</template>
