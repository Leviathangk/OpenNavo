import { computed, ref } from 'vue';
import { defineStore } from 'pinia';
import type { PermissionState, Permissions, PrivacyPane } from '@/ipc/bindings';
import { commands, unwrap } from '@/ipc/client';

/** Two privacy permissions requiring guidance (06 §12.3, §12.6); notification permission uses the system prompt instead. */
export type GuidePane = Extract<PrivacyPane, 'app_management' | 'full_disk_access'>;

export function paneState(pane: GuidePane, status: Permissions | null): PermissionState {
  if (!status) return 'unknown';
  return pane === 'app_management' ? status.appManagement : status.fullDiskAccess;
}

/**
 * Privacy permissions: read at startup, receive permissions:changed from guidance polling, and reread on returning to the main window.
 * guiding identifies the panel being guided; clear it when permission is granted or the user returns to OpenNavo.
 */
export const usePermissionsStore = defineStore('permissions', () => {
  const status = ref<Permissions | null>(null);
  const guiding = ref<GuidePane | null>(null);
  /** The user said Full Disk Access is on although macOS could not tell (this session only, like Welcome). */
  const fullDiskConfirmed = ref(false);
  let requestSequence = 0;
  let eventVersion = 0;

  const appManagement = computed(() => paneState('app_management', status.value));
  const fullDiskAccess = computed(() => paneState('full_disk_access', status.value));
  const relaunchRequired = computed(() => status.value?.relaunchRequired ?? false);
  /**
   * Installed and uninstalling start only with Full Disk Access (2026-10-12): without it app data in other apps'
   * containers cannot be measured or found, and the Trash cannot be read back for Undo.
   */
  const fullDiskReady = computed(
    () => fullDiskAccess.value === 'granted' || (fullDiskAccess.value === 'unknown' && fullDiskConfirmed.value)
  );

  function setStatus(next: Permissions) {
    status.value = next;
    if (guiding.value && paneState(guiding.value, next) === 'granted') guiding.value = null;
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
    fullDiskConfirmed,
    appManagement,
    fullDiskAccess,
    fullDiskReady,
    relaunchRequired,
    apply,
    refresh,
    guide,
    returned
  };
});
