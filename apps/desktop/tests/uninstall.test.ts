import { flushPromises, mount } from '@vue/test-utils';
import { formatBytes } from '@opennavo/shared';
import { afterEach, describe, expect, it, vi } from 'vitest';
import InstalledPage from '@/pages/InstalledPage.vue';
import HistoryList from '@/components/updates/HistoryList.vue';
import UninstallDialog from '@/components/uninstall/UninstallDialog.vue';
import { i18n } from '@/i18n';
import { commands } from '@/ipc/client';
import { startLocalState, useSettingsStore, useTasksStore } from '@/stores';
import { useInstalledFilterStore } from '@/stores/installedFilter';
import { isUnused, useLocalAppsStore } from '@/stores/localApps';
import { displayPath } from '@/utils/paths';
import { usePermissionsStore } from '@/stores/permissions';
import { usePermissionGateStore } from '@/stores/permissionGate';
import { useUninstallStore } from '@/stores/uninstall';
import { useToasts } from '@/composables/useToasts';
import { setup } from './helpers';

// Uninstall any app (proposal §03–§08): plan defaults, execution through the task queue, undo, batch and page.
// Leftovers of deleted apps moved to Clean Up Mac (tests/cleanup.test.ts).
const text = () => document.body.textContent ?? '';

// Installed and uninstalling start with Full Disk Access; the guide itself is covered below.
async function context(path = '/installed', flags = 'full_disk_access') {
  window.history.replaceState(null, '', flags ? `/?mock=${flags}` : '/');
  const result = await setup(path, { brew: true, tickMs: 5 });
  await startLocalState();
  return result;
}

async function review(ids: string[]) {
  const store = useUninstallStore();
  void store.start(ids);
  await vi.waitFor(() => expect(store.stage).toBe('review'), { timeout: 3000 });
  return store;
}

afterEach(() => {
  document.body.innerHTML = '';
  useToasts().clear();
  vi.restoreAllMocks();
});

describe('Uninstall plan defaults', () => {
  it('Keeps required items, suggests confident ones, leaves shared and name-only matches for the user', async () => {
    await context();
    const store = await review(['cask-docker-desktop']);
    const items = store.apps[0]?.items ?? [];
    const bundle = items.find(item => item.category === 'app');
    const shared = items.find(item => item.sharedWith.length);
    const helper = items.find(item => item.reason === 'brew_uninstall');
    expect(bundle && store.isChecked(bundle)).toBe(true);
    expect(helper && store.isChecked(helper)).toBe(true);
    expect(shared && store.isChecked(shared)).toBe(false);
    expect(store.sharedKept).toBe(1);
    expect(store.adminCount).toBeGreaterThan(0);

    store.selectRequired();
    expect(store.chosen.every(item => item.required)).toBe(true);
    store.recommended();
    expect(shared && store.isChecked(shared)).toBe(false);
    expect(store.chosen.length).toBeGreaterThan(3);
  });

  it('Starts from required items only when "clean related files" is off', async () => {
    await context();
    const settings = useSettingsStore();
    await settings.update('zapByDefault', false);
    const store = await review(['local-zoom']);
    expect(store.chosen.every(item => item.required)).toBe(true);
    expect(store.onlyRequired).toBe(true);
  });
});

