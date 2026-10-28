import { flushPromises, mount } from '@vue/test-utils';
import { afterEach, describe, expect, it, vi } from 'vitest';
import UninstallDialog from '@/components/uninstall/UninstallDialog.vue';
import PermissionGateModal from '@/components/permissions/PermissionGateModal.vue';
import { usePermissionGateStore } from '@/stores/permissionGate';
import PermissionGuidePage from '@/pages/PermissionGuidePage.vue';
import SettingsPage from '@/pages/SettingsPage.vue';
import { i18n } from '@/i18n';
import { commands, events } from '@/ipc/client';
import type { Permissions } from '@/ipc/bindings';
import { startGuideState, startLocalState, usePermissionsStore } from '@/stores';
import { useUninstallStore } from '@/stores/uninstall';
import { useToasts } from '@/composables/useToasts';
import { setup } from './helpers';

// Privacy permissions (06 §12.3, §12.6, §12.7): uninstall scans need Full Disk Access for app containers; guidance overlay and settings permission rows.
const text = () => document.body.textContent ?? '';
const button = (label: string) =>
  [...document.body.querySelectorAll<HTMLButtonElement>('button')].find(
    item => item.textContent?.trim() === label || item.getAttribute('aria-label') === label
  );

async function context(path: string, flags = 'no-full-disk-access') {
  window.history.replaceState(null, '', flags ? `/?mock=${flags}` : '/');
  return setup(path, { brew: true, tickMs: 5 });
}

afterEach(() => {
  document.body.innerHTML = '';
  window.history.replaceState(null, '', '/');
  useToasts().clear();
  localStorage.clear();
  vi.restoreAllMocks();
});

describe('Uninstall: Full Disk Access comes first', () => {
  async function mountDialog() {
    const { pinia, router } = await context('/installed');
    await startLocalState();
    mount(UninstallDialog, { global: { plugins: [pinia, router, i18n] }, attachTo: document.body });
    mount(PermissionGateModal, { global: { plugins: [pinia, router, i18n] }, attachTo: document.body });
    await flushPromises();
  }

  it('Guides the permission first, then scans everything, containers included', async () => {
    await mountDialog();
    expect(usePermissionsStore().fullDiskAccess).toBe('denied');
    const store = useUninstallStore();
    void store.start(['cask-wechat']);
    await vi.waitFor(() => expect(usePermissionGateStore().current).not.toBeNull(), { timeout: 3000 });
    await flushPromises();
    expect(text()).toContain('开启权限，开始使用');

    const guide = vi.spyOn(commands, 'permissionGuideOpen');
    button('开启')?.click();
    await flushPromises();
    expect(guide).toHaveBeenCalledWith('full_disk_access');
    await vi.waitFor(() => expect(usePermissionsStore().fullDiskAccess).toBe('granted'), {
      timeout: 3000
    });
    button('继续')?.click();
    await vi.waitFor(() => expect(store.stage).toBe('review'), { timeout: 3000 });
    await flushPromises();
    expect(store.plan?.incomplete).toEqual([]);
    expect(text()).not.toContain('没有「完全磁盘访问权限」');
    expect(text()).toContain('com.tencent.xinWeChat.MiniProgram');
    expect(text()).toContain('聊天记录');
  });
});

