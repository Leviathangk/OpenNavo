import { flushPromises, mount } from '@vue/test-utils';
import { afterEach, describe, expect, it, vi } from 'vitest';
import PermissionPrompt from '@/components/shell/PermissionPrompt.vue';
import { i18n } from '@/i18n';
import { commands, unwrap } from '@/ipc/client';
import type { Task } from '@/ipc/bindings';
import { startLocalState, usePermissionPromptStore, usePermissionsStore, useTasksStore } from '@/stores';
import { setup } from './helpers';

// Permission prompt (06 §12.3, §12.6): handles both App Management and Full Disk Access failures.
const text = () => document.body.textContent ?? '';
const button = (label: string) =>
  [...document.body.querySelectorAll<HTMLButtonElement>('button')].find(item => item.textContent?.trim() === label);

const APP_TITLE = '需要允许 OpenNavo 管理 App';
const DISK_TITLE = '需要完全磁盘访问权限';

async function open(flags = 'no-app-management') {
  window.history.replaceState(null, '', `/?mock=${flags},no-full-disk-access`);
  const context = await setup('/updates', { tickMs: 5 });
  await startLocalState();
  mount(PermissionPrompt, {
    global: { plugins: [context.pinia, context.router, i18n] },
    attachTo: document.body
  });
  await flushPromises();
  return context;
}

afterEach(async () => {
  for (const task of await unwrap(commands.taskListActive())) await unwrap(commands.taskCancel(task.id));
  document.body.innerHTML = '';
  window.history.replaceState(null, '', '/');
  vi.restoreAllMocks();
});