describe('Uninstall execution', () => {
  it('Runs one task per app, reports what moved to the Trash, and undo puts the app back', async () => {
    await context();
    const apps = useLocalAppsStore();
    expect(apps.find('local-zoom')).toBeDefined();
    const store = await review(['local-zoom']);
    await store.confirm();
    expect(['running', 'done']).toContain(store.stage);
    await vi.waitFor(() => expect(store.stage).toBe('done'), { timeout: 5000 });
    expect(store.sessionTasks[0]?.target).toEqual({
      kind: 'app',
      token: 'local-zoom'
    });
    expect(store.sessionTasks[0]?.targetName).toBe('zoom.us');
    expect(store.result.moved).toBeGreaterThan(0);
    expect(store.result.restorable).toBe(true);
    await vi.waitFor(() => expect(apps.find('local-zoom')).toBeUndefined(), {
      timeout: 3000
    });

    const { restored } = (await store.restore())!;
    expect(restored).toBeGreaterThan(0);
    expect(store.stage).toBe('idle');
    await vi.waitFor(() => expect(apps.find('local-zoom')).toBeDefined(), {
      timeout: 3000
    });
  });

  it('Skipped items are reported with their reason', async () => {
    // With Full Disk Access the mini-program container is planned; the mock keeps it in use, like a helper outliving the app.
    window.history.replaceState(null, '', '/?mock=full_disk_access');
    await setup('/installed', { brew: true, tickMs: 5 });
    await startLocalState();
    const store = await review(['cask-wechat']);
    await store.confirm();
    await vi.waitFor(() => expect(store.stage).toBe('done'), { timeout: 5000 });
    expect(store.result.skipped).toEqual([expect.objectContaining({ reason: 'in_use' })]);
    expect(store.result.failed).toHaveLength(0);
  });

  it('Batch: an app left out of the run gets no task', async () => {
    await context();
    const store = await review(['local-word', 'local-zoom']);
    store.toggleApp('local-word');
    expect(store.included.map(app => app.appId)).toEqual(['local-zoom']);
    await store.confirm();
    expect(store.taskIds).toHaveLength(1);
    await vi.waitFor(() => expect(store.stage).toBe('done'), { timeout: 5000 });
    expect(useLocalAppsStore().find('local-word')).toBeDefined();
  });

  it('A stale plan is scanned again instead of executed', async () => {
    await context();
    const store = await review(['local-notion']);
    const execute = vi.spyOn(commands, 'uninstallExecute');
    if (store.plan) store.plan.expiresAt = Date.now() - 1;
    await store.confirm();
    expect(execute).not.toHaveBeenCalled();
    expect(store.rescanned).toBe(true);
    await vi.waitFor(() => expect(store.stage).toBe('review'), {
      timeout: 3000
    });
  });

  it('Closing while running continues in the background and announces the result with Undo', async () => {
    const { pinia, router } = await context();
    mount(UninstallDialog, {
      global: { plugins: [pinia, router, i18n] },
      attachTo: document.body
    });
    const store = await review(['local-spotify']);
    await store.confirm();
    await store.close();
    // The run continues on its own; the dialog is free for the next uninstall.
    expect(store.stage).toBe('idle');
    expect(store.detached).toHaveLength(1);
    const toasts = useToasts();
    await vi.waitFor(() => expect(toasts.items.value.some(toast => toast.actionLabel === '撤销')).toBe(true), {
      timeout: 5000
    });
    expect(store.stage).toBe('idle');
    const restore = vi.spyOn(commands, 'uninstallRestore');
    const toast = toasts.items.value.find(item => item.actionLabel === '撤销');
    if (toast) toasts.runAction(toast.id);
    await flushPromises();
    expect(restore).toHaveBeenCalledTimes(1);
  });
});