describe('Permission guidance overlay', () => {
  async function mountGuide(pane: string) {
    const { pinia, router } = await context(`/permission-guide/${pane}`);
    await startGuideState();
    mount(PermissionGuidePage, {
      global: { plugins: [pinia, router, i18n] },
      attachTo: document.body
    });
    await flushPromises();
  }

  it('Describe current panel, drag icon on press, Close dismisses overlay', async () => {
    await mountGuide('full_disk_access');
    expect(text()).toContain('完全磁盘访问权限');
    expect(text()).toContain('把 OpenNavo 拖到上方的列表');
    expect(text()).toContain('列表里已有 OpenNavo 时，打开它旁边的开关即可。');

    const drag = vi.spyOn(commands, 'permissionGuideDrag');
    button('拖动 OpenNavo 图标')?.dispatchEvent(new MouseEvent('mousedown', { button: 0, bubbles: true }));
    expect(drag).toHaveBeenCalledTimes(1);
    // Right-click must not start dragging.
    button('拖动 OpenNavo 图标')?.dispatchEvent(new MouseEvent('mousedown', { button: 2, bubbles: true }));
    expect(drag).toHaveBeenCalledTimes(1);

    const close = vi.spyOn(commands, 'permissionGuideClose');
    button('关闭')?.click();
    expect(close).toHaveBeenCalledTimes(1);
  });

  it('Show completion after permission is enabled', async () => {
    await mountGuide('app_management');
    expect(text()).toContain('App 管理');
    usePermissionsStore().apply({
      appManagement: 'granted',
      appManagementSwitch: 'granted',
      fullDiskAccess: 'denied',
      relaunchRequired: false,
      appManagementPending: false,
      fullDiskAccessPending: false
    });
    await flushPromises();
    expect(text()).toContain('已允许 OpenNavo 管理 App');
    expect(button('拖动 OpenNavo 图标')).toBeUndefined();
  });

  // macOS applies Full Disk Access only after OpenNavo reopens: once the switch is on, the overlay is done and says to
  // click Quit & Reopen, instead of asking to drag the icon again.
  it('Full Disk Access awaiting a reopen completes the overlay', async () => {
    await mountGuide('full_disk_access');
    usePermissionsStore().apply({
      appManagement: 'denied',
      appManagementSwitch: 'denied',
      fullDiskAccess: 'denied',
      relaunchRequired: true,
      appManagementPending: false,
      fullDiskAccessPending: true
    });
    await flushPromises();
    expect(text()).toContain('已开启完全磁盘访问权限');
    expect(text()).toContain('如果系统提示「退出并重新打开」，点它即可。');
    expect(button('拖动 OpenNavo 图标')).toBeUndefined();
  });

  it('Reports the height its wrapped text needs so the window fits it', async () => {
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({ height: 145.2 } as DOMRect);
    const fit = vi.spyOn(commands, 'permissionGuideFit');
    await mountGuide('full_disk_access');
    expect(fit).toHaveBeenCalledWith(146);
    expect(fit).toHaveBeenCalledTimes(1);
  });

  it('Without an app bundle to drag, instruct using + instead', async () => {
    vi.spyOn(commands, 'permissionGuideInfo').mockResolvedValue({
      status: 'ok',
      data: { draggable: false, ownerName: null, ownerIcon: null }
    });
    await mountGuide('full_disk_access');
    expect(text()).toContain('点按列表下方的「+」，选择 OpenNavo');
    const drag = vi.spyOn(commands, 'permissionGuideDrag');
    button('拖动 OpenNavo 图标')?.dispatchEvent(new MouseEvent('mousedown', { button: 0, bubbles: true }));
    expect(drag).not.toHaveBeenCalled();
  });

  // macOS checks a development build as the app it was started from, so that app is the one to drag and turn on.
  it('Names and drags the app macOS checks when it is not OpenNavo', async () => {
    vi.spyOn(commands, 'permissionGuideInfo').mockResolvedValue({
      status: 'ok',
      data: { draggable: true, ownerName: 'Paseo', ownerIcon: null }
    });
    await mountGuide('full_disk_access');
    expect(text()).toContain('把 Paseo 拖到上方的列表');
    expect(text()).toContain('列表里已有 Paseo 时，打开它旁边的开关即可。');
    const drag = vi.spyOn(commands, 'permissionGuideDrag');
    button('拖动 Paseo 图标')?.dispatchEvent(new MouseEvent('mousedown', { button: 0, bubbles: true }));
    expect(drag).toHaveBeenCalledTimes(1);
  });
});

