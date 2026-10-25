<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { OnButton, OnCard, OnConfirm, OnToggle } from '@opennavo/ui';
import type { FirewallSettings } from '@/ipc/bindings';
import SettingRow from '@/components/settings/SettingRow.vue';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { useFirewallStore } from '@/stores/firewall';

// Firewall settings (firewall plan §4.7): the extension keeps them, so they apply while OpenNavo is closed too.
// Removing the firewall turns filtering off and uninstalls the system extension; macOS may ask for a password.
const { t } = useI18n();
const toasts = useToasts();
const { errorText } = usePackageState();
const store = useFirewallStore();

const settings = computed(() => store.settings);
const confirmRemove = ref(false);
/** Only a removal in flight spins; the button also waits while an on/off change is being applied. */
const removing = ref(false);

onUnmounted(store.follow('settings'));
onMounted(() => void store.loadSettings().catch(() => undefined));

async function set<K extends 'allowApple' | 'allowInstalled'>(key: K, value: FirewallSettings[K]) {
  if (!settings.value) return;
  try {
    await store.saveSettings({ ...settings.value, [key]: value });
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}

async function remove() {
  removing.value = true;
  try {
    await store.uninstall();
    confirmRemove.value = false;
  } catch (error) {
    confirmRemove.value = false;
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  } finally {
    removing.value = false;
  }
}
</script>

<template>
  <div class="flex flex-col">
    <h2 class="m-0 mb-10px text-14px font-600 text-ink-primary">{{ t('firewall.settings.automatic') }}</h2>
    <OnCard padding="none">
      <SettingRow
        :label="t('firewall.options.allowApple')"
        :hint="t('firewall.options.allowAppleHint')"
        label-id="firewall-allowApple"
      >
        <OnToggle
          :model-value="settings?.allowApple ?? false"
          :disabled="!settings"
          aria-labelledby="firewall-allowApple"
          @update:model-value="(on: boolean) => set('allowApple', on)"
        />
      </SettingRow>
      <SettingRow
        :label="t('firewall.options.allowInstalled')"
        :hint="t('firewall.settings.allowInstalledHint')"
        label-id="firewall-allowInstalled"
      >
        <OnToggle
          :model-value="settings?.allowInstalled ?? false"
          :disabled="!settings"
          aria-labelledby="firewall-allowInstalled"
          @update:model-value="(on: boolean) => set('allowInstalled', on)"
        />
      </SettingRow>
    </OnCard>
    <p class="m-0 mt-8px text-12px leading-[1.55] text-ink-tertiary">{{ t('firewall.settings.closedHint') }}</p>

    <h2 class="m-0 mb-10px mt-24px text-14px font-600 text-ink-primary">{{ t('firewall.settings.remove') }}</h2>
    <OnCard padding="none">
      <SettingRow :label="t('firewall.settings.removeLabel')" :hint="t('firewall.settings.removeHint')">
        <OnButton
          variant="danger"
          size="sm"
          :loading="removing"
          :disabled="store.switching"
          @click="confirmRemove = true"
          >{{ t('firewall.settings.removeAction') }}</OnButton
        >
      </SettingRow>
    </OnCard>

    <OnConfirm
      :open="confirmRemove"
      :title="t('firewall.settings.removeTitle')"
      :description="t('firewall.settings.removeBody')"
      :confirm-label="t('firewall.settings.removeAction')"
      tone="danger"
      :loading="removing"
      @confirm="remove"
      @cancel="confirmRemove = false"
    />
  </div>
</template>