describe('Installed page', () => {
  async function mountPage() {
    const { pinia, router } = await context();
    const wrapper = mount(InstalledPage, {
      global: { plugins: [pinia, router, i18n] },
      attachTo: document.body
    });
    await vi.waitFor(() => expect(useLocalAppsStore().loaded).toBe(true));
    await flushPromises();
    return wrapper;
  }

  it('Lists every source with counts; protected apps cannot be selected', async () => {
    const wrapper = await mountPage();
    const apps = useLocalAppsStore();
    expect(wrapper.findAll('tbody tr')).toHaveLength(apps.items.length);
    expect(text()).toContain('App Store');
    expect(text()).toContain('手动安装');
    expect(text()).toContain('PKG 安装');
    expect(text()).toContain('当前正在使用的 App');
    const self = wrapper.get('input[aria-label="选择 OpenNavo"]');
    expect(self.attributes('disabled')).toBeDefined();
  });

  it('Homebrew rows use the same local app menu and open command as other sources', async () => {
    const wrapper = await mountPage();
    const app = useLocalAppsStore().find('cask-docker-desktop')!;
    const row = wrapper.findAll('tbody tr').find(item => item.text().includes('docker-desktop'))!;
    const open = vi.spyOn(commands, 'localAppOpen');
    await row.get('button[aria-haspopup="menu"]').trigger('click');
    await flushPromises();
    const items = Array.from(document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]'));
    expect(items.map(item => item.textContent?.trim())).toEqual(['打开', '在访达中显示', '卸载']);
    items[0]!.click();
    await flushPromises();
    expect(open).toHaveBeenCalledWith(app.id);
    wrapper.unmount();
  });

  it('Shows total space including data/cache and shares it with the info sheet', async () => {
    const wrapper = await mountPage();
    const apps = useLocalAppsStore();
    const app = apps.find('cask-docker-desktop')!;
    const result = await apps.footprint(app.id);
    expect(result).not.toBeNull();
    const total = result!.appBytes + result!.dataBytes + result!.cacheBytes;
    expect(total).toBeGreaterThan(app.sizeBytes!);
    await flushPromises();
    const row = wrapper.findAll('tbody tr').find(item => item.text().includes('docker-desktop'))!;
    expect(wrapper.get('thead').text()).toContain('占用空间');
    expect(row.text()).toContain(formatBytes(total, { locale: 'zh-CN' }));
    const scan = vi.spyOn(commands, 'localAppFootprint');
    await row.get('button:not([aria-haspopup])').trigger('click');
    await flushPromises();
    expect(text()).toContain(formatBytes(total, { locale: 'zh-CN' }));
    expect(scan).not.toHaveBeenCalled();
    wrapper.unmount();
  });

  it('Manual recalculation keeps old values until new ones arrive and displays final failure reasons', async () => {
    const wrapper = await mountPage();
    const apps = useLocalAppsStore();
    const app = apps.find('cask-docker-desktop')!;
    await apps.loadFootprints();
    expect(apps.occupiedBytes(app.id)).not.toBeNull();
    let resolve!: (value: Awaited<ReturnType<typeof commands.localAppsScanFootprints>>) => void;
    const scan = vi.spyOn(commands, 'localAppsScanFootprints').mockImplementationOnce(
      () =>
        new Promise(done => {
          resolve = done;
        })
    );
    vi.spyOn(commands, 'localAppFootprints').mockResolvedValue({
      status: 'ok',
      data: {
        progress: null,
        values: {},
        errors: { [app.id]: { code: 'E_TIMEOUT', message: 'timeout', detail: null } }
      }
    });
    const button = wrapper.findAll('button').find(item => item.text() === '重新统计占用')!;
    await button.trigger('click');
    expect(scan).toHaveBeenCalledWith(true);
    // The table does not blank out (and reshuffle) while the new values are measured.
    expect(apps.occupiedBytes(app.id)).not.toBeNull();
    resolve({ status: 'ok', data: true });
    await flushPromises();
    const row = wrapper.findAll('tbody tr').find(item => item.text().includes('docker-desktop'))!;
    expect(row.text()).toContain('长时间没有响应');
    wrapper.unmount();
  });

  it('Shows the full failure reason on hover and retries only the clicked app', async () => {
    const wrapper = await mountPage();
    const apps = useLocalAppsStore();
    const app = apps.find('cask-docker-desktop')!;
    const other = apps.items.find(item => item.id !== app.id)!;
    const before = apps.footprints[other.id];
    const report = await commands.localAppFootprints();
    if (report.status !== 'ok') throw new Error('missing report');
    vi.spyOn(commands, 'localAppFootprints').mockResolvedValue(report);
    apps.footprintErrors[app.id] = {
      code: 'E_FULL_DISK_ACCESS',
      message: 'footprint_incomplete',
      detail: null
    };
    const retry = vi.spyOn(commands, 'localAppRetryFootprint');
    const fullScan = vi.spyOn(commands, 'localAppsScanFootprints');
    await flushPromises();
    const button = wrapper.get('[data-testid="retry-footprint"]');
    await button.element.parentElement!.dispatchEvent(new MouseEvent('mouseenter'));
    await vi.waitFor(
      () => {
        const tooltip = Array.from(document.querySelectorAll<HTMLElement>('[role="tooltip"]')).find(item =>
          item.textContent?.includes('点击重新计算此应用')
        )!;
        expect(tooltip.style.display).not.toBe('none');
        expect(tooltip.textContent).toContain(i18n.global.t('appInfo.footprintIncomplete'));
      },
      { timeout: 1500 }
    );
    await button.trigger('click');
    await flushPromises();
    expect(retry).toHaveBeenCalledExactlyOnceWith(app.id);
    expect(fullScan).not.toHaveBeenCalled();
    expect(apps.footprintErrors[app.id]).toBeUndefined();
    expect(apps.footprints[other.id]).toEqual(before);
    expect(wrapper.find('[data-testid="retry-footprint"]').exists()).toBe(false);
    wrapper.unmount();
  });

  it('Source filter and unused filter narrow the table', async () => {
    const wrapper = await mountPage();
    const filters = useInstalledFilterStore();
    filters.source = 'app_store';
    await flushPromises();
    expect(wrapper.findAll('tbody tr')).toHaveLength(3);
    filters.source = 'all';
    filters.showUnused(true);
    await flushPromises();
    const rows = wrapper.findAll('tbody tr').length;
    expect(rows).toBeGreaterThan(0);
    expect(rows).toBeLessThan(useLocalAppsStore().items.length);
    filters.showUnused(false);
  });

  it('Selecting rows shows the batch bar, which opens the batch uninstall', async () => {
    const wrapper = await mountPage();
    // Catalog-matched apps show the catalog's localized name; find rows by their bundle ID line instead.
    const row = (bundleId: string) => wrapper.findAll('tbody tr').find(item => item.text().includes(bundleId));
    await row('com.spotify.client')?.get('input').setValue(true);
    await row('com.apple.iWork.Keynote')?.get('input').setValue(true);
    expect(text()).toContain('已选 2 个 App');
    const start = vi.spyOn(useUninstallStore(), 'start');
    const button = wrapper.findAll('button').find(item => item.text() === '批量卸载');
    await button?.trigger('click');
    expect([...(start.mock.calls[0]?.[0] ?? [])].sort()).toEqual(['local-keynote', 'local-spotify']);
  });
});

