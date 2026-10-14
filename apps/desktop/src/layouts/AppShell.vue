<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { RouterView, useRoute } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { isTauri } from '@tauri-apps/api/core';
import { OnAppShell, OnToastRegion, OnButton, useUiMessages } from '@opennavo/ui';
import AppSidebar from '@/components/shell/AppSidebar.vue';
import AppToolbar from '@/components/shell/AppToolbar.vue';
import WindowControls from '@/components/shell/WindowControls.vue';
import CommandPalette from '@/components/palette/CommandPalette.vue';
import PermissionPrompt from '@/components/shell/PermissionPrompt.vue';
import DeepLinkConfirm from '@/components/shell/DeepLinkConfirm.vue';
import QuitConfirm from '@/components/shell/QuitConfirm.vue';
import InstallConfirm from '@/components/shell/InstallConfirm.vue';
import TapConfirm from '@/components/shell/TapConfirm.vue';
import CleanupNotifier from '@/components/cleanup/CleanupNotifier.vue';
import MonitorNotifier from '@/components/monitor/MonitorNotifier.vue';
import UninstallDialog from '@/components/uninstall/UninstallDialog.vue';
import RunningAppsConfirm from '@/components/shell/RunningAppsConfirm.vue';
import RestorePrompt from '@/components/shell/RestorePrompt.vue';
import AppUpdateDialog from '@/components/shell/AppUpdateDialog.vue';
import { useMediaQuery } from '@/composables/useMediaQuery';
import { usePageRefresh, usePageRecovery } from '@/composables/usePageRefresh';
import { useToasts } from '@/composables/useToasts';

const route = useRoute();
const { refresh, errors, loading } = usePageRefresh();
const messages = useUiMessages();
const { t } = useI18n();
const recover = usePageRecovery();
onMounted(() => {
  window.addEventListener('online', recover);
  window.addEventListener('focus', recover);
  document.addEventListener('visibilitychange', recover);
});
onBeforeUnmount(() => {
  window.removeEventListener('online', recover);
  window.removeEventListener('focus', recover);
  document.removeEventListener('visibilitychange', recover);
});
const toasts = useToasts();
const inBrowser = !isTauri();
// Below 1240 px, collapse the statistics rail into the toolbar's Overview button (08 §9.1).
const wide = useMediaQuery('(min-width: 1240px)');
const overviewOpen = ref(false);
const paletteOpen = ref(false);
const shell = ref<InstanceType<typeof OnAppShell>>();
// Page-named rail views provide the right statistics rail (Discover, Installed, Updates).
const hasRail = computed(() => route.matched.some(record => Boolean(record.components?.rail)));

// Command-K / Ctrl-K opens the command palette on any page.
function onKeydown(event: KeyboardEvent) {
  if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k' && !event.isComposing) {
    event.preventDefault();
    paletteOpen.value = !paletteOpen.value;
  }
}
onMounted(() => window.addEventListener('keydown', onKeydown));
onBeforeUnmount(() => window.removeEventListener('keydown', onKeydown));

watch(
  () => route.path,
  () => {
    overviewOpen.value = false;
    // Pages scroll inside <main>; return to the top on navigation.
    shell.value?.scrollToTop();
  }
);

// Use @opennavo/ui's OnAppShell shared with the web app (ADR-017); supply desktop sidebar, toolbar, and local-state overlays here.
</script>

<template>
  <OnAppShell ref="shell" scroll="container" :has-rail="hasRail" :rail-docked="wide" :rail-open="overviewOpen">
    <template #sidebar>
      <AppSidebar @reselect="shell?.scrollToTop()" />
    </template>
    <template #toolbar>
      <AppToolbar
        :show-overview-toggle="hasRail && !wide"
        :overview-open="overviewOpen"
        @toggle-overview="overviewOpen = !overviewOpen"
        @open-palette="paletteOpen = true"
      />
    </template>
    <div
      v-if="errors.length"
      role="alert"
      class="mb-16px flex items-center gap-12px rounded-default bg-surface-raised p-12px"
    >
      <span class="flex-1 text-13px text-ink-secondary">{{ t('common.refreshFailed') }}</span>
      <OnButton variant="secondary" size="sm" :loading="loading" @click="refresh()">{{
        messages.action.retry
      }}</OnButton>
    </div>
    <RouterView />
    <template #rail>
      <RouterView name="rail" class="h-full" />
    </template>
    <template #overlay>
      <WindowControls v-if="inBrowser" />
      <CommandPalette v-model:open="paletteOpen" />
      <DeepLinkConfirm />
      <PermissionPrompt />
      <QuitConfirm />
      <RestorePrompt />
      <RunningAppsConfirm />
      <InstallConfirm />
      <TapConfirm />
      <UninstallDialog />
      <CleanupNotifier />
      <MonitorNotifier />
      <AppUpdateDialog />
      <OnToastRegion :items="toasts.items.value" @dismiss="toasts.dismiss" @action="toasts.runAction" />
    </template>
  </OnAppShell>
</template>
