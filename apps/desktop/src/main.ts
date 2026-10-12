import { createApp, computed } from 'vue';
import { createPinia } from 'pinia';
import { isTauri } from '@tauri-apps/api/core';
import '@unocss/reset/tailwind.css';
import '@opennavo/tokens/tokens.css';
import '@opennavo/ui/base.css';
import '@opennavo/ui/system-fonts.css';
import 'virtual:uno.css';
import { createOnUi } from '@opennavo/ui';
import App from './App.vue';
import { i18n } from './i18n';
import { router } from './router';
import { deepLinkOpened, resumeDeepLink, startDeepLinks } from './composables/useDeepLink';
import { startAppLocale } from './composables/startAppLocale';
import { syncMainWindow } from './composables/syncMainWindow';
import { startupRedirect } from './composables/startupRoute';
import { startGuideState, startLocalState, startMonitorPopoverState, startTrayState, useSettingsStore } from './stores';
import { useStateSubscriptions } from './stores/stateSubscriptions';

// Regular browsers (pnpm dev:web) have no Tauri runtime; simulate IPC with fixtures (06 §16).
if (!isTauri()) {
  const { installMockIpc } = await import('./ipc/mock');
  installMockIpc();
}

const app = createApp(App);
app.use(createPinia());
app.use(i18n);
app.use(router);
app.use(createOnUi({ locale: computed(() => i18n.global.locale.value) }));
const subscriptions = useStateSubscriptions();
const navigationCleanups: (() => void)[] = [];
await subscriptions.start('locale', async register => {
  await register(startAppLocale());
});
if (import.meta.hot) {
  import.meta.hot.dispose(() => {
    subscriptions.stopAll();
    for (const cleanup of navigationCleanups) cleanup();
    app.unmount();
  });
}
if (location.hash.startsWith('#/tray-monitor')) {
  document.documentElement.dataset.opennavoWindow = 'tray-monitor';
  // Monitoring popover shared by every monitoring menu bar item: monitoring and fan state only (monitor proposal §12).
  await startMonitorPopoverState();
} else if (location.hash.startsWith('#/tray')) {
  document.documentElement.dataset.opennavoWindow = 'tray';
  // Menu-bar popover: read only state allowed by the tray capability (06 §4.2, §14).
  await startTrayState();
} else if (location.hash.startsWith('#/permission-guide')) {
  document.documentElement.dataset.opennavoWindow = 'guide';
  // Permission guidance overlay: read only permissions and language allowed by the guide capability (06 §12.7, §14).
  await startGuideState();
} else {
  // Subscribe to Rust events and load local state before mounting; subscribe to deep links alongside local state to receive buffered cold-start links.
  const localState = startLocalState();
  await subscriptions.start('deepLinks', async register => {
    await register(startDeepLinks(router, localState));
  });
  await localState;
  // Keep navigation on Welcome until onboarding completes, then replay deferred deep links.
  navigationCleanups.push(
    router.beforeEach(to => {
      if (useSettingsStore().value?.onboardingCompleted === false && to.name !== 'welcome') return '/welcome';
    })
  );
  const redirect = startupRedirect({
    deepLinkOpened: deepLinkOpened(),
    onboardingCompleted: useSettingsStore().value?.onboardingCompleted
  });
  if (redirect) await router.replace(redirect);
  else if (!deepLinkOpened()) await resumeDeepLink(router);
  await router.isReady();
  await syncMainWindow(router.currentRoute.value.name === 'welcome');
  navigationCleanups.push(
    router.beforeResolve(async to => {
      await syncMainWindow(to.name === 'welcome');
    })
  );
}
app.mount('#app');
