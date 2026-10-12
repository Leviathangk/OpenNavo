<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { formatDate } from '@opennavo/shared';
import { OnButton, OnCard, OnChip, OnSegmented } from '@opennavo/ui';
import SettingRow from './SettingRow.vue';
import type { ProtectRule } from '@/ipc/bindings';
import { useAppLocale } from '@/composables/useAppLocale';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { useSettingsStore } from '@/stores';
import { useCleanupStore } from '@/stores/cleanup';
import { displayPath } from '@/utils/paths';
import { pickFolder } from '@/utils/files';

// Settings → Clean Up (cleanup proposal §04): how review items are removed, and the protected list.
const { t } = useI18n();
const { appLocale } = useAppLocale();
const settings = useSettingsStore();
const store = useCleanupStore();
const toasts = useToasts();
const { errorText } = usePackageState();

const modes = computed(() => [
  { value: 'trash' as const, label: t('uninstall.mode.trash') },
  { value: 'delete' as const, label: t('uninstall.mode.delete') }
]);
const label = (rule: ProtectRule) => (rule.kind === 'path' ? displayPath(rule.value) : rule.value);

async function attempt(operation: () => Promise<unknown>) {
  try {
    await operation();
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}
async function add() {
  const path = await pickFolder();
  if (path) await attempt(() => store.addRule(path));
}
</script>

<template>
  <OnCard v-if="settings.value" padding="none">
    <SettingRow
      :label="t('cleanup.settings.rebuildable')"
      :hint="t('cleanup.settings.rebuildableHint')"
      label-id="setting-cleanupRebuildable"
    >
      <OnChip size="sm">{{ t('cleanup.settings.fixed') }}</OnChip>
    </SettingRow>
    <SettingRow
      :label="t('cleanup.settings.deleteMode')"
      :hint="t('cleanup.settings.deleteModeHint')"
      label-id="setting-cleanupDeleteMode"
    >
      <OnSegmented
        :model-value="settings.value.cleanupDeleteMode"
        :options="modes"
        size="sm"
        :aria-label="t('cleanup.settings.deleteMode')"
        @update:model-value="value => attempt(() => settings.update('cleanupDeleteMode', value))"
      />
    </SettingRow>
  </OnCard>

  <h2 class="m-0 mb-10px mt-24px text-14px font-600 text-ink-primary">
    {{ t('cleanup.settings.protected') }}
  </h2>
  <OnCard padding="none">
    <ul class="m-0 list-none p-0">
      <li
        v-for="rule in store.rules"
        :key="rule.id"
        class="flex items-center gap-10px border-b border-b-solid border-line-subtle py-10px pl-18px pr-12px"
      >
        <span class="min-w-0 flex-1">
          <span class="block truncate font-mono text-12.5px text-ink-primary" :title="rule.value">{{
            label(rule)
          }}</span>
          <span class="block text-11.5px text-ink-tertiary">{{
            t('cleanup.settings.added', { date: formatDate(rule.createdAt, { locale: appLocale }) })
          }}</span>
        </span>
        <OnButton variant="ghost" size="xs" @click="attempt(() => store.removeRule(rule))">{{
          t('cleanup.settings.remove')
        }}</OnButton>
      </li>
    </ul>
    <div class="flex items-center gap-12px px-18px py-12px">
      <OnButton variant="secondary" size="sm" icon="plus" @click="add">{{ t('cleanup.settings.add') }}</OnButton>
      <span v-if="!store.rules.length" class="text-12px text-ink-tertiary">{{ t('cleanup.settings.empty') }}</span>
    </div>
  </OnCard>
  <p class="m-0 mt-10px text-12px leading-[1.55] text-ink-tertiary">
    {{ t('cleanup.settings.always') }}
  </p>
</template>