describe('Permission prompt', () => {
  it('No startup prompt; E_PERMISSION opens guidance; retry uses original arguments and retry trigger', async () => {
    await open();
    expect(text()).not.toContain(APP_TITLE);
    expect(usePermissionsStore().appManagement).toBe('denied');

    await unwrap(commands.taskEnqueue('upgrade', { kind: 'cask', token: 'ghostty' }, { greedy: true }, 'manual'));
    await vi.waitFor(() => expect(text()).toContain(APP_TITLE), { timeout: 3000 });
    expect(text().toLowerCase()).toContain('ghostty');

    const guide = vi.spyOn(commands, 'permissionGuideOpen');
    button('开启')?.click();
    await flushPromises();
    expect(guide).toHaveBeenCalledWith('app_management');
    // Keep the prompt during guidance; enabling permission in System Settings removes Enable and leaves Retry.
    expect(text()).toContain(APP_TITLE);
    await vi.waitFor(() => expect(usePermissionsStore().appManagement).toBe('granted'), {
      timeout: 3000
    });
    await flushPromises();
    expect(button('开启')).toBeUndefined();

    const enqueue = vi.spyOn(commands, 'taskEnqueue');
    button('重试')?.click();
    await flushPromises();
    expect(enqueue).toHaveBeenCalledWith(
      'upgrade',
      { kind: 'cask', token: 'ghostty' },
      { greedy: true, quitRunning: false, reopen: false },
      'retry'
    );
    expect(text()).not.toContain(APP_TITLE);
  });

  it('Partial zap failure opens Full Disk Access guidance and retries with --force', async () => {
    await open('');
    expect(usePermissionsStore().fullDiskAccess).toBe('denied');
    await unwrap(commands.taskEnqueue('uninstall', { kind: 'cask', token: 'ghostty' }, { zap: true }, 'manual'));
    await vi.waitFor(() => expect(text()).toContain(DISK_TITLE), { timeout: 3000 });
    expect(text()).toContain('应用数据还没删');

    const enqueue = vi.spyOn(commands, 'taskEnqueue');
    button('重试')?.click();
    await flushPromises();
    expect(enqueue).toHaveBeenCalledWith(
      'uninstall',
      { kind: 'cask', token: 'ghostty' },
      { zap: true, force: true },
      'retry'
    );
    expect(text()).not.toContain(DISK_TITLE);
  });

  // Full Disk Access turned on in System Settings applies after OpenNavo reopens: Enable would only reopen the same switch.
  it('Full Disk Access awaiting a reopen drops Enable and offers the reopen notice', async () => {
    await open('permission-relaunch');
    await unwrap(commands.taskEnqueue('uninstall', { kind: 'cask', token: 'ghostty' }, { zap: true }, 'manual'));
    await vi.waitFor(() => expect(text()).toContain(DISK_TITLE), { timeout: 3000 });
    button('开启')?.click();
    await vi.waitFor(() => expect(usePermissionsStore().fullDiskAccessPending).toBe(true), { timeout: 3000 });
    await flushPromises();
    expect(usePermissionsStore().fullDiskAccess).toBe('denied');
    expect(button('开启')).toBeUndefined();
    expect(text()).toContain('重新打开 OpenNavo 后生效');
    expect(button('重新打开')).toBeDefined();
  });

  it('Prioritize Full Disk Access before App Management when both fail', async () => {
    await open();
    const store = usePermissionPromptStore();
    await unwrap(commands.taskEnqueue('upgrade', { kind: 'cask', token: 'zed' }, { greedy: true }, 'manual'));
    await unwrap(commands.taskEnqueue('uninstall', { kind: 'cask', token: 'ghostty' }, { zap: true }, 'manual'));
    await vi.waitFor(() => expect(store.blocked).toHaveLength(2), { timeout: 5000 });
    expect(store.pane).toBe('full_disk_access');
    await flushPromises();
    expect(text()).toContain(DISK_TITLE);
    button('稍后')?.click();
    await flushPromises();
    expect(store.pane).toBe('app_management');
    expect(text()).toContain(APP_TITLE);
  });

  it('Merge app failures into one prompt; Retry all enqueues individually, Later dismisses', async () => {
    await open();
    await unwrap(
      commands.taskEnqueueMany(
        'upgrade',
        [
          { kind: 'cask', token: 'ghostty' },
          { kind: 'cask', token: 'visual-studio-code' }
        ],
        { greedy: true },
        'manual'
      )
    );
    await vi.waitFor(() => expect(button('全部重试')).toBeDefined(), { timeout: 5000 });
    const enqueue = vi.spyOn(commands, 'taskEnqueue');
    button('稍后')?.click();
    await flushPromises();
    expect(text()).not.toContain(APP_TITLE);
    expect(enqueue).not.toHaveBeenCalled();
  });

  it.each(['其他 App', '同一 App'])(
    'Preserve new permission failures for %s received while batch retry waits',
    async scope => {
      await open();
      const store = usePermissionPromptStore();
      const tasks = useTasksStore();
      await unwrap(
        commands.taskEnqueueMany(
          'upgrade',
          [
            { kind: 'cask', token: 'ghostty' },
            { kind: 'cask', token: 'visual-studio-code' }
          ],
          { greedy: true },
          'manual'
        )
      );
      await vi.waitFor(() => expect(store.blocked).toHaveLength(2), { timeout: 5000 });
      const [first, second] = store.blocked as [Task, Task];
      const queued: Task = {
        ...first,
        id: 'retry-first',
        state: 'queued',
        error: null,
        trigger: 'retry'
      };
      let finish: (() => void) | undefined;
      const pending = new Promise<Task>(resolve => {
        finish = () => resolve(queued);
      });
      const enqueue = vi
        .spyOn(tasks, 'enqueue')
        .mockReturnValueOnce(pending)
        .mockResolvedValueOnce({ ...queued, id: 'retry-second', target: second.target });

      button('全部重试')?.click();
      await flushPromises();
      expect(enqueue).toHaveBeenCalledTimes(1);
      const newer: Task = {
        ...first,
        id: 'new-permission-failure',
        target: scope === '同一 App' ? first.target : { kind: 'cask', token: 'zed' }
      };
      store.observe(newer);
      finish?.();
      await flushPromises();

      expect(enqueue).toHaveBeenCalledTimes(2);
      expect(store.blocked).toEqual([newer]);
      expect(text()).toContain(APP_TITLE);
      expect(button('重试')?.disabled).toBe(false);
      expect(tasks.active.map(task => task.id)).toEqual(['retry-first', 'retry-second']);
    }
  );

  it('Remove only successfully re-enqueued failures; retry remaining failures next time', async () => {
    await open();
    const store = usePermissionPromptStore();
    const tasks = useTasksStore();
    await unwrap(
      commands.taskEnqueueMany(
        'upgrade',
        [
          { kind: 'cask', token: 'ghostty' },
          { kind: 'cask', token: 'visual-studio-code' },
          { kind: 'cask', token: 'zed' }
        ],
        { greedy: true },
        'manual'
      )
    );
    await vi.waitFor(() => expect(store.blocked).toHaveLength(3), { timeout: 5000 });
    const [first, second, third] = store.blocked as [Task, Task, Task];
    const queued: Task = {
      ...first,
      id: 'retry-first',
      state: 'queued',
      error: null,
      trigger: 'retry'
    };
    const enqueue = vi
      .spyOn(tasks, 'enqueue')
      .mockResolvedValueOnce(queued)
      .mockRejectedValueOnce({ code: 'E_NETWORK', detail: null });

    button('全部重试')?.click();
    await flushPromises();
    expect(enqueue).toHaveBeenCalledTimes(2);
    expect(store.blocked).toEqual([second, third]);
    expect(tasks.active.map(task => task.id)).toEqual(['retry-first']);
    expect(button('全部重试')?.disabled).toBe(false);

    enqueue.mockClear();
    enqueue
      .mockResolvedValueOnce({ ...queued, id: 'retry-second', target: second.target })
      .mockResolvedValueOnce({ ...queued, id: 'retry-third', target: third.target });
    button('全部重试')?.click();
    await flushPromises();
    expect(enqueue.mock.calls).toEqual([
      [second.op, second.target, 'retry', second.options],
      [third.op, third.target, 'retry', third.options]
    ]);
    expect(store.blocked).toEqual([]);
    expect(text()).not.toContain(APP_TITLE);
  });
});

it.each(['E_PERMISSION', 'E_FULL_DISK_ACCESS'].flatMap(code => ['app', 'cask'].map(kind => [code, kind] as const)))(
  'Planned %s %s failure opens review without replay or force',
  async (code, kind) => {
    const context = await open();
    const store = usePermissionPromptStore();
    store.observe({
      ...context.state.history[0]!,
      id: 'planned-failure',
      op: 'uninstall',
      state: 'failed',
      target: { kind: kind as 'app' | 'cask', token: 'local-editor' },
      options: { planId: 'consumed' },
      error: { code, message: 'permission', detail: null }
    });
    await flushPromises();
    const enqueue = vi.spyOn(commands, 'taskEnqueue');
    expect(button('重试')).toBeUndefined();
    expect(button('重新检查')).toBeDefined();
    button('重新检查')!.click();
    await flushPromises();
    expect(context.router.currentRoute.value.path).toBe('/installed');
    expect(enqueue).not.toHaveBeenCalled();
    expect(store.blocked).toEqual([]);
  }
);
