import { computed, ref } from 'vue';
import { defineStore } from 'pinia';
import type { Task, TaskOptions } from '@/ipc/bindings';
import type { GuidePane } from './permissions';

/** Task failure codes for missing privacy permissions (06 §5.4): App Management and Full Disk Access (zap removes app data). */
export const PERMISSION_ERRORS: Readonly<Record<string, GuidePane>> = {
  E_PERMISSION: 'app_management',
  E_FULL_DISK_ACCESS: 'full_disk_access'
};

/**
 * Retry arguments: an uninstall blocked by Full Disk Access already moved the app, leaving a Caskroom backup; add force to finish (06 §6.3). Otherwise retain the original arguments.
 */
export function retryOptions(task: Task): TaskOptions {
  return !task.options.planId && task.op === 'uninstall' && task.error?.code === 'E_FULL_DISK_ACCESS'
    ? { ...task.options, force: true }
    : task.options;
}

/** Consumed plans require a fresh scan and user confirmation, including after restart. */
export function recheckRoute(task: Task): string | null {
  if (!task.options.planId) return null;
  return task.op === 'uninstall' ? '/installed' : '/cleanup';
}

/**
 * Tasks blocked by privacy permissions (06 §12.3, §12.6): the main window offers guidance and retries with the original arguments after permission is granted.
 * Collect only failures pushed during this session, excluding startup history to avoid replaying old failures.
 */
export const usePermissionPromptStore = defineStore('permissionPrompt', () => {
  const blocked = ref<Task[]>([]);

  // Handle Full Disk Access first: the app is already moved and data removal needs prompt completion.
  const pane = computed<GuidePane | null>(() => {
    const panes = blocked.value.map(task => PERMISSION_ERRORS[task.error?.code ?? '']);
    if (panes.includes('full_disk_access')) return 'full_disk_access';
    return panes.includes('app_management') ? 'app_management' : null;
  });
  const items = computed(() =>
    blocked.value.filter(task => pane.value && PERMISSION_ERRORS[task.error?.code ?? ''] === pane.value)
  );

  function observe(task: Task) {
    const target = task.target;
    if (task.state !== 'failed' || !PERMISSION_ERRORS[task.error?.code ?? ''] || !target) return;
    // Keep only the latest failure per package.
    blocked.value = [
      ...blocked.value.filter(item => !(item.target?.kind === target.kind && item.target.token === target.token)),
      task
    ];
  }

  /** Dismiss the currently shown permission category; show failures in the other category next. */
  function dismiss() {
    const current = new Set(items.value.map(task => task.id));
    blocked.value = blocked.value.filter(task => !current.has(task.id));
  }

  function remove(id: string) {
    blocked.value = blocked.value.filter(task => task.id !== id);
  }

  return { blocked, pane, items, observe, dismiss, remove };
});
