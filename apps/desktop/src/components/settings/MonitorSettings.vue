<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { OnButton, OnCard, OnChip, OnIcon, OnSegmented, OnToggle } from '@opennavo/ui';
import type { MenuBarStyle } from '@/ipc/bindings';
import MenuBarStylePicker from '@/components/monitor/MenuBarStylePicker.vue';
import { MODULE_ICONS } from '@/components/monitor/meta';
import { useHelperApproval } from '@/components/monitor/useHelperApproval';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { useSettingsStore } from '@/stores';
import { useMonitorStore } from '@/stores/monitor';
import type { DisplayModule } from '@/stores/monitor';
import SettingRow from './SettingRow.vue';

// Settings → Monitoring (§12, §14): a row per menu bar item (unlimited), refresh rate, and the fan helper.
const { t } = useI18n();
const settings = useSettingsStore();
const store = useMonitorStore();
const toasts = useToasts();
const { errorText } = usePackageState();
const busy = ref(false);

const INTERVALS = [1000, 2000, 5000] as const;
const intervals = computed(() =>
  INTERVALS.map(value => ({
    value: String(value),
    label: t('monitor.settings.seconds', { count: value / 1000 }, { plural: value / 1000 })
  }))
);
const interval = (value: string) => INTERVALS.find(entry => String(entry) === value) ?? 2000;
const helper = computed(() => store.helper);
/** No helper to offer on chips without fan control; an installed one can still be removed. */
const fanLocked = computed(
  () => helper.value != null && !helper.value.manualAllowed && helper.value.status !== 'enabled'
);
useHelperApproval();

onMounted(() => {
  void store.loadAvailable().catch(() => undefined);
  void store.loadHelper().catch(() => undefined);
});

async function attempt(operation: () => Promise<unknown>) {
  busy.value = true;
  try {
    await operation();
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  } finally {
    busy.value = false;
  }
}
const set = (module: DisplayModule, patch: { enabled?: boolean; style?: MenuBarStyle }) =>
  attempt(() => store.setMenuBar(module, patch));
</script>

<template>
  <div v-if="settings.value" class="flex flex-col">
    <h2 class="m-0 mb-10px text-14px font-600 text-ink-primary">
      {{ t('monitor.settings.menuBar') }}
    </h2>
    <OnCard padding="none">
      <div
        v-for="module in store.modules"
        :key="module"
        class="flex items-center gap-12px border-t border-t-solid border-line-subtle px-18px py-12px first:border-t-0"
        :data-module="module"
      >
        <OnIcon :name="MODULE_ICONS[module]" :size="15" class="shrink-0 text-ink-tertiary" />
        <div class="min-w-0 flex-1">
          <div :id="`monitor-setting-${module}`" class="text-13.5px text-ink-primary">
            {{ t(`monitor.modules.${module}`) }}
          </div>
          <div class="mt-2px text-12px text-ink-tertiary">
            {{ t(`monitor.moduleHints.${module}`) }}
          </div>
        </div>
        <MenuBarStylePicker
          :module="module"
          :model-value="store.item(module).style"
          @update:model-value="value => set(module, { style: value })"
        />
        <OnToggle
          :model-value="store.item(module).enabled"
          :data-permission-free="store.item(module).enabled ? '' : undefined"
          :aria-labelledby="`monitor-setting-${module}`"
          @update:model-value="value => set(module, { enabled: value })"
        />
      </div>
      <SettingRow :label="t('monitor.settings.interval')" :hint="t('monitor.settings.intervalHint')">
        <OnSegmented
          :model-value="String(settings.value.monitorIntervalMs)"
          :options="intervals"
          size="sm"
          :aria-label="t('monitor.settings.interval')"
          @update:model-value="value => attempt(() => store.setMonitorSetting('monitorIntervalMs', interval(value)))"
        />
      </SettingRow>
      <SettingRow
        :label="t('monitor.settings.alerts')"
        :hint="t('monitor.settings.alertsHint')"
        label-id="monitor-setting-alerts"
      >
        <OnToggle
          :model-value="settings.value.monitorAlerts"
          :data-permission-free="settings.value.monitorAlerts ? '' : undefined"
          aria-labelledby="monitor-setting-alerts"
          @update:model-value="value => attempt(() => store.setMonitorSetting('monitorAlerts', value))"
        />
      </SettingRow>
      <SettingRow :label="t('monitor.settings.notVisible')" :hint="t('monitor.settings.notVisibleHint')">
        <OnButton
          data-permission-free
          variant="secondary"
          size="sm"
          @click="attempt(() => store.openMenuBarSettings())"
          >{{ t('monitor.settings.openSettings') }}</OnButton
        >
      </SettingRow>
    </OnCard>
    <p class="m-0 mt-10px text-12px leading-[1.55] text-ink-tertiary">
      {{ t('monitor.settings.menuBarNote') }}
    </p>

    <template v-if="store.available.includes('fans')">
      <h2 class="m-0 mb-10px mt-24px text-14px font-600 text-ink-primary">
        {{ t('monitor.modules.fans') }}
      </h2>
      <OnCard padding="none">
        <SettingRow
          :label="t('monitor.settings.helper')"
          :hint="
            fanLocked
              ? t('monitor.fans.unsupported')
              : helper == null
                ? t('common.loading')
                : t('monitor.settings.helperHint', {
                    status: t(`monitor.settings.helperStatus.${helper.status}`)
                  })
          "
        >
          <OnButton
            v-if="helper?.status === 'enabled'"
            data-permission-free
            variant="ghost"
            size="sm"
            :loading="busy"
            @click="attempt(() => store.uninstallHelper())"
            >{{ t('monitor.settings.uninstall') }}</OnButton
          >
          <template v-else-if="!fanLocked && helper?.status === 'requires_approval'">
            <OnButton variant="secondary" size="sm" @click="attempt(() => store.openApproval())">{{
              t('monitor.settings.openSettings')
            }}</OnButton>
            <OnButton
              data-permission-free
              variant="secondary"
              size="sm"
              :loading="busy"
              @click="attempt(() => store.loadHelper())"
              >{{ t('monitor.fans.recheck') }}</OnButton
            >
          </template>
          <OnButton
            v-else-if="!fanLocked && helper?.status === 'not_installed'"
            variant="secondary"
            size="sm"
            :loading="busy"
            @click="attempt(() => store.installHelper())"
            >{{ t('monitor.settings.install') }}</OnButton
          >
          <OnButton
            v-else-if="!fanLocked && helper?.status === 'error'"
            variant="secondary"
            size="sm"
            :loading="busy"
            @click="attempt(() => store.reinstallHelper())"
            >{{ t('monitor.settings.reinstall') }}</OnButton
          >
        </SettingRow>
        <SettingRow :label="t('monitor.settings.autoRestore')" :hint="t('monitor.settings.autoRestoreHint')">
          <OnChip size="sm" tone="success">{{ t('monitor.settings.always') }}</OnChip>
        </SettingRow>
        <SettingRow :label="t('monitor.settings.relaunch')" :hint="t('monitor.settings.relaunchHint')" />
      </OnCard>
    </template>

    <p class="m-0 mt-16px text-12px leading-[1.55] text-ink-tertiary">
      {{ t('monitor.settings.privacy') }}
    </p>
  </div>
</template>
