<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue';
import { RouterLink, useRoute } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { OnErrorState, OnPageHeader, OnSkeleton, OnTabs } from '@opennavo/ui';
import FirewallActivityPanel from '@/components/firewall/FirewallActivityPanel.vue';
import FirewallBlocklistsPanel from '@/components/firewall/FirewallBlocklistsPanel.vue';
import FirewallRulesPanel from '@/components/firewall/FirewallRulesPanel.vue';
import FirewallSettingsPanel from '@/components/firewall/FirewallSettingsPanel.vue';
import FirewallSetup from '@/components/firewall/FirewallSetup.vue';
import FirewallStatusCard from '@/components/firewall/FirewallStatusCard.vue';
import { FIREWALL_TABS, firewallPhase } from '@/components/firewall/meta';
import type { FirewallTab } from '@/components/firewall/meta';
import { usePackageState } from '@/composables/usePackageState';
import { useFirewallStore } from '@/stores/firewall';

// App firewall (firewall plan, M7 / S4): its own Tools page below Scan for Viruses. Until the network extension runs
// the page explains and follows macOS's confirmations; then status, and four tabs addressed by the route.
const { t } = useI18n();
const route = useRoute();
const { errorText } = usePackageState();
const store = useFirewallStore();

const loadError = ref<unknown>(null);
const phase = computed(() => firewallPhase(store.status));
const tab = computed<FirewallTab>(() => FIREWALL_TABS.find(value => value === route.params.tab) ?? 'rules');
const tabs = computed(() =>
  FIREWALL_TABS.map(value => ({
    value,
    label: t(`firewall.tabs.${value}`),
    href: value === 'rules' ? '/firewall' : `/firewall/${value}`
  }))
);

async function load() {
  loadError.value = null;
  const known = store.status;
  try {
    await store.loadStatus();
  } catch (error) {
    // A status pushed while the read was in flight is newer than the failure.
    if (store.status === known) loadError.value = error;
    return;
  }
  // Only a missing status is a page error: the status card and the panels show what they could not read.
  if (store.installed) {
    void store.loadSettings().catch(() => undefined);
    void store.loadAlerts().catch(() => undefined);
  }
}

onMounted(load);
// A status pushed after a failed read replaces the error, e.g. once the extension answers again.
watch(
  () => store.status,
  next => {
    if (next && loadError.value) loadError.value = null;
  }
);
</script>

<template>
  <div class="relative">
    <OnPageHeader :title="t('firewall.title')" :subtitle="t('firewall.subtitle')" />
    <OnErrorState v-if="loadError" :description="errorText(loadError)" @retry="load" />
    <div v-if="phase === 'loading' && !loadError" class="flex flex-col gap-12px" aria-hidden="true">
      <OnSkeleton height="120px" radius="default" />
      <OnSkeleton height="240px" radius="default" />
    </div>
    <FirewallSetup v-else-if="phase !== 'loading' && phase !== 'ready'" />
    <div v-else-if="phase === 'ready'" class="flex flex-col gap-18px">
      <FirewallStatusCard />
      <div>
        <OnTabs
          class="mb-16px"
          :model-value="tab"
          :items="tabs"
          :aria-label="t('firewall.tabs.label')"
          :link-as="RouterLink"
        />
        <FirewallRulesPanel v-if="tab === 'rules'" />
        <FirewallActivityPanel v-else-if="tab === 'activity'" />
        <FirewallBlocklistsPanel v-else-if="tab === 'blocklists'" />
        <FirewallSettingsPanel v-else />
      </div>
    </div>
  </div>
</template>