describe('Settings: Privacy', () => {
  it('List App Management/Full Disk Access states and enable missing permissions', async () => {
    const { pinia, router } = await context('/settings/privacy');
    await startLocalState();
    mount(SettingsPage, { global: { plugins: [pinia, router, i18n] }, attachTo: document.body });
    await flushPromises();
    expect(text()).toContain('App 管理');
    expect(text()).toContain('完全磁盘访问权限');
    expect(text()).toContain('已开启');
    expect(text()).toContain('未开启');

    const guide = vi.spyOn(commands, 'permissionGuideOpen');
    const enable = [...document.body.querySelectorAll<HTMLButtonElement>('button')].filter(
      item => item.textContent?.trim() === '开启'
    );
    expect(enable).toHaveLength(1);
    enable[0]?.click();
    await flushPromises();
    expect(guide).toHaveBeenCalledWith('full_disk_access');
    await vi.waitFor(() => expect(usePermissionsStore().fullDiskAccess).toBe('granted'), {
      timeout: 3000
    });
    await flushPromises();
    expect(text()).not.toContain('未开启');
  });

  async function mountSettings(flags: string) {
    const { pinia, router } = await context('/settings/privacy', `${flags},no-full-disk-access`);
    await startLocalState();
    mount(SettingsPage, { global: { plugins: [pinia, router, i18n] }, attachTo: document.body });
    await flushPromises();
  }

  // Real macOS: Full Disk Access turned on while OpenNavo runs stays off for this process until it reopens.
  it('A permission awaiting a reopen says so instead of Off + Enable, next to the reopen notice', async () => {
    await mountSettings('permission-relaunch');
    button('开启完全磁盘访问权限')?.click();
    await vi.waitFor(() => expect(text()).toContain('重新打开后生效'), { timeout: 3000 });
    expect(usePermissionsStore().fullDiskAccess).toBe('denied');
    expect(usePermissionsStore().guiding).toBeNull();
    expect(button('开启完全磁盘访问权限')).toBeUndefined();
    expect(text()).not.toContain('未开启');
    expect(text()).toContain('已在系统设置中开启，重新打开 OpenNavo 后生效。');
    expect(button('重新打开')).toBeDefined();
  });

  it('Reopening while tasks run says to wait instead of showing an error code', async () => {
    await mountSettings('permission-relaunch');
    button('开启完全磁盘访问权限')?.click();
    await vi.waitFor(() => expect(button('重新打开')).toBeDefined(), { timeout: 3000 });
    // Rust refuses to restart with running or queued tasks.
    const internals = (
      window as unknown as { __TAURI_INTERNALS__: { invoke: (...args: unknown[]) => Promise<unknown> } }
    )['__TAURI_INTERNALS__'];
    const invoke = internals.invoke;
    vi.spyOn(internals, 'invoke').mockImplementation((command, ...rest) =>
      command === 'plugin:process|restart' ? Promise.reject('active_tasks') : invoke(command, ...rest)
    );
    button('重新打开')?.click();
    await flushPromises();
    const titles = useToasts().items.value.map(item => item.title);
    expect(titles).toContain('有任务正在进行，完成后再重新打开');
    expect(titles).not.toContain('操作失败');
  });

  it('Shows when macOS cannot report a permission and keeps Enable', async () => {
    await mountSettings('no-permission-check');
    expect(text().match(/无法检测/g)).toHaveLength(2);
    expect(button('开启 App 管理')).toBeDefined();
    expect(button('开启完全磁盘访问权限')).toBeDefined();
  });
});

describe('Permission response ordering', () => {
  const oldStatus: Permissions = {
    appManagement: 'denied',
    appManagementSwitch: 'denied',
    fullDiskAccess: 'denied',
    relaunchRequired: false,
    appManagementPending: false,
    fullDiskAccessPending: false
  };
  const newStatus: Permissions = {
    appManagement: 'granted',
    appManagementSwitch: 'granted',
    fullDiskAccess: 'granted',
    relaunchRequired: true,
    appManagementPending: true,
    fullDiskAccessPending: false
  };

  function deferredQuery() {
    let resolve!: (value: Awaited<ReturnType<typeof commands.permissionsGet>>) => void;
    const promise = new Promise<Awaited<ReturnType<typeof commands.permissionsGet>>>(done => {
      resolve = done;
    });
    return { promise, resolve: (data: Permissions) => resolve({ status: 'ok', data }) };
  }

  it('Old queries cannot clear newer relaunch notices; post-event queries can update', async () => {
    await context('/discover');
    await startLocalState();
    const store = usePermissionsStore();
    const query = deferredQuery();
    const get = vi.spyOn(commands, 'permissionsGet').mockReturnValueOnce(query.promise);
    const refreshing = store.refresh();
    await events.permissionsChanged.emit(newStatus);
    await flushPromises();
    expect(store.relaunchRequired).toBe(true);
    query.resolve(oldStatus);
    expect(await refreshing).toEqual(newStatus);
    expect(store.status).toEqual(newStatus);
    expect(store.relaunchRequired).toBe(true);

    get.mockResolvedValueOnce({ status: 'ok', data: oldStatus });
    expect(await store.refresh()).toEqual(oldStatus);
    expect(store.relaunchRequired).toBe(false);
  });

  it.each([true, false])(
    'Only the latest concurrent request applies; old request returns first: %s',
    async olderFirst => {
      await context('/discover');
      const store = usePermissionsStore();
      store.apply(oldStatus);
      const older = deferredQuery();
      const newer = deferredQuery();
      vi.spyOn(commands, 'permissionsGet').mockReturnValueOnce(older.promise).mockReturnValueOnce(newer.promise);
      const first = store.refresh();
      const second = store.refresh();
      if (olderFirst) {
        older.resolve(newStatus);
        await first;
        expect(store.status).toEqual(oldStatus);
        newer.resolve(newStatus);
        await second;
      } else {
        newer.resolve(newStatus);
        await second;
        older.resolve(oldStatus);
        expect(await first).toEqual(newStatus);
      }
      expect(store.status).toEqual(newStatus);
    }
  );
});

