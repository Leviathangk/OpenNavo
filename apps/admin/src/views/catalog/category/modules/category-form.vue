<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue';
import type { FormInst, FormRules } from 'naive-ui';
import { createCategory, updateCategory } from '@/service/api';
import type { BodyOf, Schemas } from '@/typings/api/opennavo';
import { useCategoryTree } from '@/hooks/business/category';
import { $t } from '@/locales';
import LocalizedFields from '@/components/opennavo/localized-fields.vue';
import type { LocalizedField } from '@/components/opennavo/localized-fields.vue';
import { DEFAULT_SOURCE_LOCALE, blankTexts, fromView, localizedBody } from '@/utils/content-locale';
import type { LocalizedStatuses, LocalizedTexts } from '@/utils/content-locale';

defineOptions({ name: 'CategoryForm' });

type Node = Schemas['AdminCategoryNode'];
type Upsert = BodyOf<'createCategory'>;
type Form = Omit<Upsert, 'i18n'> & { i18n: LocalizedTexts };

// Create/edit category (07 §7.4): pass node for editing, only parentId for a new child.
const props = defineProps<{ node: Node | null; parentId: number | null }>();
const show = defineModel<boolean>('show', { required: true });
const emit = defineEmits<{ saved: [] }>();

const SLUG = /^[a-z0-9][a-z0-9-]*$/;
const ICON = /^lucide:[a-z0-9-]+$/;

const { tree, nameOf } = useCategoryTree();
const formRef = ref<FormInst | null>(null);
const saving = ref(false);

const TEXT_KEYS = ['name', 'description', 'seoTitle', 'seoDescription'] as const;
const fields = computed<LocalizedField[]>(() => [
  { key: 'name', label: $t('page.catalog.category.form.name'), maxlength: 30, required: true },
  { key: 'description', label: $t('page.catalog.category.form.description'), type: 'textarea', maxlength: 200 },
  { key: 'seoTitle', label: $t('page.content.articleDetail.seoTitle'), maxlength: 120 },
  { key: 'seoDescription', label: $t('page.content.articleDetail.seoDescription'), type: 'textarea', maxlength: 300 }
]);

function blank(): Form {
  return {
    parentId: null,
    slug: '',
    icon: 'lucide:',
    appliesTo: 'cask',
    visible: true,
    hiddenByDefault: false,
    sourceLocale: DEFAULT_SOURCE_LOCALE,
    i18n: blankTexts(TEXT_KEYS)
  };
}

const model = reactive<Form>(blank());
// Initial text/statuses: save only source and changed languages.
const original = ref<LocalizedTexts>({});
const statuses = ref<LocalizedStatuses>({});

watch(show, open => {
  if (!open) return;
  const node = props.node;
  if (!node) {
    original.value = {};
    statuses.value = {};
    Object.assign(model, { ...blank(), parentId: props.parentId });
    return;
  }
  const view = fromView(node.i18n, TEXT_KEYS);
  original.value = structuredClone(view.texts);
  statuses.value = view.statuses;
  Object.assign(model, {
    parentId: node.parentId ?? null,
    slug: node.slug,
    icon: node.icon,
    appliesTo: node.appliesTo,
    visible: node.visible,
    hiddenByDefault: node.hiddenByDefault,
    sourceLocale: node.sourceLocale ?? DEFAULT_SOURCE_LOCALE,
    i18n: view.texts
  });
});

// Two levels only: parents must be top-level; exclude self when editing.
const parentOptions = computed(() =>
  tree.value.filter(node => node.id !== props.node?.id).map(node => ({ label: nameOf(node), value: node.id }))
);
const appliesOptions = computed(() =>
  // Cask-only catalog (ADR-018): new categories default to apps; retain legacy command-line category values in the table.
  (['cask', 'both'] as const).map(value => ({
    value,
    label: $t(`page.catalog.category.appliesTo.${value}`)
  }))
);

const required = { required: true, message: $t('page.catalog.category.form.required'), trigger: ['blur', 'input'] };
const rules = computed<FormRules>(() => ({
  slug: [
    required,
    { pattern: SLUG, message: $t('page.catalog.category.form.slugInvalid'), trigger: ['blur', 'input'] }
  ],
  icon: [required, { pattern: ICON, message: $t('page.catalog.category.form.iconInvalid'), trigger: ['blur', 'input'] }]
}));

async function save() {
  await formRef.value?.validate();
  saving.value = true;
  const body: Upsert = {
    ...model,
    slug: model.slug.trim(),
    // Submit only source and changed locales (04 §9.1); server queues remaining translations.
    i18n: localizedBody({
      texts: model.i18n,
      original: original.value,
      sourceLocale: model.sourceLocale,
      keys: TEXT_KEYS,
      entry: text => ({
        name: text.name ?? '',
        description: text.description,
        seoTitle: text.seoTitle,
        seoDescription: text.seoDescription
      })
    })
  };
  const { error } = props.node ? await updateCategory(props.node.id, body) : await createCategory(body);
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
    :title="node ? $t('page.catalog.category.editTitle') : $t('page.catalog.category.addTitle')"
    class="w-640px"
  >
    <NForm ref="formRef" :model="model" :rules="rules" label-placement="left" :label-width="96">
      <NFormItem :label="$t('page.catalog.category.form.parent')" path="parentId">
        <NSelect
          v-model:value="model.parentId"
          :options="parentOptions"
          :placeholder="$t('page.catalog.category.form.parentNone')"
          clearable
        />
      </NFormItem>
      <NFormItem :label="$t('page.catalog.category.form.slug')" path="slug">
        <NInput v-model:value="model.slug" :maxlength="40" class="font-mono" />
      </NFormItem>
      <NFormItem :label="$t('page.catalog.category.form.icon')" path="icon">
        <div class="w-full flex items-center gap-12px">
          <NInput
            v-model:value="model.icon"
            :placeholder="$t('page.catalog.category.form.iconHint')"
            class="font-mono"
          />
          <SvgIcon v-if="/^lucide:[a-z0-9-]+$/.test(model.icon)" :icon="model.icon" class="shrink-0 text-24px" />
        </div>
      </NFormItem>
      <NFormItem :label="$t('page.catalog.category.columns.appliesTo')" path="appliesTo">
        <NRadioGroup v-model:value="model.appliesTo">
          <NRadio v-for="option in appliesOptions" :key="option.value" :value="option.value">{{ option.label }}</NRadio>
        </NRadioGroup>
      </NFormItem>
      <NFormItem :label="$t('page.catalog.category.columns.visible')" path="visible">
        <NSwitch v-model:value="model.visible" />
      </NFormItem>
      <NFormItem :label="$t('page.catalog.category.columns.hiddenByDefault')" path="hiddenByDefault">
        <NSwitch v-model:value="model.hiddenByDefault" />
      </NFormItem>
      <LocalizedFields
        v-model:source-locale="model.sourceLocale"
        v-model:texts="model.i18n"
        :fields="fields"
        :statuses="statuses"
      />
    </NForm>
    <template #footer>
      <div class="flex justify-end gap-12px">
        <NButton @click="show = false">{{ $t('common.cancel') }}</NButton>
        <NButton type="primary" :loading="saving" @click="save">{{ $t('common.confirm') }}</NButton>
      </div>
    </template>
  </NModal>
</template>
