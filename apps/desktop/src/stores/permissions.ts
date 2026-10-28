import { computed, ref } from 'vue';
import { defineStore } from 'pinia';
import type { PermissionState, Permissions, PrivacyPane } from '@/ipc/bindings';
import { commands, unwrap } from '@/ipc/client';

/** Two privacy permissions requiring guidance (06 §12.3, §12.6); notification permission uses the system prompt instead. */
export type GuidePane = Extract<PrivacyPane, 'app_management' | 'full_disk_access'>;

export function paneState(pane: GuidePane, status: Permissions | null): PermissionState {
  if (!status) return 'unknown';
  return pane === 'app_management' ? status.appManagementSwitch : status.fullDiskAccess;
}

/** Gate readiness follows §1.1: App Management uses the System Settings switch; unknown states require this-session confirmation. */
export function gateReady(status: Permissions | null, unknownConfirmed: boolean): boolean {
  if (!status || status.relaunchRequired) return false;
  const app = status.appManagementSwitch;
  const disk = status.fullDiskAccess;
  return (
    (app === 'granted' || (app === 'unknown' && unknownConfirmed)) &&
    (disk === 'granted' || (disk === 'unknown' && unknownConfirmed))
  );
}

/** On in System Settings, but OpenNavo gets it only after reopening (Full Disk Access always does). */
export function panePending(pane: GuidePane, status: Permissions | null): boolean {
  if (!status) return false;
  return pane === 'app_management' ? status.appManagementPending : status.fullDiskAccessPending;
}

/** The user's part is done: the permission is in effect or waits only for a reopen. */
export function paneDone(pane: GuidePane, status: Permissions | null): boolean {
  return paneState(pane, status) === 'granted' || panePending(pane, status);
}

/**
 * Privacy permissions: read at startup, receive permissions:changed from guidance polling, and reread on returning to the main window.
 * guiding identifies the panel being guided; clear it once the permission is on (in effect or awaiting a reopen) or the user returns to OpenNavo.
 */
export const usePermissionsStore = defineStore('permissions', () => {
  const status = ref<Permissions | null>(null);
  const guiding = ref<GuidePane | null>(null);
  const unknownConfirmed = ref(false);
  let requestSequence = 0;
  let eventVersion = 0;

  const appManagement = computed(() => paneState('app_management', status.value));
  const fullDiskAccess = computed(() => paneState('full_disk_access', status.value));
  const relaunchRequired = computed(() => status.value?.relaunchRequired ?? false);
  const appManagementPending = computed(() => panePending('app_management', status.value));
  const fullDiskAccessPending = computed(() => panePending('full_disk_access', status.value));

  function setStatus(next: Permissions) {
    status.value = next;
    if (guiding.value && paneDone(guiding.value, next)) guiding.value = null;
  }

  function pending(pane: GuidePane) {
    return panePending(pane, status.value);
  }

  function apply(next: Permissions) {
    eventVersion += 1;
    setStatus(next);
  }

  async function refresh() {
    const request = ++requestSequence;
    const version = eventVersion;
    const next = await unwrap(commands.permissionsGet());
    // New events supersede prior queries; only the latest concurrent request may update state.
    if (request === requestSequence && version === eventVersion) setStatus(next);
    return status.value;
  }

  /** Open the corresponding System Settings panel and guidance overlay. */
  async function guide(pane: GuidePane) {
    guiding.value = pane;
    try {
      await unwrap(commands.permissionGuideOpen(pane));
    } catch (error) {
      guiding.value = null;
      throw error;
    }
  }

  /** On returning to the main window, stop waiting and read current permissions. */
  async function returned() {
    guiding.value = null;
    await refresh().catch(() => undefined);
  }

  return {
    status,
    guiding,
    unknownConfirmed,
    appManagement,
    fullDiskAccess,
    relaunchRequired,
    appManagementPending,
    fullDiskAccessPending,
    pending,
    apply,
    refresh,
    guide,
    returned
  };
});
