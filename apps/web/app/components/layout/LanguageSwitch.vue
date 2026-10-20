<script setup lang="ts">
import { SITE_LOCALES, LOCALE_METADATA } from '@opennavo/shared';
import type { SiteLocale } from '@opennavo/shared';
import { OnIcon, OnMenu } from '@opennavo/ui';

withDefaults(defineProps<{ align?: 'start' | 'end' }>(), { align: 'end' });

const { t, locale, setLocale } = useI18n();
// Both Chinese scripts sit together; the rest follow their English names.
const ORDER: readonly SiteLocale[] = [
  'en-US',
  'zh-CN',
  'zh-Hant',
  'ja-JP',
  'de-DE',
  'es-ES',
  'fr-FR',
  'it-IT',
  'pt-BR',
  'ru-RU'
];
const items = [...SITE_LOCALES]
  .sort((a, b) => ORDER.indexOf(a) - ORDER.indexOf(b))
  .map(code => ({ key: toRouteLocale(code), label: LOCALE_METADATA[code].name }));
const currentName = computed(() => LOCALE_METADATA[toAppLocale(locale.value)].name);
const chosen = useState('onv-language-choice', () => false);

async function select(code: string) {
  const target = items.find(item => item.key === code);
  if (target) {
    await setLocale(target.key as typeof locale.value);
    saveLanguagePreference(LANGUAGE_CHOICE_KEY, toAppLocale(target.key));
    chosen.value = true;
  }
}
</script>

<template>
  <OnMenu :align="align" :items="items" :label="t('nav.switchLanguageLabel')" @select="select">
    <template #trigger="{ attrs }">
      <button
        v-bind="attrs"
        type="button"
        :aria-label="`${t('nav.switchLanguageLabel')}: ${currentName}`"
        class="inline-flex h-32px shrink-0 items-center gap-6px rounded-default px-8px text-13px text-ink-secondary outline-none transition-colors duration-fast hover:bg-surface-raised hover:text-ink-primary focus-visible:shadow-focus-ring"
      >
        <OnIcon name="globe" :size="15" />
        {{ currentName }}
      </button>
    </template>
  </OnMenu>
</template>
