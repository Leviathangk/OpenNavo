import { useI18n } from 'vue-i18n';
import { formatBytes } from '@opennavo/shared';
import { IpcError } from '@/ipc/client';
import { usePermissionsStore } from '@/stores';
import { useUninstallStore } from '@/stores/uninstall';
import { useAppLocale } from './useAppLocale';
import { usePackageState } from './usePackageState';
import { useToasts } from './useToasts';

/**
 * Undo and purge of uninstall / clean-up runs with their toasts: the result screen, background announcements and the
 * task history share them. Both resolve to whether something was done.
 */
export function useUninstallUndo() {
  const { t } = useI18n();
  const { appLocale } = useAppLocale();
  const store = useUninstallStore();
  const permissions = usePermissionsStore();
  const toasts = useToasts();
  const { errorText } = usePackageState();

  /** The Trash is unreadable without Full Disk Access: offer to turn it on instead of a dead end. */
  function failed(error: unknown) {
    if (error instanceof IpcError && error.code === 'E_FULL_DISK_ACCESS')
      toasts.pushAction(
        {
          tone: 'warning',
          title: t('errors.actionFailed'),
          description: t('uninstall.toasts.needsFullDisk'),
          actionLabel: t('permission.prompt.enable')
        },
        () => void permissions.guide('full_disk_access').catch(() => undefined)
      );
    else toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }

  async function undo(ids?: readonly string[]) {
    try {
      const result = await store.restore(ids);
      if (result === null) return false;
      const { restored, missing } = result;
      const body = t('uninstall.toasts.undoneBody', { count: restored }, { plural: restored });
      toasts.push({
        tone: missing.length ? 'warning' : 'success',
        title: t(restored ? 'uninstall.toasts.undone' : 'errors.actionFailed'),
        description: missing.length
          ? [body, t('uninstall.toasts.undoMissing', { count: missing.length }, { plural: missing.length })].join(' ')
          : body
      });
      return restored > 0;
    } catch (error) {
      failed(error);
      return false;
    }
  }

  async function purge(ids?: readonly string[]) {
    try {
      const freed = await store.purge(ids);
      if (freed === null) return false;
      toasts.push({
        tone: 'success',
        title: t('uninstall.toasts.purged', { size: formatBytes(freed, { locale: appLocale.value }) })
      });
      return true;
    } catch (error) {
      failed(error);
      return false;
    }
  }

  return { undo, purge };
}
