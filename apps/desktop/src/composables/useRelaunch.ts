import { ref } from 'vue';
import { relaunch } from '@tauri-apps/plugin-process';
import { i18n } from '@/i18n';
import { IpcError } from '@/ipc/client';
import { updateErrorKey } from '@/ipc/updateErrors';
import { router } from '@/router';
import { usePermissionGateStore } from '@/stores/permissionGate';
import { useToasts } from './useToasts';

/** Shared by permission recovery and self-update, including calls outside component setup. */
export function useRelaunch(route = () => router.currentRoute.value.fullPath, busyKey = 'permission.relaunch.busy') {
  const restarting = ref(false);
  const gate = usePermissionGateStore();
  const toasts = useToasts();

  async function restart() {
    if (restarting.value) return;
    restarting.value = true;
    try {
      gate.rememberForRelaunch(route());
      await relaunch();
    } catch (error) {
      const { t, te } = i18n.global;
      const reason = String(error);
      if (reason.includes('active_tasks')) toasts.push({ tone: 'warning', title: t(busyKey) });
      else if (!reason.includes('exit_in_progress')) {
        const key = error instanceof IpcError ? updateErrorKey(error) : null;
        const description = key && te(key) ? t(key) : error instanceof Error ? error.message : reason;
        toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description });
      }
    } finally {
      restarting.value = false;
    }
  }

  return { restarting, restart };
}