it('Retains all batch results when more than 20 tasks finish in one tick', async () => {
  const { state } = await context();
  const store = useUninstallStore();
  const tasks = useTasksStore();
  const batch = Array.from({ length: 25 }, (_, index) => ({
    ...state.history[0]!,
    id: `batch-${index}`,
    state: 'queued' as const
  }));
  store.taskIds = batch.map(task => task.id);
  store.stage = 'running';
  for (const task of batch) tasks.applyUpdate(task);
  for (const task of batch) tasks.applyUpdate({ ...task, state: 'succeeded' });
  await flushPromises();
  expect(tasks.finished).toHaveLength(20);
  expect(store.stage).toBe('done');
  expect(store.result.tasks).toHaveLength(25);
});

it('Undo skips known Homebrew tasks without a manifest', async () => {
  const { state } = await context();
  const store = useUninstallStore();
  store.taskIds = ['brew', 'local'];
  store.stage = 'done';
  for (const id of store.taskIds)
    useTasksStore().applyUpdate({
      ...state.history[0]!,
      id,
      state: 'succeeded',
      outcome: { freedBytes: 2, trashedBytes: 2, trashed: 1, deleted: 0, skipped: [], restorable: id === 'local' }
    });
  const restore = vi
    .spyOn(commands, 'uninstallRestore')
    .mockResolvedValue({ status: 'ok', data: { restored: 2, missing: [] } });
  expect(await store.restore()).toEqual({ restored: 2, missing: [] });
  expect(restore).toHaveBeenCalledExactlyOnceWith('local');
});

