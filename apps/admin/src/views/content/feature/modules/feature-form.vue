<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue';
import type { FormInst, FormItemRule, FormRules } from 'naive-ui';
import { OnFeatureHero } from '@opennavo/ui';
import { createFeature, fetchCollectionList, fetchFeature, updateFeature } from '@/service/api';
import type { BodyOf, RecordOf, Schemas } from '@/typings/api/opennavo';
import { useCategoryTree } from '@/hooks/business/category';
import { useLocalizedPackage } from '@/hooks/business/localized';
import PackagePicker from '@/components/opennavo/package-picker.vue';
import UiPreview from '@/components/opennavo/ui-preview.vue';
import { useAppStore } from '@/store/modules/app';
import {
  CONTENT_LOCALES,
  CONTENT_LOCALE_OPTIONS,
  SOURCE_LOCALE_OPTIONS,
  DEFAULT_SOURCE_LOCALE,
  fromView,
  localeName,
  localizedBody,
  pickLocalized
} from '@/utils/content-locale';
import type { ContentLocale, LocalizedStatuses, LocalizedTexts } from '@/utils/content-locale';
import { $t } from '@/locales';

defineOptions({ name: 'FeatureForm' });

type Upsert = BodyOf<'createFeature'>;
type Copy = Record<(typeof TEXT_KEYS)[number], string>;

// Feature form (07 §7.6): position, required category for category headers, target, glow color, source copy with correctable AI translations,
// contract length limits and bold-only body, schedule, state, order; live public OnFeatureHero preview on right.
const props = defineProps<{ featureId: number | null }>();
const show = defineModel<boolean>('show', { required: true });
const emit = defineEmits<{ saved: [] }>();

const TEXT_KEYS = ['badge', 'title', 'subtitle', 'body', 'ctaLabel'] as const;
const appStore = useAppStore();
const { nameOf } = useLocalizedPackage();
const formRef = ref<FormInst | null>(null);
const saving = ref(false);
const previewLocale = ref<ContentLocale>(DEFAULT_SOURCE_LOCALE);
const collections = ref<{ label: string; value: number }[]>([]);
const pickedPackage = ref<{ name: string; kind: Schemas['PackageKind']; token: string; iconUrl: string | null } | null>(
  null
);
const { options: categoryOptions } = useCategoryTree();

// String fields for all six locales simplify input binding; treat empty strings as absent on save.
function fullCopy(texts: LocalizedTexts): Record<ContentLocale, Copy> {
  return Object.fromEntries(
    CONTENT_LOCALES.map(code => [code, Object.fromEntries(TEXT_KEYS.map(key => [key, texts[code]?.[key] ?? '']))])
  ) as Record<ContentLocale, Copy>;
}

// Loaded copy/statuses: submit only source and changed languages (04 §9.1).
const original = ref<LocalizedTexts>({});
const statuses = ref<LocalizedStatuses>({});
// Open on the source-language tab.
const activeLocale = ref<ContentLocale>(DEFAULT_SOURCE_LOCALE);
const model = reactive({
  placement: 'home_hero' as Schemas['FeaturePlacement'],
  categoryId: null as number | null,
  targetType: 'package' as Upsert['targetType'],
  packageId: null as number | null,
  collectionId: null as number | null,
  url: '',
  glowColor: null as string | null,
  status: 'draft' as Schemas['PublishStatus'],
  range: null as [number, number] | null,
  sort: 0,
  sourceLocale: DEFAULT_SOURCE_LOCALE as ContentLocale,
  copy: fullCopy({})
});

