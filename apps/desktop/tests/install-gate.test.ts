import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';
import type { TaskOp, TaskTarget, TaskTrigger } from '@/ipc/bindings';
import { commands } from '@/ipc/client';
import { usePermissionGateStore } from '@/stores/permissionGate';
import { usePermissionsStore } from '@/stores/permissions';
import { useTasksStore } from '@/stores/tasks';
import { useLinksStore } from '@/stores/links';
import { useUpdatesStore } from '@/stores/updates';
import { useUninstallStore } from '@/stores/uninstall';
import { useCleanupStore } from '@/stores/cleanup';
import { useInstallConfirmStore } from '@/stores/installConfirm';
import { setup } from './helpers';
import { i18n } from '@/i18n';
import InstallConfirm from '@/components/shell/InstallConfirm.vue';
import TapConfirm from '@/components/shell/TapConfirm.vue';
import RunningAppsConfirm from '@/components/shell/RunningAppsConfirm.vue';
import PermissionGateModal from '@/components/permissions/PermissionGateModal.vue';

const targets: TaskTarget[] = [
  { kind: 'mas', token: '123' },
  { kind: 'mas', token: '456' }
];

async function context() {
  window.history.replaceState(null, '', '/?mock=app-management-switch-off');
  await setup('/discover', { brew: true, tickMs: 5 });
  await usePermissionsStore().refresh();
  return usePermissionGateStore();
}

function grant() {
  const permissions = usePermissionsStore();
  permissions.apply({ ...permissions.status!, appManagementSwitch: 'granted', fullDiskAccess: 'granted' });
}

enableAutoUnmount(afterEach);
afterEach(() => {
  document.body.innerHTML = '';
  localStorage.clear();
  history.replaceState(null, '', '/');
  delete document.documentElement.dataset.opennavoWindow;
  vi.restoreAllMocks();
});

async function authorized() {
  history.replaceState(null, '', '/');
  const ctx = await setup('/discover', { brew: true, tickMs: 5 });
  const permissions = usePermissionsStore();
  await permissions.refresh();
  grant();
  vi.spyOn(commands, 'permissionsGet').mockImplementation(async () => ({ status: 'ok', data: permissions.status! }));
  const enqueue = vi.spyOn(commands, 'taskEnqueue').mockImplementation(async (op, target, options, trigger) => ({
    status: 'ok',
    data: { ...ctx.state.history[0]!, id: `queued-${target?.token}`, state: 'queued', op, target, options, trigger }
  }));
  return { ...ctx, permissions, enqueue, gate: usePermissionGateStore(), tasks: useTasksStore() };
}

function revoke() {
  const permissions = usePermissionsStore();
  permissions.apply({ ...permissions.status!, appManagementSwitch: 'denied' });
}

const recoveryKey = 'opennavo.permissionGate.resume';
const batchTargets: TaskTarget[] = [
  { kind: 'cask', token: 'example-a' },
  { kind: 'cask', token: 'example-b' }
];
const readyPreflight = { name: 'Example App', paths: [], status: 'ready' as const, tap: null };

function savedTargets() {
  return JSON.parse(localStorage.getItem(recoveryKey)!).request.targets;
}

