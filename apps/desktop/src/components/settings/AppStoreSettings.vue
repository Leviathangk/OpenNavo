<script setup lang="ts">
import { computed, onMounted } from 'vue';
import { useI18n } from 'vue-i18n';
import { OnButton, OnCard, OnMenu, OnToggle } from '@opennavo/ui';
import SettingRow from './SettingRow.vue';
import type { AppSettings } from '@/ipc/client';
import { masRegionName } from '@/components/appstore/meta';
import { useAppLocale } from '@/composables/useAppLocale';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { useSettingsStore } from '@/stores';
import { useMasStore } from '@/stores/mas';

// Settings → App Store (store/security plan §5): storefront region, App Store results in search, update checks.
// The region decides search results, prices and availability; Auto follows the App Store account, then the system.
const { t } = useI18n();
const { appLocale } = useAppLocale();
const settings = useSettingsStore();
const mas = useMasStore();
const toasts = useToasts();
const { errorText } = usePackageState();

// Storefronts people in OpenNavo's six languages use most; any other comes from Auto.
const COMMON = ['cn', 'hk', 'tw', 'us', 'jp', 'gb', 'de', 'fr', 'es', 'br', 'pt', 'ru', 'ca', 'au', 'sg', 'kr'];

onMounted(() => void mas.loadStatus().catch(() => undefined));

const current = computed(() => settings.value?.appStoreRegion ?? 'auto');
const autoLabel = computed(() =>
  mas.status && current.value === 'auto'
    ? t('appStore.settings.autoDetected', { region: masRegionName(mas.status.country, appLocale.value) })
    : t('appStore.settings.auto')
);
const items = computed(() => [
  { key: 'auto', label: autoLabel.value },
  { key: 'separator', label: '', separator: true },
  ...COMMON.map(code => ({ key: code, label: masRegionName(code, appLocale.value) }))
]);
const label = computed(() =>
  current.value === 'auto' ? autoLabel.value : masRegionName(current.value, appLocale.value)
);

async function save<K extends 'appStoreRegion' | 'appStoreSearch' | 'updateSources'>(key: K, value: AppSettings[K]) {
  try {
    await settings.update(key, value);
    if (key === 'appStoreRegion') {
      // Another storefront means other results and prices: drop everything cached from the old one.
      mas.reset();
      await mas.loadStatus().catch(() => undefined);
    }
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}
</script>

<template>
  <OnCard padding="none">
    <SettingRow
      :label="t('appStore.settings.region')"
      :hint="t('appStore.settings.regionHint')"
      label-id="setting-appStoreRegion"
    >
      <OnMenu
        :label="t('appStore.settings.region')"
        :items="items"
        align="end"
        @select="key => save('appStoreRegion', key)"
      >
        <template #trigger="{ attrs }">
          <OnButton v-bind="attrs" size="sm" icon="globe">{{ label }}</OnButton>
        </template>
      </OnMenu>
    </SettingRow>
    <SettingRow
      :label="t('appStore.settings.search')"
      :hint="t('appStore.settings.searchHint')"
      label-id="setting-appStoreSearch"
    >
      <OnToggle
        :model-value="settings.value?.appStoreSearch ?? true"
        aria-labelledby="setting-appStoreSearch"
        @update:model-value="value => save('appStoreSearch', value)"
      />
    </SettingRow>
    <SettingRow
      :label="t('appStore.settings.updates')"
      :hint="t('appStore.settings.updatesHint')"
      label-id="setting-appStoreUpdates"
    >
      <OnToggle
        :model-value="settings.value?.updateSources.mas ?? true"
        aria-labelledby="setting-appStoreUpdates"
        @update:model-value="value => save('updateSources', { ...settings.value?.updateSources, mas: value })"
      />
    </SettingRow>
  </OnCard>
  <p class="m-0 mt-12px text-12px leading-[1.6] text-ink-tertiary">{{ t('appStore.settings.privacy') }}</p>
</template>