watch(show, async open => {
  if (!open) return;
  pickedPackage.value = null;
  if (!collections.value.length) {
    const { data, error } = await fetchCollectionList({ current: 1, size: 100 });
    if (!error)
      collections.value = data.records.map(item => ({
        label: pickLocalized(item.i18n, appStore.locale, item.sourceLocale)?.title ?? item.slug,
        value: item.id
      }));
  }
  if (props.featureId === null) {
    original.value = {};
    statuses.value = {};
    Object.assign(model, {
      placement: 'home_hero',
      categoryId: null,
      targetType: 'package',
      packageId: null,
      collectionId: null,
      url: '',
      glowColor: null,
      status: 'draft',
      range: null,
      sort: 0,
      sourceLocale: DEFAULT_SOURCE_LOCALE,
      copy: fullCopy({})
    });
    activeLocale.value = DEFAULT_SOURCE_LOCALE;
    return;
  }
  const { data, error } = await fetchFeature(props.featureId);
  if (error) return;
  const view = fromView(data.i18n, TEXT_KEYS);
  original.value = view.texts;
  statuses.value = view.statuses;
  Object.assign(model, {
    placement: data.placement,
    categoryId: data.categoryId ?? null,
    targetType: data.targetType,
    packageId: data.packageId ?? null,
    collectionId: data.collectionId ?? null,
    url: data.url ?? '',
    glowColor: data.glowColor ?? null,
    status: data.status,
    range: data.startsAt && data.endsAt ? [new Date(data.startsAt).getTime(), new Date(data.endsAt).getTime()] : null,
    sort: data.sort,
    sourceLocale: data.sourceLocale ?? DEFAULT_SOURCE_LOCALE,
    copy: fullCopy(view.texts)
  });
  previewLocale.value = model.sourceLocale;
  activeLocale.value = model.sourceLocale;
  if (data.packageId && data.packageName)
    pickedPackage.value = { name: data.packageName, kind: 'cask', token: '', iconUrl: null };
});

function pick(row: RecordOf<'listAdminPackages'>) {
  model.packageId = row.id;
  pickedPackage.value = {
    name: nameOf(row),
    kind: row.kind,
    token: row.token,
    iconUrl: row.iconUrl ?? null
  };
}