describe('Pending installation recovery', () => {
  it('restores the whole unsubmitted batch after permission is revoked during preparation', async () => {
    const { tasks, gate, enqueue, permissions } = await authorized();
    let prepared!: () => void;
    const preparation = new Promise<void>(resolve => {
      prepared = resolve;
    });
    vi.spyOn(commands, 'installPreflight')
      .mockResolvedValue({ status: 'ok', data: readyPreflight })
      .mockImplementationOnce(async () => {
        await preparation;
        return { status: 'ok', data: readyPreflight };
      });
    const pending = tasks.enqueueMany('install', [...batchTargets, batchTargets[0]!], 'bundle');
    await flushPromises();
    revoke();
    prepared();
    await flushPromises();
    expect(enqueue).not.toHaveBeenCalled();
    gate.rememberForRelaunch('/brewfile');
    expect(savedTargets()).toEqual(batchTargets);
    const record = localStorage.getItem(recoveryKey)!;
    // Dispose of the old process's callers, retaining the snapshot taken before it exited.
    gate.dismiss();
    await flushPromises();
    gate.dismiss();
    expect(await pending).toEqual([]);
    expect(localStorage.getItem(recoveryKey)).toBeNull();

    setActivePinia(createPinia());
    const restoredPermissions = usePermissionsStore();
    restoredPermissions.apply({ ...permissions.status!, appManagementSwitch: 'granted' });
    vi.mocked(commands.permissionsGet).mockImplementation(async () => ({
      status: 'ok',
      data: restoredPermissions.status!
    }));
    localStorage.setItem(recoveryKey, record);
    const restarted = usePermissionGateStore();
    const remembered = restarted.takeRemembered()!;
    expect(remembered.route).toBe('/brewfile');
    expect(remembered.request.resume).toEqual({ op: 'install', trigger: 'bundle' });
    const approved = restarted.offer(remembered.request);
    expect(enqueue).not.toHaveBeenCalled();
    restarted.proceed();
    expect(await approved).toBe(true);
    const restored = await useTasksStore().enqueueMany(
      'install',
      [...remembered.request.targets!],
      remembered.request.resume!.trigger
    );
    expect(restored.map(task => task.target)).toEqual(batchTargets);
    expect(enqueue.mock.calls.map(call => call[1])).toEqual(batchTargets);
    expect(restarted.takeRemembered()).toBeNull();
  });

  it('updates an already saved batch when a submission finishes or another target joins', async () => {
    const { tasks, gate, enqueue } = await authorized();
    let submitted!: () => void;
    const submission = new Promise<void>(resolve => {
      submitted = resolve;
    });
    const submit = enqueue.getMockImplementation()!;
    enqueue.mockImplementationOnce(async (...args) => {
      await submission;
      return submit(...args);
    });
    const pending = tasks.enqueueMany('install', targets, 'bundle');
    await flushPromises();
    expect(enqueue).toHaveBeenCalledOnce();
    revoke();
    gate.show('installed');
    gate.rememberForRelaunch('/brewfile');
    expect(savedTargets()).toEqual(targets);
    submitted();
    await flushPromises();
    expect(savedTargets()).toEqual([targets[1]]);
    const extra = { kind: 'mas' as const, token: '789' };
    const additional = tasks.enqueue('install', extra);
    await flushPromises();
    expect(savedTargets()).toEqual([targets[1], extra]);
    gate.dismiss();
    expect((await pending).map(task => task.target)).toEqual([targets[0]]);
    expect(await additional).toBeNull();
    expect(localStorage.getItem(recoveryKey)).toBeNull();
    grant();
    revoke();
    gate.show('installed', true);
    gate.rememberForRelaunch('/installed');
    expect(gate.takeRemembered()?.request.resume).toBeUndefined();
    expect(enqueue).toHaveBeenCalledOnce();
    gate.dismiss();
  });

  it.each(['cancel', 'failure'] as const)(
    'removes a prepared install after %s without losing queued targets',
    async outcome => {
      const { tasks, gate, enqueue } = await authorized();
      let finish!: () => void;
      const preparation = new Promise<void>(resolve => {
        finish = resolve;
      });
      vi.spyOn(commands, 'installPreflight').mockImplementationOnce(async () => {
        await preparation;
        if (outcome === 'failure') throw new Error('preflight failed');
        return { status: 'ok', data: { ...readyPreflight, status: 'conflict' } };
      });
      const first = tasks.enqueue('install', batchTargets[0]!);
      const result = first.catch(error => error);
      const second = tasks.enqueue('install', batchTargets[1]!);
      await flushPromises();
      revoke();
      gate.show('installed');
      // The system can reopen the app before the first preflight has even returned.
      gate.rememberForRelaunch('/brewfile');
      expect(savedTargets()).toEqual(batchTargets);
      finish();
      await flushPromises();
      if (outcome === 'cancel') useInstallConfirmStore().choose(false);
      if (outcome === 'failure') expect(await result).toEqual(new Error('preflight failed'));
      else expect(await result).toBeNull();
      await flushPromises();
      expect(savedTargets()).toEqual([batchTargets[1]]);
      expect(enqueue).not.toHaveBeenCalled();
      gate.dismiss();
      expect(await second).toBeNull();
      expect(localStorage.getItem(recoveryKey)).toBeNull();
    }
  );

  it('forgets a denied install immediately even while another preflight still owns the queue', async () => {
    const { tasks, gate, enqueue } = await authorized();
    let prepared!: () => void;
    const preparation = new Promise<void>(resolve => {
      prepared = resolve;
    });
    vi.spyOn(commands, 'installPreflight').mockImplementationOnce(async () => {
      await preparation;
      return { status: 'ok', data: readyPreflight };
    });
    const first = tasks.enqueue('install', batchTargets[0]!);
    await flushPromises();
    revoke();
    const second = tasks.enqueue('install', batchTargets[1]!);
    gate.rememberForRelaunch('/brewfile');
    expect(savedTargets()).toEqual(batchTargets);
    gate.dismiss();
    expect(await second).toBeNull();
    gate.show('installed', true);
    gate.rememberForRelaunch('/brewfile');
    expect(savedTargets()).toEqual([batchTargets[0]]);
    prepared();
    await flushPromises();
    gate.dismiss();
    expect(await first).toBeNull();
    expect(localStorage.getItem(recoveryKey)).toBeNull();
    expect(enqueue).not.toHaveBeenCalled();
  });

  it('clears recovery when the last installation is canceled while the permission prompt remains open', async () => {
    const { tasks, gate, enqueue } = await authorized();
    vi.spyOn(commands, 'installPreflight').mockResolvedValueOnce({
      status: 'ok',
      data: { ...readyPreflight, status: 'conflict' }
    });
    const pending = tasks.enqueue('install', batchTargets[0]!);
    await flushPromises();
    revoke();
    gate.show('installed');
    gate.rememberForRelaunch('/brewfile');
    expect(savedTargets()).toEqual([batchTargets[0]]);
    useInstallConfirmStore().choose(false);
    expect(await pending).toBeNull();
    expect(gate.current).not.toBeNull();
    expect(localStorage.getItem(recoveryKey)).toBeNull();
    void gate.require({ feature: 'installed' });
    expect(localStorage.getItem(recoveryKey)).toBeNull();
    expect(enqueue).not.toHaveBeenCalled();
    gate.dismiss();
  });
});

