import { flushPromises, mount } from '@vue/test-utils';
import { afterEach, describe, expect, it, vi } from 'vitest';
import UninstallDialog from '@/components/uninstall/UninstallDialog.vue';
import PermissionGuidePage from '@/pages/PermissionGuidePage.vue';
import SettingsPage from '@/pages/SettingsPage.vue';
import { i18n } from '@/i18n';
import { commands, events } from '@/ipc/client';
import type { Permissions } from '@/ipc/bindings';
import { startGuideState, startLocalState, usePermissionsStore } from '@/stores';
import { useUninstallStore } from '@/stores/uninstall';
import { setup } from './helpers';

// Privacy permissions (06 §12.3, §12.6, §12.7): uninstall scans need Full Disk Access for app containers; guidance overlay and settings permission rows.
const text = () => document.body.textContent ?? '';
const button = (label: string) =>
  [...document.body.querySelectorAll<HTMLButtonElement>('button')].find(
    item => item.textContent?.trim() === label || item.getAttribute('aria-label') === label
  );

async function context(path: string, flags = '') {
  window.history.replaceState(null, '', flags ? `/?mock=${flags}` : '/');
  return setup(path, { brew: true, tickMs: 5 });
}

afterEach(() => {
  document.body.innerHTML = '';
  window.history.replaceState(null, '', '/');
  vi.restoreAllMocks();
});

describe('Uninstall: Full Disk Access comes first', () => {
  async function mountDialog() {
    const { pinia, router } = await context('/installed');
    await startLocalState();
    mount(UninstallDialog, { global: { plugins: [pinia, router, i18n] }, attachTo: document.body });
    await flushPromises();
  }

  it('Guides the permission first, then scans everything, containers included', async () => {
    await mountDialog();
    expect(usePermissionsStore().fullDiskAccess).toBe('denied');
    const store = useUninstallStore();
    void store.start(['cask-wechat']);
    await vi.waitFor(() => expect(store.stage).toBe('permission'), { timeout: 3000 });
    await flushPromises();
    expect(text()).toContain('先开启「完全磁盘访问权限」');

    const guide = vi.spyOn(commands, 'permissionGuideOpen');
    button('开启')?.click();
    await flushPromises();
    expect(guide).toHaveBeenCalledWith('full_disk_access');
    await vi.waitFor(() => expect(usePermissionsStore().fullDiskAccess).toBe('granted'), {
      timeout: 3000
    });
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
      fullDiskAccess: 'denied',
      relaunchRequired: false
    });
    await flushPromises();
    expect(text()).toContain('已允许 OpenNavo 管理 App');
    expect(button('拖动 OpenNavo 图标')).toBeUndefined();
  });

  it('Development builds that cannot drag instruct using + instead', async () => {
    vi.spyOn(commands, 'permissionGuideInfo').mockResolvedValue({
      status: 'ok',
      data: { draggable: false }
    });
    await mountGuide('full_disk_access');
    expect(text()).toContain('点按列表下方的「+」，选择 OpenNavo');
    const drag = vi.spyOn(commands, 'permissionGuideDrag');
    button('拖动 OpenNavo 图标')?.dispatchEvent(new MouseEvent('mousedown', { button: 0, bubbles: true }));
    expect(drag).not.toHaveBeenCalled();
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
});

describe('Permission response ordering', () => {
  const oldStatus: Permissions = {
    appManagement: 'denied',
    fullDiskAccess: 'denied',
    relaunchRequired: false
  };
  const newStatus: Permissions = {
    appManagement: 'granted',
    fullDiskAccess: 'granted',
    relaunchRequired: true
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
