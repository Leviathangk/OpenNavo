<script setup lang="ts">
import { computed, onMounted } from 'vue';
import { RouterLink, useRoute } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { OnPageHeader, OnTabs } from '@opennavo/ui';
import SecurityOverviewPanel from '@/components/security/SecurityOverviewPanel.vue';
import SecurityProtectionPanel from '@/components/security/SecurityProtectionPanel.vue';
import SecurityQuarantinePanel from '@/components/security/SecurityQuarantinePanel.vue';
import SecurityScanPanel from '@/components/security/SecurityScanPanel.vue';
import SecurityStartupPanel from '@/components/security/SecurityStartupPanel.vue';
import { SECURITY_TABS } from '@/components/security/meta';
import type { SecurityTab } from '@/components/security/meta';
import { useSecurityStore } from '@/stores/security';
import { NO_PREVIEW, scanPreview, startupPreview } from '@/utils/deepLinkPreview';

// Security (store/security plan §6, mockup E): one page, five tabs addressed by the route so notifications can deep-link.
// Preview links from the web (F1, F5) open the startup tab with ?label=…&preview=1 and the scan tab with
// ?token=…&preview=1: the panels find and preselect the item or app, and nothing runs until the user acts.
const { t } = useI18n();
const route = useRoute();
const store = useSecurityStore();

const tab = computed<SecurityTab>(() => SECURITY_TABS.find(value => value === route.params.tab) ?? 'overview');
const startupLink = computed(() => (tab.value === 'startup' ? startupPreview(route.query) : NO_PREVIEW));
const scanLink = computed(() => (tab.value === 'scan' ? scanPreview(route.query) : NO_PREVIEW));
const tabs = computed(() =>
  SECURITY_TABS.map(value => ({
    value,
    label: t(`security.tabs.${value}`),
    count:
      value === 'startup'
        ? store.overview?.startup.pendingReview || undefined
        : value === 'quarantine'
          ? store.overview?.quarantine.count || undefined
          : undefined,
    href: value === 'overview' ? '/security' : `/security/${value}`
  }))
);

onMounted(() => void store.loadOverview().catch(() => undefined));
</script>

<template>
  <div class="relative">
    <OnPageHeader :title="t('security.title')" :subtitle="t('security.subtitle')" />
    <OnTabs
      class="mb-18px"
      :model-value="tab"
      :items="tabs"
      :aria-label="t('security.tabs.label')"
      :link-as="RouterLink"
    />
    <SecurityOverviewPanel v-if="tab === 'overview'" />
    <SecurityScanPanel v-else-if="tab === 'scan'" :preview="scanLink" />
    <SecurityStartupPanel v-else-if="tab === 'startup'" :preview="startupLink" />
    <SecurityProtectionPanel v-else-if="tab === 'protection'" />
    <SecurityQuarantinePanel v-else />
  </div>
</template>