async function clickConfirmation(label: string) {
  const button = [...document.querySelectorAll<HTMLButtonElement>('[role="alertdialog"] button')].find(
    item => item.textContent?.trim() === label
  );
  expect(button).toBeDefined();
  button!.click();
  await flushPromises();
}

describe('Permission checks after asynchronous task preparation', () => {
  for (const choice of ['cancel', 'continue'] as const) {
    it.each(['adoption', 'tap', 'running app'] as const)(
      `rechecks after %s confirmation and handles permission ${choice}`,
      async kind => {
        const { pinia, router, tasks, gate, enqueue } = await authorized();
        const target = { kind: 'cask' as const, token: 'example-app' };
        vi.spyOn(commands, 'installPreflight').mockResolvedValue({
          status: 'ok',
          data: {
            name: 'Example App',
            paths: ['/Applications/Example.app'],
            status: kind === 'adoption' ? 'conflict' : 'ready',
            tap: kind === 'tap' ? 'example-dev/tap' : null
          }
        });
        vi.spyOn(commands, 'appsRunning').mockResolvedValue({ status: 'ok', data: [{ target, name: 'Example App' }] });
        const component = kind === 'adoption' ? InstallConfirm : kind === 'tap' ? TapConfirm : RunningAppsConfirm;
        for (const view of [component, PermissionGateModal])
          mount(view, { global: { plugins: [pinia, router, i18n] }, attachTo: document.body });
        const op = kind === 'running app' ? 'upgrade' : 'install';
        const pending = tasks.enqueue(op, target);
        await flushPromises();
        expect(document.querySelector('[role="alertdialog"]')).not.toBeNull();
        revoke();
        await clickConfirmation(
          i18n.global.t(
            kind === 'adoption' ? 'installConfirm.confirm' : kind === 'tap' ? 'tapConfirm.confirm' : 'runningApps.quit'
          )
        );
        expect(enqueue).not.toHaveBeenCalled();
        expect(gate.current?.feature).toBe(op === 'install' ? 'install' : 'updates');
        if (choice === 'cancel') {
          document
            .querySelector('[role="dialog"]')!
            .dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
          expect(await pending).toBeNull();
          expect(enqueue).not.toHaveBeenCalled();
          expect(tasks.active).toHaveLength(0);
        } else {
          grant();
          await flushPromises();
          expect(enqueue).not.toHaveBeenCalled();
          gate.proceed();
          expect(await pending).not.toBeNull();
          expect(enqueue).toHaveBeenCalledExactlyOnceWith(
            op,
            target,
            kind === 'running app' ? { quitRunning: true, reopen: true } : { adopt: kind === 'adoption' },
            'manual'
          );
        }
      }
    );
  }

  it.each(['install', 'upgrade'] as const)('rechecks a queued %s before starting its preparation', async op => {
    const { tasks, gate, enqueue } = await authorized();
    let release!: () => void;
    const waiting = new Promise<void>(resolve => {
      release = resolve;
    });
    const submit = enqueue.getMockImplementation()!;
    enqueue.mockImplementationOnce(async (...args) => {
      await waiting;
      return submit(...args);
    });
    vi.spyOn(commands, 'appsRunning').mockResolvedValue({ status: 'ok', data: [] });
    const first = tasks.enqueue(op, targets[0]!);
    await flushPromises();
    expect(enqueue).toHaveBeenCalledOnce();
    const second = tasks.enqueue(op, targets[1]!);
    await flushPromises();
    revoke();
    release();
    await first;
    await flushPromises();
    expect(enqueue).toHaveBeenCalledOnce();
    expect(gate.current?.feature).toBe(op === 'install' ? 'install' : 'updates');
    gate.dismiss();
    expect(await second).toBeNull();
    expect(enqueue).toHaveBeenCalledOnce();
  });

  it('rechecks each update in a batch after the preceding enqueue resolves', async () => {
    const { tasks, gate, enqueue } = await authorized();
    const submit = enqueue.getMockImplementation()!;
    enqueue.mockImplementationOnce(async (...args) => {
      const result = await submit(...args);
      revoke();
      return result;
    });
    const pending = tasks.enqueueMany('upgrade', targets);
    await flushPromises();
    expect(enqueue).toHaveBeenCalledOnce();
    expect(gate.current?.feature).toBe('updates');
    gate.dismiss();
    expect((await pending).map(task => task.target)).toEqual([targets[0]]);
    expect(enqueue).toHaveBeenCalledOnce();
  });
});

