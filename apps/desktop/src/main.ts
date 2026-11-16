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
import { queueHermesNotificationOpen } from './hermes/notificationOpen';
import {
  startFirewallAlertState,
  startGuideState,
  startLocalState,
  startMonitorPopoverState,
  startProfileWindowState,
  startTrayState,
  useSettingsStore
} from './stores';
import { useStateSubscriptions } from './stores/stateSubscriptions';
import { useToasts } from './composables/useToasts';
import { EXTERNAL_LINK_REJECTED, startExternalLinks } from './utils/external';

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
const rejectedExternalLink = () => useToasts().push({ tone: 'danger', title: i18n.global.t('errors.actionFailed') });
window.addEventListener(EXTERNAL_LINK_REJECTED, rejectedExternalLink);
const navigationCleanups: (() => void)[] = [
  startExternalLinks(),
  () => window.removeEventListener(EXTERNAL_LINK_REJECTED, rejectedExternalLink)
];
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
} else if (location.hash.startsWith('#/firewall-alert')) {
  document.documentElement.dataset.opennavoWindow = 'firewall-alert';
  // Firewall question window: only the waiting connections and the answer (firewall plan §4.4).
  await startFirewallAlertState();
} else if (location.hash.startsWith('#/mcp-confirm')) {
  // Local MCP confirmation window: its own prompt and answer only (mcp-confirm capability), no app state.
  document.documentElement.dataset.opennavoWindow = 'mcp-confirm';
} else if (location.hash.startsWith('#/permission-guide')) {
  document.documentElement.dataset.opennavoWindow = 'guide';
  // Permission guidance overlay: read only permissions and language allowed by the guide capability (06 §12.7, §14).
  await startGuideState();
} else {
  const profileWindow = new URLSearchParams(location.search).has('profile');
  if (profileWindow) {
    document.documentElement.dataset.opennavoWindow = 'hermes-profile';
    // The profile window owns no package, security or update state; loading it would invoke
    // commands its capability deliberately does not grant.
    await startProfileWindowState();
    navigationCleanups.push(router.beforeEach(to => (to.name === 'assistant' ? true : '/assistant')));
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
  }
  await router.isReady();
  if (isTauri()) {
    const { listen } = await import('@tauri-apps/api/event');
    navigationCleanups.push(
      await listen<{ conversationId: string }>('hermes-notification-open', event => {
        const conversationId = String(event.payload?.conversationId ?? '').trim();
        if (!conversationId) return;
        queueHermesNotificationOpen({ conversationId });
        void router.push('/assistant').catch(() => undefined);
      })
    );
  }
  if (!profileWindow) {
    await syncMainWindow(router.currentRoute.value.name === 'welcome');
    navigationCleanups.push(
      router.beforeResolve(async to => {
        await syncMainWindow(to.name === 'welcome');
      })
    );
  }
}
app.mount('#app');
