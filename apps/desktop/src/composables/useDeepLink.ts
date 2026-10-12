import { ref, watch } from 'vue';
import { defineStore } from 'pinia';
import type { Router } from 'vue-router';
import type { DeepLinkEvent, TaskTarget } from '@/ipc/bindings';
import { events } from '@/ipc/client';
import { useToasts } from '@/composables/useToasts';
import { i18n } from '@/i18n';
import { useEnvStore, useLibraryStore, useTasksStore, useSettingsStore } from '@/stores';

const PENDING_KEY = 'opennavo.pendingDeepLink';

function readPending(): DeepLinkEvent | null {
  try {
    const value: unknown = JSON.parse(localStorage.getItem(PENDING_KEY) ?? 'null');
    if (
      value &&
      typeof value === 'object' &&
      'route' in value &&
      'url' in value &&
      'action' in value &&
      typeof value.route === 'string' &&
      typeof value.url === 'string' &&
      (value.action === null || value.action === 'install') &&
      (/^\/package\/(cask|formula)\/[a-z0-9][a-z0-9+@._-]{0,127}$/.test(value.route) ||
        /^\/collection\/[a-z0-9][a-z0-9-]{0,63}$/.test(value.route) ||
        ((value.route.startsWith('/search?q=') ||
          value.route === '/updates' ||
          /^\/monitor(\?detail=(cpu|gpu|memory|disk|network|battery|sensors|fans))?$/.test(value.route) ||
          value.route === '/settings/monitor') &&
          value.action === null))
    ) {
      return { route: value.route, url: value.url, action: value.action };
    }
    localStorage.removeItem(PENDING_KEY);
  } catch {
    // Allow normal startup even when storage is unavailable or old data is corrupt.
  }
  return null;
}

/** Deep-link install request awaiting confirmation (one global dialog; see DeepLinkConfirm). */
export const useDeepLinkStore = defineStore('deeplink', () => {
  const install = ref<TaskTarget | null>(null);
  const pending = ref<DeepLinkEvent | null>(readPending());
  // Persist synchronously so immediate process termination during system authorization cannot lose the target; do not persist installation consent.
  watch(
    pending,
    value => {
      try {
        if (value) localStorage.setItem(PENDING_KEY, JSON.stringify(value));
        else localStorage.removeItem(PENDING_KEY);
      } catch {
        // Keep pending requests in this process when storage is unavailable.
      }
    },
    { flush: 'sync' }
  );
  return { install, pending };
});

const PACKAGE_ROUTE = /^\/package\/(cask|formula)\/([^/?#]+)$/;
const COLLECTION_ROUTE = /^\/collection\/([a-z0-9][a-z0-9-]{0,63})$/;

let opened = false;

/** Whether a deep link has been opened this session; links deferred during onboarding do not count as opened. */
export function deepLinkOpened(): boolean {
  return opened;
}

/**
 * Deep links (06 §10): Rust sends validated routes and actions; navigate here. Install actions open confirmation and enqueue only after consent.
 * Batch collection installs navigate to the collection and open its confirmation. `ready` is the initial local-state read; await it before deciding whether confirmation is needed.
 */
export async function openDeepLink(router: Router, event: DeepLinkEvent, ready: Promise<unknown> = Promise.resolve()) {
  await ready.catch(() => undefined);
  if (useSettingsStore().value?.onboardingCompleted === false) {
    useDeepLinkStore().pending = event;
    await router.replace('/welcome');
    return;
  }
  opened = true;
  const collection = COLLECTION_ROUTE.exec(event.route);
  if (collection) {
    await router.push({
      path: `/collections/${collection[1]}`,
      query: event.action === 'install' ? { install: '1' } : {}
    });
    return;
  }
  const target = PACKAGE_ROUTE.exec(event.route);
  // Cask-only catalog (ADR-018): command-line tool deep links neither open details nor install; show one notice.
  if (target?.[1] === 'formula') {
    await router.push('/discover');
    useToasts().push({ tone: 'warning', title: i18n.global.t('deeplink.formulaUnsupported') });
    return;
  }
  await router.push(event.route);
  if (event.action !== 'install' || !target) return;
  const kind = 'cask';
  const token = decodeURIComponent(target[2] ?? '');
  // No confirmation without Homebrew, for installed packages, or for queued packages; the detail button shows the corresponding state.
  if (!useEnvStore().hasBrew || useLibraryStore().find(kind, token) || useTasksStore().forPackage(kind, token)) return;
  useDeepLinkStore().install = { kind, token };
}

/** Subscribe before mounting: on cold start, Rust buffers command-line deep links until the main window loads. */
export function startDeepLinks(router: Router, ready?: Promise<unknown>) {
  return events.deeplinkReceived.listen(event => void openDeepLink(router, event.payload, ready));
}

/** Replay deep links after onboarding; install actions still use the existing confirmation dialog. */
export async function resumeDeepLink(router: Router): Promise<boolean> {
  const store = useDeepLinkStore();
  const event = store.pending;
  if (!event || useSettingsStore().value?.onboardingCompleted !== true) return false;
  await openDeepLink(router, event);
  if (store.pending === event) store.pending = null;
  return true;
}