describe('Install and app-operation permission gate', () => {
  it('Merges batch targets, waits for Continue, and never enqueues a dismissed batch', async () => {
    const gate = await context();
    const enqueue = vi.spyOn(commands, 'taskEnqueue');
    const preflight = vi.spyOn(commands, 'installPreflight');
    const result = useTasksStore().enqueueMany('install', targets, 'bundle');
    await flushPromises();
    expect(gate.current?.targets).toEqual(targets);
    expect(gate.current?.resume).toEqual({ op: 'install', trigger: 'bundle' });
    expect(enqueue).not.toHaveBeenCalled();
    expect(preflight).not.toHaveBeenCalled();
    grant();
    await flushPromises();
    expect(enqueue).not.toHaveBeenCalled();
    gate.dismiss();
    expect(await result).toEqual([]);
    expect(enqueue).not.toHaveBeenCalled();
  });

  it('Continues an approved batch once and clears pending install state', async () => {
    const gate = await context();
    const enqueue = vi.spyOn(commands, 'taskEnqueue');
    const result = useTasksStore().enqueueMany('install', targets, 'bundle');
    await flushPromises();
    grant();
    expect(gate.ready).toBe(true);
    gate.proceed();
    expect(await result).toHaveLength(2);
    expect(enqueue).toHaveBeenCalledTimes(2);
    expect(gate.current).toBeNull();
  });

  it.each<TaskOp>(['reinstall', 'uninstall', 'remove_leftovers', 'rollback', 'clean', 'upgrade', 'install'])(
    'Dismissed %s requests never reach task IPC',
    async op => {
      const gate = await context();
      const one = vi.spyOn(commands, 'taskEnqueue');
      const many = vi.spyOn(commands, 'taskEnqueueMany');
      const tasks = useTasksStore();
      const single = tasks.enqueue(op, targets[0]!);
      const batch = tasks.enqueueMany(op, targets);
      await flushPromises();
      gate.dismiss();
      expect(await single).toBeNull();
      expect(await batch).toEqual([]);
      expect(one).not.toHaveBeenCalled();
      expect(many).not.toHaveBeenCalled();
    }
  );

  it.each<TaskTrigger>(['schedule', 'retry'])('Does not block %s work', async trigger => {
    const gate = await context();
    const enqueue = vi.spyOn(commands, 'taskEnqueue');
    await useTasksStore().enqueue('install', targets[0]!, trigger);
    expect(enqueue).toHaveBeenCalledOnce();
    expect(gate.current).toBeNull();
  });

  it('Secondary windows and safe operations bypass the main-window gate', async () => {
    document.documentElement.dataset.opennavoWindow = 'tray';
    const gate = await context();
    const enqueue = vi.spyOn(commands, 'taskEnqueue');
    await useTasksStore().enqueue('install', targets[0]!);
    expect(enqueue).toHaveBeenCalledOnce();
    expect(gate.current).toBeNull();
  });

  it('Pinning and canceling remain available while locked', async () => {
    const gate = await context();
    const enqueue = vi.spyOn(commands, 'taskEnqueue');
    const cancel = vi.spyOn(commands, 'taskCancel').mockResolvedValue({ status: 'ok', data: null });
    await useTasksStore().enqueue('pin', { kind: 'cask', token: 'firefox' });
    await useTasksStore().cancel('running-task');
    expect(enqueue).toHaveBeenCalledOnce();
    expect(cancel).toHaveBeenCalledExactlyOnceWith('running-task');
    expect(gate.current).toBeNull();
  });

  it('Links require permission for Download but not for an explicit homepage visit', async () => {
    const gate = await context();
    const open = vi.spyOn(commands, 'linkOpen').mockResolvedValue({ status: 'ok', data: null });
    const links = useLinksStore();
    const download = links.open('sample-link', 'download');
    await flushPromises();
    expect(gate.current?.targets).toEqual([{ kind: 'link', token: 'sample-link' }]);
    expect(open).not.toHaveBeenCalled();
    gate.dismiss();
    await download;
    await links.open('sample-link', 'homepage');
    expect(open).toHaveBeenCalledExactlyOnceWith('sample-link', 'homepage');
  });

  it('Update permission precedes any major-version confirmation', async () => {
    const gate = await context();
    const updates = useUpdatesStore();
    await updates.load();
    const result = updates.upgrade(updates.actionable);
    await flushPromises();
    expect(gate.current?.feature).toBe('updates');
    gate.dismiss();
    expect(await result).toEqual([]);
  });

  it('Dismissed restore and purge return null without executing anything', async () => {
    const gate = await context();
    const restore = vi.spyOn(commands, 'uninstallRestore');
    const purge = vi.spyOn(commands, 'uninstallPurge');
    const uninstaller = useUninstallStore();
    const cleanup = useCleanupStore();
    for (const operation of [
      () => uninstaller.restore(['sample']),
      () => uninstaller.purge(['sample']),
      () => cleanup.restore(),
      () => cleanup.purge()
    ]) {
      const pending = operation();
      await flushPromises();
      gate.dismiss();
      expect(await pending).toBeNull();
    }
    expect(restore).not.toHaveBeenCalled();
    expect(purge).not.toHaveBeenCalled();
  });
});
