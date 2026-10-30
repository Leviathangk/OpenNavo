import { ref, watch } from 'vue';
import { defineStore } from 'pinia';
import type { Router } from 'vue-router';
import type { DeepLinkEvent } from '@/ipc/bindings';
import { IpcError, commands, events, unwrap } from '@/ipc/client';
import { useToasts } from '@/composables/useToasts';
import { i18n } from '@/i18n';
import { useSettingsStore } from '@/stores';

const PENDING_KEY = 'opennavo.pendingDeepLink';

function readPending(): DeepLinkEvent | null {
  try {
    const value: unknown = JSON.parse(localStorage.getItem(PENDING_KEY) ?? 'null');
    const pendingId =
      value && typeof value === 'object' && 'pendingId' in value && typeof value.pendingId === 'string'
        ? value.pendingId
        : undefined;
    if (
      value &&
      typeof value === 'object' &&
      'route' in value &&
      'url' in value &&
      'action' in value &&
      typeof value.route === 'string' &&
      typeof value.url === 'string' &&
      (value.action === null || value.action === 'install') &&
      (!('pendingId' in value) ||
        (typeof value.pendingId === 'string' && value.pendingId.length > 0 && value.pendingId.length <= 128)) &&
      (/^\/package\/(cask|formula)\/[a-z0-9][a-z0-9+@._-]{0,127}$/.test(value.route) ||
        /^\/collection\/[a-z0-9][a-z0-9-]{0,63}$/.test(value.route) ||
        ((value.route.startsWith('/search?q=') ||
          value.route === '/updates' ||
          // Preview links (F1, F5) in the exact shape Rust writes them (form-encoded values, preview=1 last).
          /^\/security\/startup\?label=[A-Za-z0-9*._+%-]{1,765}&preview=1$/.test(value.route) ||
          /^\/security\/scan\?token=[A-Za-z0-9*._+%-]{1,384}&preview=1$/.test(value.route) ||
          /^\/uninstall\/[a-z0-9][a-z0-9+@._-]{0,127}\?preview=1$/.test(value.route) ||
          /^\/monitor(\?detail=(cpu|gpu|memory|disk|network|battery|sensors|fans))?$/.test(value.route) ||
          value.route === '/settings/monitor') &&
          value.action === null))
    ) {
      return {
        route: value.route,
        url: value.url,
        action: value.action,
        ...(pendingId ? { pendingId } : {})
      };
    }
    localStorage.removeItem(PENDING_KEY);
  } catch {
    // Allow normal startup even when storage is unavailable or old data is corrupt.
  }
  return null;
}

/** Rust owns install requests; this store only persists links deferred by onboarding. */
export const useDeepLinkStore = defineStore('deeplink', () => {
  const pending = ref<DeepLinkEvent | null>(readPending());
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
  return { pending };
});

const PACKAGE_ROUTE = /^\/package\/(cask|formula)\/([^/?#]+)$/;
const COLLECTION_ROUTE = /^\/collection\/([a-z0-9][a-z0-9-]{0,63})$/;

let opened = false;

/** Whether a deep link has been opened this session; links deferred during onboarding do not count as opened. */
export function deepLinkOpened(): boolean {
  return opened;
}

/**
 * Rust sends validated routes and opaque install ids. The frontend navigates and asks Rust to run the native
 * confirmation/guarded enqueue flow; it never supplies a target or confirmation flag.
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
    await router.push('/collections/' + collection[1]);
    if (event.action === 'install' && event.pendingId) await confirmDeepLink(event.pendingId);
    return;
  }
  const target = PACKAGE_ROUTE.exec(event.route);
  // Formula deep links only open the read-only detail page, even if an old payload carries an install id.
  if (target?.[1] === 'formula') {
    await router.push(event.route);
    return;
  }
  await router.push(event.route);
  if (event.action === 'install' && target && event.pendingId) await confirmDeepLink(event.pendingId);
}

const CONFIRMATION_ERROR_KEYS: Record<string, string> = {
  E_BUSY: 'deeplink.confirmationBusy',
  E_CONFIRMATION_TIMEOUT: 'deeplink.confirmationTimeout'
};

function confirmationErrorMessage(cause: unknown): string {
  if (cause instanceof IpcError) {
    const key = CONFIRMATION_ERROR_KEYS[cause.code];
    if (key) return i18n.global.t(key);
  }
  return cause instanceof Error ? cause.message : String(cause);
}

async function confirmDeepLink(pendingId: string) {
  try {
    await unwrap(commands.deeplinkInstallConfirm(pendingId));
  } catch (cause) {
    const toasts = useToasts();
    toasts.push({
      tone: 'danger',
      title: i18n.global.t('errors.actionFailed'),
      description: confirmationErrorMessage(cause)
    });
  }
}

/** Subscribe before mounting: on cold start, Rust buffers command-line deep links until the main window loads. */
export function startDeepLinks(router: Router, ready?: Promise<unknown>) {
  return events.deeplinkReceived.listen(event => void openDeepLink(router, event.payload, ready));
}

/** Replay deep links after onboarding; Rust still owns any install confirmation. */
export async function resumeDeepLink(router: Router): Promise<boolean> {
  const store = useDeepLinkStore();
  const event = store.pending;
  if (!event || useSettingsStore().value?.onboardingCompleted !== true) return false;
  await openDeepLink(router, event);
  if (store.pending === event) store.pending = null;
  return true;
}
