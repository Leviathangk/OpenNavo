<script setup lang="ts">
import { computed, onUnmounted } from 'vue';
import { useI18n } from 'vue-i18n';
import { OnButton, OnCard, OnIcon, OnSegmented, OnToggle } from '@opennavo/ui';
import type { FirewallMode } from '@/ipc/bindings';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { useFirewallStore } from '@/stores/firewall';

// The firewall at a glance (firewall plan §5): on or paused, what happens to connections no rule covers, how much it
// did today, and connections still waiting for an answer.
const { t, te } = useI18n();
const toasts = useToasts();
const { errorText } = usePackageState();
const store = useFirewallStore();

const status = computed(() => store.status);
const on = computed(() => store.on);
// While macOS applies an on/off change the switch shows what was asked for, not the state it is leaving.
const switchedOn = computed(() => store.switchingTo ?? on.value);
const settings = computed(() => store.settings);
const pending = computed(() => store.alerts.length || status.value?.pendingAlerts || 0);
const MODES: readonly FirewallMode[] = ['ask', 'allow', 'block'];
const modes = computed(() => MODES.map(value => ({ value, label: t(`firewall.status.modes.${value}`) })));
// After an update macOS can start the extension without the service OpenNavo talks to; only a restart brings it back.
const serviceMissing = computed(() => status.value?.error?.detail === 'firewall_service_missing');
const disconnected = computed(() => on.value && status.value?.connected === false);
const failure = computed(() => {
  const error = status.value?.error;
  if (!error) return null;
  // The notice above already says the extension isn't answering; other failures still show.
  if (error.code === 'E_FW_DISCONNECTED' && (serviceMissing.value || disconnected.value)) return null;
  const key = `errors.${error.code}`;
  return te(key) ? t(key) : (error.detail ?? error.code);
});

// The mode row reads the settings; they are read again when the extension answers after not responding.
onUnmounted(store.follow('settings'));

async function toggle(value: boolean) {
  try {
    if (value) {
      await store.enable(
        settings.value
          ? {
              allowApple: settings.value.allowApple,
              allowInstalled: settings.value.allowInstalled
            }
          : null
      );
    } else await store.disable();
  } catch (error) {
    toasts.push({
      tone: 'danger',
      title: t('errors.actionFailed'),
      description: errorText(error)
    });
  }
}

async function setMode(mode: FirewallMode) {
  if (!settings.value) return;
  try {
    await store.saveSettings({ ...settings.value, mode });
  } catch (error) {
    toasts.push({
      tone: 'danger',
      title: t('errors.actionFailed'),
      description: errorText(error)
    });
  }
}

async function review() {
  try {
    await store.showAlerts();
  } catch (error) {
    toasts.push({
      tone: 'danger',
      title: t('errors.actionFailed'),
      description: errorText(error)
    });
  }
}
</script>

<template>
  <OnCard v-if="status" padding="none" as="section" aria-labelledby="firewall-status">
    <div class="flex items-center gap-14px px-16px py-14px">
      <span
        class="flex h-40px w-40px shrink-0 items-center justify-center rounded-big"
        :class="on ? 'bg-status-success-subtle text-status-success' : 'bg-surface-inset text-ink-tertiary'"
      >
        <OnIcon name="brick-wall-fire" :size="20" />
      </span>
      <div class="min-w-0 flex-1">
        <h2 id="firewall-status" class="m-0 text-15px font-600 text-ink-primary">
          {{ on ? t('firewall.status.on') : t('firewall.status.off') }}
        </h2>
        <p class="m-0 mt-2px text-12.5px text-ink-secondary">
          <template v-if="on">
            {{ t('firewall.status.rules', { count: status.ruleCount }, { plural: status.ruleCount }) }}
            ·
            {{ t('firewall.status.blockedToday', { count: status.blockedToday }, { plural: status.blockedToday }) }}
          </template>
          <template v-else>{{ t('firewall.status.offBody') }}</template>
        </p>
      </div>
      <OnToggle
        :model-value="switchedOn"
        :disabled="store.switching"
        :aria-label="t('firewall.status.toggle')"
        @update:model-value="toggle"
      />
    </div>
    <p
      v-if="on && status.connected && status.preparing"
      class="m-0 flex items-start gap-8px border-t border-t-solid border-line-subtle bg-status-info-subtle px-16px py-10px text-12.5px text-ink-secondary"
      role="status"
      data-firewall-preparing
    >
      <OnIcon name="loader-circle" :size="14" class="mt-2px shrink-0 animate-spin text-status-info" />
      <span>{{ t('firewall.status.preparing') }}</span>
    </p>
    <p
      v-if="serviceMissing"
      class="m-0 flex items-start gap-8px border-t border-t-solid border-line-subtle bg-status-warning-subtle px-16px py-10px text-12.5px text-ink-secondary"
      role="alert"
      data-firewall-service-missing
    >
      <OnIcon name="triangle-alert" :size="14" class="mt-2px shrink-0 text-status-warning" />
      <span
        ><span class="font-600 text-ink-primary">{{ t('firewall.status.serviceMissing') }}</span>
        {{ t('firewall.status.serviceMissingBody') }}</span
      >
    </p>
    <p
      v-else-if="disconnected"
      class="m-0 flex items-start gap-8px border-t border-t-solid border-line-subtle bg-status-warning-subtle px-16px py-10px text-12.5px text-ink-secondary"
      role="status"
      data-firewall-disconnected
    >
      <OnIcon name="triangle-alert" :size="14" class="mt-2px shrink-0 text-status-warning" />
      <span
        ><span class="font-600 text-ink-primary">{{ t('firewall.status.disconnected') }}</span>
        {{ t('firewall.status.disconnectedBody') }}</span
      >
    </p>
    <p
      v-if="failure"
      class="m-0 border-t border-t-solid border-line-subtle bg-status-danger-subtle px-16px py-10px text-12.5px text-status-danger"
      role="alert"
    >
      {{ failure }}
    </p>
    <div
      v-if="settings"
      class="flex flex-wrap items-center gap-x-14px gap-y-8px border-t border-t-solid border-line-subtle px-16px py-12px"
    >
      <span class="text-13px text-ink-primary">{{ t('firewall.status.mode') }}</span>
      <OnSegmented
        :model-value="settings.mode"
        :options="modes"
        size="sm"
        :aria-label="t('firewall.status.mode')"
        @update:model-value="setMode"
      />
      <span class="min-w-0 flex-1 text-12px text-ink-tertiary">{{
        t(`firewall.status.modeHints.${settings.mode}`)
      }}</span>
    </div>
    <div
      v-if="pending"
      class="flex items-center gap-10px border-t border-t-solid border-line-subtle bg-accent-salmon-subtle px-16px py-10px"
      role="status"
    >
      <OnIcon name="circle-alert" :size="15" class="shrink-0 text-brand-salmon" />
      <span class="min-w-0 flex-1 text-12.5px text-ink-primary">{{
        t('firewall.status.pending', { count: pending }, { plural: pending })
      }}</span>
      <OnButton size="sm" variant="primary" @click="review">{{ t('firewall.status.review') }}</OnButton>
    </div>
  </OnCard>
</template>
