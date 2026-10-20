<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { fixTranslation } from '@/service/api';
import type { RecordOf, Schemas } from '@/typings/api/opennavo';
import { APP_CONTENT_LOCALES, CONTENT_LOCALES, localeName } from '@/utils/content-locale';
import type { ContentLocale } from '@/utils/content-locale';
import { $t } from '@/locales';

defineOptions({ name: 'TranslationFixDrawer' });

type Item = RecordOf<'listTranslations'>;

// Correct one translation (M9-02 fixTranslation), marking it manual without affecting others.
// Edit source through its entity editor; structured fields such as grouped highlights are read-only here and edited in the release list.
const props = defineProps<{ item: Item | null }>();
const show = defineModel<boolean>('show', { required: true });
const emit = defineEmits<{ saved: [] }>();

const TEXT_LONG = new Set(['description', 'body', 'notes', 'bodyMarkdown', 'note', 'caption']);
const locale = ref<ContentLocale>('en-US');
const values = ref<Record<string, string>>({});
const saving = ref(false);

const targets = computed(() =>
  props.item
    ? (props.item.ref.entity === 'desktop_release' ? APP_CONTENT_LOCALES : CONTENT_LOCALES).filter(
        code => code !== props.item!.sourceLocale
      )
    : []
);
const localeOptions = computed(() => targets.value.map(code => ({ value: code, label: localeName(code) })));
/** List only source fields with text; do not edit structured fields such as sections here. */
const textKeys = computed(() => {
  if (!props.item) return [];
  const source = (props.item.i18n[props.item.sourceLocale]?.fields ?? {}) as Record<string, unknown>;
  return Object.keys(source).filter(key => typeof source[key] === 'string' && source[key]);
});

function load() {
  if (!props.item) return;
  const fields = (props.item.i18n[locale.value]?.fields ?? {}) as Record<string, unknown>;
  values.value = Object.fromEntries(
    textKeys.value.map(key => [key, typeof fields[key] === 'string' ? (fields[key] as string) : ''])
  );
}

watch(
  show,
  open => {
    if (!open || !props.item) return;
    const failed = targets.value.find(code =>
      ['failed', 'missing'].includes(props.item!.i18n[code]?.status ?? 'missing')
    );
    locale.value = failed ?? targets.value[0] ?? 'en-US';
    load();
  },
  { immediate: true }
);
watch(locale, load);

const sourceText = (key: string) => {
  const fields = props.item?.i18n[props.item.sourceLocale]?.fields as Record<string, unknown> | undefined;
  return typeof fields?.[key] === 'string' ? (fields[key] as string) : '';
};

const fieldLabel = (key: string) => {
  const text = $t(`page.changelog.translations.fields.${key}` as App.I18n.I18nKey);
  return text.startsWith('page.') ? key : text;
};

async function save() {
  if (!props.item || !targets.value.includes(locale.value)) return;
  saving.value = true;
  const fields = Object.fromEntries(
    textKeys.value.map(key => [key, values.value[key].trim() || null])
  ) as Schemas['ContentFields'];
  const { error } = await fixTranslation({ ref: props.item.ref, locale: locale.value, fields });
  saving.value = false;
  if (error) return;
  window.$message?.success($t('page.changelog.translations.fixed'));
  show.value = false;
  emit('saved');
}
</script>

<template>
  <NDrawer v-model:show="show" :width="820" placement="right">
    <NDrawerContent v-if="item" :title="$t('page.changelog.translations.fixTitle', { label: item.label })" closable>
      <div class="flex-col gap-16px">
        <div class="flex items-center gap-12px">
          <NText depth="3">{{ $t('page.changelog.translations.fixLocale') }}</NText>
          <NSelect v-model:value="locale" :options="localeOptions" class="w-200px" />
          <NText depth="3" class="text-12px">{{ $t('page.changelog.translations.fixHint') }}</NText>
        </div>
        <NEmpty v-if="!textKeys.length" :description="$t('page.changelog.translations.noTextFields')" />
        <div v-for="key in textKeys" :key="key" class="grid grid-cols-2 gap-12px lt-sm:grid-cols-1">
          <div class="flex-col gap-4px">
            <NText depth="3" class="text-12px">{{ fieldLabel(key) }} · {{ localeName(item.sourceLocale) }}</NText>
            <div class="whitespace-pre-wrap rounded-small bg-layout p-8px text-13px">{{ sourceText(key) }}</div>
          </div>
          <div class="flex-col gap-4px">
            <NText depth="3" class="text-12px">{{ fieldLabel(key) }} · {{ localeName(locale) }}</NText>
            <NInput
              v-model:value="values[key]"
              :type="TEXT_LONG.has(key) ? 'textarea' : 'text'"
              :rows="TEXT_LONG.has(key) ? 6 : 1"
            />
          </div>
        </div>
      </div>
      <template #footer>
        <div class="flex justify-end gap-12px">
          <NButton @click="show = false">{{ $t('common.cancel') }}</NButton>
          <NButton type="primary" :loading="saving" :disabled="!textKeys.length" @click="save">
            {{ $t('page.changelog.translations.saveFix') }}
          </NButton>
        </div>
      </template>
    </NDrawerContent>
  </NDrawer>
</template>