it.each(['succeeded', 'failed', 'canceled', 'running'] as const)(
  'Keeps 30 early %s events across the execute response',
  async terminal => {
    const { state } = await context();
    const store = await review(['local-zoom']);
    const batch = Array.from({ length: 30 }, (_, i) => ({
      ...state.history[0]!,
      id: `early-${i}`,
      options: { planId: store.plan!.planId },
      state: 'queued' as const
    }));
    vi.spyOn(commands, 'uninstallExecute').mockImplementation(async () => {
      for (const task of batch) useTasksStore().applyUpdate({ ...task, state: terminal });
      await flushPromises();
      return { status: 'ok', data: batch };
    });
    await store.confirm();
    await flushPromises();
    expect(store.sessionTasks).toHaveLength(30);
    expect(store.sessionTasks.every(t => t.state === terminal)).toBe(true);
    expect(store.stage).toBe(terminal === 'running' ? 'running' : 'done');
  }
);

it('Discards an execute response after the session was reset', async () => {
  const { state } = await context();
  const store = await review(['local-zoom']);
  let resolve!: (result: Awaited<ReturnType<typeof commands.uninstallExecute>>) => void;
  vi.spyOn(commands, 'uninstallExecute').mockImplementation(
    () =>
      new Promise(done => {
        resolve = done;
      })
  );
  const pending = store.confirm();
  await flushPromises();
  store.reset();
  resolve({ status: 'ok', data: [state.history[0]!] });
  await pending;
  expect(store.stage).toBe('idle');
  expect(store.sessionTasks).toEqual([]);
});

it('Reports partial restore failures, continues the batch, and retries only remaining manifests', async () => {
  await context();
  const store = useUninstallStore();
  const restore = vi
    .spyOn(commands, 'uninstallRestore')
    .mockResolvedValueOnce({
      status: 'error',
      error: {
        code: 'E_RESTORE_UNAVAILABLE',
        message: 'uninstall_restore_unavailable',
        detail: JSON.stringify({ restored: 0, missing: ['/Applications/Lost.app'] })
      }
    })
    .mockResolvedValue({ status: 'ok', data: { restored: 2, missing: [] } });
  expect(await store.restore(['lost', 'good'])).toEqual({ restored: 2, missing: ['/Applications/Lost.app'] });
  await store.restore(['lost', 'good']);
  expect(restore.mock.calls.map(args => args[0])).toEqual(['lost', 'good', 'lost']);
});

it('Does not swallow unavailable background restores or stop purge at the first failure', async () => {
  await context();
  const store = useUninstallStore();
  const failure = {
    status: 'error' as const,
    error: { code: 'E_RESTORE_UNAVAILABLE', message: 'uninstall_restore_unavailable', detail: null }
  };
  vi.spyOn(commands, 'uninstallRestore').mockResolvedValue(failure);
  await expect(store.restore(['missing'])).rejects.toMatchObject({ code: 'E_RESTORE_UNAVAILABLE' });
  const purge = vi
    .spyOn(commands, 'uninstallPurge')
    .mockResolvedValueOnce(failure)
    .mockResolvedValue({ status: 'ok', data: 42 });
  await expect(store.purge(['bad', 'good'])).rejects.toMatchObject({ code: 'E_RESTORE_UNAVAILABLE' });
  expect(purge.mock.calls.map(args => args[0])).toEqual(['bad', 'good']);
  await store.purge(['bad', 'good']);
  expect(purge.mock.calls.map(args => args[0])).toEqual(['bad', 'good', 'bad']);
});

it.each(['failed', 'canceled'] as const)('Keeps partial %s outcomes available for restoration', async terminal => {
  const { state } = await context();
  const store = useUninstallStore();
  store.taskIds = ['partial'];
  store.stage = 'running';
  useTasksStore().applyUpdate({
    ...state.history[0]!,
    id: 'partial',
    state: terminal,
    outcome: {
      freedBytes: 42,
      trashedBytes: 30,
      trashed: 2,
      deleted: 1,
      skipped: [{ itemId: 'kept', path: '/kept', reason: 'in_use' }],
      restorable: true
    }
  });
  await flushPromises();
  expect(store.stage).toBe('done');
  expect(store.result).toMatchObject({
    freedBytes: 42,
    moved: 2,
    deleted: 1,
    skipped: [{ itemId: 'kept', path: '/kept', reason: 'in_use' }],
    restorable: true
  });
  expect(store.result.failed.map(task => task.id)).toEqual(['partial']);
  const restore = vi
    .spyOn(commands, 'uninstallRestore')
    .mockResolvedValue({ status: 'ok', data: { restored: 2, missing: [] } });
  expect(await store.restore()).toEqual({ restored: 2, missing: [] });
  expect(restore).toHaveBeenCalledExactlyOnceWith('partial');
});