describe('Shared permission checklist', () => {
  async function openGate(flags: string) {
    const { pinia, router } = await context('/installed', flags);
    await startLocalState();
    mount(PermissionGateModal, { global: { plugins: [pinia, router, i18n] }, attachTo: document.body });
    await usePermissionsStore().refresh();
    usePermissionGateStore().show('installed');
    await flushPromises();
  }
  it.each(['app-management-switch-off', 'no-full-disk-access'])(
    'saves the pending install before opening system guidance (%s)',
    async flags => {
      await openGate(flags);
      const gate = usePermissionGateStore();
      const request = {
        feature: 'install' as const,
        targets: [{ kind: 'cask' as const, token: 'firefox' }],
        resume: { op: 'install' as const, trigger: 'manual' as const }
      };
      void gate.require(request);
      let remembered: ReturnType<typeof gate.takeRemembered> = null;
      const guide = vi.spyOn(commands, 'permissionGuideOpen').mockImplementation(async () => {
        // macOS may quit this process as soon as its panel opens, without the in-app restart button.
        remembered = gate.takeRemembered();
        return { status: 'ok', data: null };
      });
      await flushPromises();
      button('开启')!.click();
      await flushPromises();
      expect(guide).toHaveBeenCalledOnce();
      expect(remembered).toEqual({ route: '/installed', request });
      gate.dismiss();
    }
  );

  it('does not recover an install canceled after a busy restart refusal', async () => {
    await openGate('no-full-disk-access');
    const gate = usePermissionGateStore();
    const pending = gate.require({
      feature: 'install',
      targets: [{ kind: 'cask', token: 'firefox' }],
      resume: { op: 'install', trigger: 'manual' }
    });
    const permissions = usePermissionsStore();
    permissions.apply({ ...permissions.status!, relaunchRequired: true, fullDiskAccessPending: true });
    const internals = (
      window as unknown as { __TAURI_INTERNALS__: { invoke: (...args: unknown[]) => Promise<unknown> } }
    )['__TAURI_INTERNALS__'];
    const invoke = internals.invoke;
    vi.spyOn(internals, 'invoke').mockImplementation((command, ...rest) =>
      command === 'plugin:process|restart' ? Promise.reject('active_tasks') : invoke(command, ...rest)
    );
    await flushPromises();
    button('重新打开')!.click();
    await flushPromises();
    expect(useToasts().items.value.map(item => item.title)).toContain('有任务正在进行，完成后再重新打开');
    expect(localStorage.getItem('opennavo.permissionGate.resume')).not.toBeNull();
    button(i18n.global.t('permissionGate.browse'))!.click();
    await flushPromises();
    expect(await pending).toBe(false);
    expect(gate.takeRemembered()).toBeNull();
  });
  it('keeps App Management required when Full Disk Access grants effective access', async () => {
    await openGate('app-management-switch-off');
    expect(usePermissionsStore().status?.appManagement).toBe('granted');
    expect(usePermissionGateStore().ready).toBe(false);
    const guide = vi.spyOn(commands, 'permissionGuideOpen');
    button('开启 App 管理')?.click();
    await vi.waitFor(() => expect(usePermissionGateStore().ready).toBe(true));
    expect(guide).toHaveBeenCalledWith('app_management');
    expect(usePermissionGateStore().current).not.toBeNull();
    button('开始使用')?.click();
    await flushPromises();
    expect(usePermissionGateStore().current).toBeNull();
  });
  it('offers session confirmation inside the checklist when detection is unavailable', async () => {
    await openGate('no-permission-check');
    expect(usePermissionGateStore().ready).toBe(false);
    button('我已开启')?.click();
    await flushPromises();
    expect(usePermissionsStore().unknownConfirmed).toBe(true);
    expect(usePermissionGateStore().ready).toBe(true);
  });
});
