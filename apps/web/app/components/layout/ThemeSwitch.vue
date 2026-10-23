<script setup lang="ts">
import { OnIcon, OnMenu, THEME_PREFERENCES, isThemePreference } from '@opennavo/ui';
import type { ThemePreference } from '@opennavo/ui';

// Color theme menu (08 §3.7) beside the language switch: System, Light, Dark, with the current choice checked.
withDefaults(defineProps<{ align?: 'start' | 'end' }>(), { align: 'end' });

const { t } = useI18n();
const { preference, set } = useThemePreference();
const ICONS: Record<ThemePreference, string> = { system: 'sun-moon', light: 'sun', dark: 'moon' };
const LABELS: Record<ThemePreference, string> = {
  system: 'nav.themeSystem',
  light: 'nav.themeLight',
  dark: 'nav.themeDark'
};
const items = computed(() =>
  THEME_PREFERENCES.map(key => ({ key, label: t(LABELS[key]), icon: ICONS[key], checked: preference.value === key }))
);

function select(key: string) {
  if (isThemePreference(key)) set(key);
}
</script>

<template>
  <OnMenu :align="align" :items="items" :label="t('nav.themeLabel')" @select="select">
    <template #trigger="{ attrs }">
      <button
        v-bind="attrs"
        type="button"
        :aria-label="`${t('nav.themeLabel')}: ${t(LABELS[preference])}`"
        class="inline-flex h-32px w-32px shrink-0 items-center justify-center rounded-default text-ink-secondary outline-none transition-colors duration-fast hover:bg-surface-raised hover:text-ink-primary focus-visible:shadow-focus-ring"
      >
        <OnIcon :name="ICONS[preference]" :size="16" />
      </button>
    </template>
  </OnMenu>
</template>