// Body permits only **bold**; after removing paired bold markers, no other Markdown syntax may remain.
function plainExceptBold(value: string) {
  return !/[*_`[\]<>#]/.test(value.replace(/\*\*[^*\n]+\*\*/g, ''));
}

const placementOptions = computed(() =>
  (['home_hero', 'home_secondary', 'category_top'] as const).map(value => ({
    value,
    label: $t(`page.content.feature.placements.${value}`)
  }))
);
const statusOptions = computed(() =>
  (['draft', 'scheduled', 'published', 'archived'] as const).map(value => ({
    value,
    label: $t(`page.shared.publishStatus.${value}`)
  }))
);

const rules = computed<FormRules>(() => ({
  categoryId: {
    validator: (_rule: FormItemRule, value: number | null) => model.placement !== 'category_top' || Boolean(value),
    message: $t('page.content.feature.categoryRequired'),
    trigger: ['change', 'blur']
  },
  target: {
    validator: () =>
      model.targetType === 'package'
        ? Boolean(model.packageId)
        : model.targetType === 'collection'
          ? Boolean(model.collectionId)
          : /^https?:\/\//.test(model.url.trim()),
    message: $t('page.content.feature.targetRequired'),
    trigger: ['change', 'blur']
  },
  // Only source title is required; every language's body permits bold only.
  [`copy.${model.sourceLocale}.title`]: { required: true, message: $t('form.required'), trigger: ['blur', 'input'] },
  ...Object.fromEntries(
    CONTENT_LOCALES.map(code => [
      `copy.${code}.body`,
      {
        validator: (_rule: FormItemRule, value: string) => plainExceptBold(value ?? ''),
        message: $t('page.content.feature.bodyInvalid'),
        trigger: ['blur', 'input']
      }
    ])
  )
}));

function tabLabel(code: ContentLocale) {
  if (code === model.sourceLocale) return `${localeName(code)} · ${$t('page.shared.translationStatus.source')}`;
  const status = statuses.value[code];
  return status ? `${localeName(code)} · ${$t(`page.shared.translationStatus.${status}`)}` : localeName(code);
}

async function save() {
  await formRef.value?.validate();
  const body: Upsert = {
    placement: model.placement,
    categoryId: model.placement === 'category_top' ? model.categoryId : null,
    targetType: model.targetType,
    packageId: model.targetType === 'package' ? model.packageId : null,
    collectionId: model.targetType === 'collection' ? model.collectionId : null,
    url: model.targetType === 'url' ? model.url.trim() : null,
    glowColor: model.glowColor,
    status: model.status,
    startsAt: model.range ? new Date(model.range[0]).toISOString() : null,
    endsAt: model.range ? new Date(model.range[1]).toISOString() : null,
    sort: model.sort,
    sourceLocale: model.sourceLocale,
    // Non-source feature languages accept null to clear translations (04 §9.1).
    i18n: localizedBody({
      texts: model.copy,
      original: original.value,
      sourceLocale: model.sourceLocale,
      keys: TEXT_KEYS,
      entry: text => ({
        badge: text.badge,
        title: text.title ?? '',
        subtitle: text.subtitle,
        body: text.body,
        ctaLabel: text.ctaLabel
      }),
      clearable: true
    })
  };
  saving.value = true;
  const { error } = props.featureId === null ? await createFeature(body) : await updateFeature(props.featureId, body);
  saving.value = false;
  if (error) return;
  window.$message?.success($t('page.shared.saved'));
  show.value = false;
  emit('saved');
}

// Preview wrapper currently maps Chinese to Chinese and other locales to English.
const previewUiLocale = computed(() => (previewLocale.value === 'zh-CN' ? 'zh-CN' : 'en-US'));
const preview = computed(() => {
  const chosen = model.copy[previewLocale.value];
  const copy = chosen.title.trim() ? chosen : model.copy[model.sourceLocale];
  const icon = pickedPackage.value?.token
    ? [
        {
          kind: pickedPackage.value.kind,
          token: pickedPackage.value.token,
          name: pickedPackage.value.name,
          src: pickedPackage.value.iconUrl
        }
      ]
    : [];
  return {
    badge: copy.badge || undefined,
    title: copy.title || '—',
    subtitle: copy.subtitle || undefined,
    description: copy.body || undefined,
    actionLabel: copy.ctaLabel || undefined,
    glowColor: model.glowColor,
    icons: icon
  };
});
</script>

<template>
  <NModal
    v-model:show="show"
    preset="card"
    :title="featureId === null ? $t('page.content.feature.create') : $t('page.content.feature.editTitle')"
    class="w-1180px max-w-[96vw]"
  >
    <NGrid :x-gap="24" responsive="screen" item-responsive>
      <NGi span="24 l:13">
        <NForm ref="formRef" :model="model" :rules="rules" label-placement="left" :label-width="96">
          <NFormItem :label="$t('page.content.feature.placement')" path="placement">
            <NSelect v-model:value="model.placement" :options="placementOptions" />
          </NFormItem>
          <NFormItem
            v-if="model.placement === 'category_top'"
            :label="$t('page.content.feature.category')"
            path="categoryId"
          >
            <NTreeSelect v-model:value="model.categoryId" :options="categoryOptions" filterable clearable />
          </NFormItem>
          <NFormItem :label="$t('page.content.feature.targetType')">
            <NRadioGroup v-model:value="model.targetType">
              <NRadio v-for="type in ['package', 'collection', 'url'] as const" :key="type" :value="type">
                {{ $t(`page.content.feature.targetTypes.${type}`) }}
              </NRadio>
            </NRadioGroup>
          </NFormItem>
          <NFormItem :label="$t('page.content.feature.target')" path="target">
            <div v-if="model.targetType === 'package'" class="w-full flex items-center gap-12px">
              <PackagePicker :placeholder="$t('page.content.feature.packageSearch')" class="flex-1" @pick="pick" />
              <NTag v-if="pickedPackage" :bordered="false" type="primary">{{ pickedPackage.name }}</NTag>
            </div>
            <NSelect
              v-else-if="model.targetType === 'collection'"
              v-model:value="model.collectionId"
              :options="collections"
              :placeholder="$t('page.content.feature.collectionSelect')"
              filterable
            />
            <NInput v-else v-model:value="model.url" :placeholder="$t('page.content.feature.url')" />
          </NFormItem>
          <NFormItem :label="$t('page.content.feature.glowColor')">
            <NColorPicker
              v-model:value="model.glowColor"
              :modes="['hex']"
              :show-alpha="false"
              :actions="['clear']"
              class="w-200px"
            />
          </NFormItem>
          <NFormItem :label="$t('page.content.feature.range')">
            <NDatePicker v-model:value="model.range" type="datetimerange" clearable class="w-full" />
          </NFormItem>
          <div class="grid grid-cols-[repeat(2,minmax(0,1fr))] gap-x-16px">
            <NFormItem :label="$t('page.content.feature.status')">
              <NSelect v-model:value="model.status" :options="statusOptions" />
            </NFormItem>
            <NFormItem :label="$t('page.content.feature.sort')">
              <NInputNumber v-model:value="model.sort" :min="0" class="w-full" />
            </NFormItem>
          </div>
          <NFormItem :label="$t('page.shared.i18n.sourceLocale')">
            <div class="w-full flex flex-wrap items-center gap-12px">
              <NSelect v-model:value="model.sourceLocale" :options="SOURCE_LOCALE_OPTIONS" class="w-200px" />
              <NText depth="3" class="text-12px">{{ $t('page.shared.i18n.sourceHint') }}</NText>
            </div>
          </NFormItem>
          <NTabs v-model:value="activeLocale" type="line" size="small">
            <NTabPane v-for="code in CONTENT_LOCALES" :key="code" :name="code" :tab="tabLabel(code)">
              <NText v-if="code === 'zh-Hant'" depth="3">{{ $t('page.shared.i18n.derivedHint') }}</NText>
              <div class="pt-12px">
                <NFormItem :label="$t('page.content.feature.badge')">
                  <NInput
                    :disabled="code === 'zh-Hant'"
                    v-model:value="model.copy[code].badge"
                    :maxlength="12"
                    show-count
                  />
                </NFormItem>
                <NFormItem
                  :label="$t('page.content.feature.featureTitle')"
                  :path="code === model.sourceLocale ? `copy.${code}.title` : undefined"
                >
                  <NInput
                    :disabled="code === 'zh-Hant'"
                    v-model:value="model.copy[code].title"
                    :maxlength="30"
                    show-count
                    :placeholder="code === model.sourceLocale ? '' : $t('page.shared.i18n.autoPlaceholder')"
                  />
                </NFormItem>
                <NFormItem :label="$t('page.content.feature.subtitle')">
                  <NInput
                    :disabled="code === 'zh-Hant'"
                    v-model:value="model.copy[code].subtitle"
                    :maxlength="30"
                    show-count
                  />
                </NFormItem>
                <NFormItem :label="$t('page.content.feature.body')" :path="`copy.${code}.body`">
                  <NInput
                    :disabled="code === 'zh-Hant'"
                    v-model:value="model.copy[code].body"
                    type="textarea"
                    :rows="3"
                    :maxlength="120"
                    show-count
                    :placeholder="$t('page.content.feature.bodyHint')"
                  />
                </NFormItem>
                <NFormItem :label="$t('page.content.feature.ctaLabel')">
                  <NInput
                    :disabled="code === 'zh-Hant'"
                    v-model:value="model.copy[code].ctaLabel"
                    :maxlength="10"
                    show-count
                  />
                </NFormItem>
              </div>
            </NTabPane>
          </NTabs>
        </NForm>
      </NGi>
      <NGi span="24 l:11">
        <div class="sticky top-0 flex-col gap-8px">
          <div class="flex items-center justify-between">
            <span class="font-600">{{ $t('page.content.feature.preview') }}</span>
            <NSelect v-model:value="previewLocale" :options="CONTENT_LOCALE_OPTIONS" size="small" class="w-160px" />
          </div>
          <UiPreview :locale="previewUiLocale">
            <OnFeatureHero v-bind="preview" />
          </UiPreview>
        </div>
      </NGi>
    </NGrid>
    <template #footer>
      <div class="flex justify-end gap-12px">
        <NButton @click="show = false">{{ $t('common.cancel') }}</NButton>
        <NButton type="primary" :loading="saving" @click="save">{{ $t('common.confirm') }}</NButton>
      </div>
    </template>
  </NModal>
</template>
