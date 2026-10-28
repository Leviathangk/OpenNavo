<script setup lang="ts">
import PermissionGate from '@/components/permissions/PermissionGate.vue';
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { OnCard, OnIcon, OnRailPanel } from '@opennavo/ui';
import { useFirewallStore } from '@/stores/firewall';
import { firewallPhase } from '@/components/firewall/meta';

// Firewall rail (firewall plan §5): its state in numbers, and what it does not cover, so nobody mistakes it for a
// guard against incoming connections or for something that still asks while OpenNavo is closed.
const { t } = useI18n();
const store = useFirewallStore();
const status = computed(() => store.status);
const ready = computed(() => firewallPhase(status.value) === 'ready');
const state = computed(() => {
  if (!status.value) return '—';
  if (!ready.value) return t('firewall.rail.notSetUp');
  if (!store.on) return t('firewall.rail.paused');
  return status.value.connected ? t('firewall.rail.on') : t('firewall.rail.noResponse');
});
</script>

<template>
  <PermissionGate feature="firewall" variant="rail">
    <OnRailPanel>
      <OnCard padding="md" class="flex flex-col gap-8px">
        <h2 class="m-0 flex items-center gap-7px text-14px font-600 text-ink-primary">
          <OnIcon name="brick-wall-fire" :size="15" />{{ t('firewall.rail.title') }}
        </h2>
        <dl class="m-0 flex flex-col gap-4px text-12.5px">
          <div class="flex justify-between gap-8px">
            <dt class="text-ink-tertiary">{{ t('firewall.rail.state') }}</dt>
            <dd class="m-0 text-ink-primary">{{ state }}</dd>
          </div>
          <template v-if="ready && status">
            <div class="flex justify-between gap-8px">
              <dt class="text-ink-tertiary">{{ t('firewall.rail.mode') }}</dt>
              <dd class="m-0 text-ink-primary">{{ t(`firewall.status.modes.${status.mode}`) }}</dd>
            </div>
            <div class="flex justify-between gap-8px">
              <dt class="text-ink-tertiary">{{ t('firewall.rail.rules') }}</dt>
              <dd class="m-0 text-ink-primary">{{ status.ruleCount }}</dd>
            </div>
            <div class="flex justify-between gap-8px">
              <dt class="text-ink-tertiary">{{ t('firewall.rail.blockedToday') }}</dt>
              <dd class="m-0 text-ink-primary">{{ status.blockedToday }}</dd>
            </div>
            <div v-if="status.extensionVersion" class="flex justify-between gap-8px">
              <dt class="text-ink-tertiary">{{ t('firewall.rail.version') }}</dt>
              <dd class="m-0 text-ink-primary">{{ status.extensionVersion }}</dd>
            </div>
          </template>
        </dl>
      </OnCard>
      <OnCard padding="md" class="flex flex-col gap-6px">
        <h2 class="m-0 flex items-center gap-7px text-14px font-600 text-ink-primary">
          <OnIcon name="info" :size="15" />{{ t('firewall.rail.limitsTitle') }}
        </h2>
        <p class="m-0 text-12px leading-[1.6] text-ink-tertiary">{{ t('firewall.rail.limits') }}</p>
      </OnCard>
    </OnRailPanel>
  </PermissionGate>
</template>