describe('Both permissions come first', () => {
  it('Installed previews features without scanning until both permissions are ready', async () => {
    const { pinia, router } = await context('/installed', 'app-management-switch-off');
    const scan = vi.spyOn(commands, 'localAppsScanFootprints');
    const wrapper = mount(InstalledPage, {
      global: { plugins: [pinia, router, i18n] },
      attachTo: document.body
    });
    await vi.waitFor(() => expect(wrapper.find('[data-testid="full-disk-gate"]').exists()).toBe(true));
    expect(wrapper.find('table').exists()).toBe(false);
    expect(text()).not.toContain('先开启「完全磁盘访问权限」');
    expect(text()).not.toContain('重新统计占用');
    expect(scan).not.toHaveBeenCalled();
    const permissions = usePermissionsStore();
    permissions.apply({ ...permissions.status!, appManagementSwitch: 'granted', fullDiskAccess: 'granted' });
    await vi.waitFor(() => expect(wrapper.find('table').exists()).toBe(true), { timeout: 3000 });
    expect(wrapper.find('[data-testid="full-disk-gate"]').exists()).toBe(false);
    expect(scan).toHaveBeenCalled();
    wrapper.unmount();
  });

  it('Unknown permissions need the shared session confirmation', async () => {
    const { pinia, router } = await context('/installed', 'no-permission-check');
    const wrapper = mount(InstalledPage, {
      global: { plugins: [pinia, router, i18n] },
      attachTo: document.body
    });
    await flushPromises();
    expect(wrapper.find('table').exists()).toBe(false);
    usePermissionsStore().unknownConfirmed = true;
    await vi.waitFor(() => expect(wrapper.find('table').exists()).toBe(true), { timeout: 3000 });
    wrapper.unmount();
  });

  it('Uninstall waits for explicit Continue before planning and dismissal does nothing', async () => {
    await context('/installed', 'app-management-switch-off');
    const plan = vi.spyOn(commands, 'uninstallPlan');
    const store = useUninstallStore();
    const gate = usePermissionGateStore();
    const first = store.start(['local-zoom']);
    await flushPromises();
    expect(store.stage).toBe('idle');
    expect(gate.current?.targets).toEqual([{ kind: 'app', token: 'local-zoom' }]);
    expect(plan).not.toHaveBeenCalled();
    gate.dismiss();
    await first;
    expect(store.stage).toBe('idle');
    const second = store.start(['local-zoom']);
    await flushPromises();
    const permissions = usePermissionsStore();
    permissions.apply({ ...permissions.status!, appManagementSwitch: 'granted', fullDiskAccess: 'granted' });
    await flushPromises();
    expect(plan).not.toHaveBeenCalled();
    gate.proceed();
    await second;
    expect(store.stage).toBe('review');
  });

  it('Uninstall preview does not open an info sheet while locked', async () => {
    const { pinia, router } = await context('/uninstall/zoom?preview=1', 'app-management-switch-off');
    const wrapper = mount(InstalledPage, {
      global: { plugins: [pinia, router, i18n] },
      attachTo: document.body
    });
    await flushPromises();
    expect(wrapper.findComponent({ name: 'AppInfoSheet' }).props('open')).toBe(false);
    expect(text()).toContain(i18n.global.t('deeplink.preview.uninstall.permission', { token: 'zoom' }));
    wrapper.unmount();
  });
});

