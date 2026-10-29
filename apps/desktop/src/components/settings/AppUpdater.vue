<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { formatPercent } from '@opennavo/shared';
import { OnButton } from '@opennavo/ui';
import { useAppLocale } from '@/composables/useAppLocale';
import { useAppUpdaterStore } from '@/stores/appUpdater';
import SettingRow from './SettingRow.vue';

const props = defineProps<{ currentVersion: string | null }>();
const { t } = useI18n();
const { appLocale } = useAppLocale();
const updater = useAppUpdaterStore();
const hint = computed(() => {
  switch (updater.state) {
    case 'checking':
      return t('updates.checking');
    case 'latest':
      return t('updater.latest');
    case 'available':
      return t('updater.available', { version: updater.update?.version ?? '' });
    case 'downloading':
      return updater.percent === null
        ? t('updater.downloadingUnknown')
        : t('updater.downloading', {
            percent: formatPercent(updater.percent / 100, { locale: appLocale.value })
          });
    case 'installed':
      return updater.busy ? t('updater.busy') : t('updater.installed');
    case 'failed':
      return t('updater.failed');
    default:
      return props.currentVersion ? t('updater.current', { version: props.currentVersion }) : undefined;
  }
});

async function openOrRestart() {
  if (updater.state !== 'installed') {
    updater.show();
    return;
  }
  await updater.restart();
}
</script>

<template>
  <SettingRow :label="t('updater.title')" :hint="hint">
    <OnButton
      v-if="updater.update"
      variant="primary"
      size="sm"
      :loading="updater.state === 'downloading' || updater.restarting"
      :disabled="updater.state === 'installed' && updater.busy"
      @click="openOrRestart"
    >
      {{ updater.state === 'installed' ? t('updater.restart') : t('updater.viewUpdate') }}
    </OnButton>
    <OnButton
      v-else
      variant="secondary"
      size="sm"
      icon="refresh-cw"
      :loading="updater.working"
      @click="updater.checkNow(false)"
    >
      {{ t('updater.check') }}
    </OnButton>
  </SettingRow>
</template>