describe('Choices and runs survive what happens around them', () => {
  it('A rescan keeps the items the user ticked or unticked', async () => {
    await context();
    const store = await review(['cask-docker-desktop']);
    const item = store.apps[0]!.items.find(entry => !entry.required && store.isChecked(entry))!;
    store.setItem(item.id, false);
    if (store.plan) store.plan.expiresAt = Date.now() - 1;
    await store.confirm();
    await vi.waitFor(() => expect(store.stage).toBe('review'), { timeout: 3000 });
    expect(store.rescanned).toBe(true);
    expect(store.isChecked(store.apps[0]!.items.find(entry => entry.id === item.id)!)).toBe(false);
  });

  it('A run in the background still announces its result after another uninstall starts', async () => {
    const { pinia, router } = await context();
    mount(UninstallDialog, { global: { plugins: [pinia, router, i18n] }, attachTo: document.body });
    const store = await review(['local-spotify']);
    await store.confirm();
    await store.close();
    await store.start(['local-zoom']);
    await vi.waitFor(() => expect(store.stage).toBe('review'), { timeout: 3000 });
    const toasts = useToasts();
    await vi.waitFor(
      () =>
        expect(toasts.items.value.map(toast => [toast.title, toast.actionLabel])).toContainEqual([
          expect.stringContaining('已卸载'),
          '撤销'
        ]),
      { timeout: 8000 }
    );
    expect(store.stage).toBe('review');
    expect(store.detached).toHaveLength(0);
  }, 15000);

  it('A running app is never suggested as unused, whatever Spotlight says', async () => {
    await context();
    const app = useLocalAppsStore().items.find(item => item.id === 'local-keynote')!;
    const old = { ...app, lastUsedAt: Date.now() - 400 * 86_400_000 };
    expect(isUnused(old)).toBe(true);
    expect(isUnused({ ...old, running: true })).toBe(false);
  });

  it('App Store copies are not offered for Homebrew adoption', async () => {
    const { pinia, router } = await context();
    const apps = useLocalAppsStore();
    await apps.load();
    apps.items = apps.items.map(item =>
      item.id === 'local-keynote' ? { ...item, catalogToken: 'keynote' } : item.id === 'local-notion' ? item : item
    );
    const wrapper = mount(InstalledPage, { global: { plugins: [pinia, router, i18n] }, attachTo: document.body });
    await flushPromises();
    const row = (bundleId: string) => wrapper.findAll('tbody tr').find(item => item.text().includes(bundleId))!;
    expect(row('com.apple.iWork.Keynote').text()).not.toContain('可接管');
    expect(row('notion.id').text()).toContain('可接管');
    wrapper.unmount();
  });

  it('The home folder shows as ~, a shared folder keeps its real path', () => {
    expect(displayPath('/Users/you/Library/Caches/x')).toBe('~/Library/Caches/x');
    expect(displayPath('/Users/Shared/Adobe')).toBe('/Users/Shared/Adobe');
    expect(displayPath('/Applications/X.app')).toBe('/Applications/X.app');
  });
});

describe('History', () => {
  it('Uninstall runs moved to the Trash can be undone from the history', async () => {
    const { pinia, router, state } = await context('/updates');
    const task = {
      ...state.history[0]!,
      id: 'history-uninstall',
      op: 'uninstall' as const,
      state: 'succeeded' as const,
      target: { kind: 'app' as const, token: 'local-zoom' },
      targetName: 'zoom.us',
      outcome: { freedBytes: 10, trashedBytes: 10, trashed: 2, deleted: 0, skipped: [], restorable: true }
    };
    const restore = vi
      .spyOn(commands, 'uninstallRestore')
      .mockResolvedValue({ status: 'ok', data: { restored: 2, missing: [] } });
    const wrapper = mount(HistoryList, {
      props: { tasks: [task] },
      global: { plugins: [pinia, router, i18n] },
      attachTo: document.body
    });
    await flushPromises();
    const undo = wrapper.findAll('button').find(item => item.text() === '撤销')!;
    expect(wrapper.findAll('button').some(item => item.text() === '彻底删除')).toBe(true);
    await undo.trigger('click');
    await flushPromises();
    expect(restore).toHaveBeenCalledExactlyOnceWith('history-uninstall');
    expect(wrapper.findAll('button').some(item => item.text() === '撤销')).toBe(false);
    wrapper.unmount();
  });
});
