import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { i18n } from '@/i18n';
import SettingsPage from '@/pages/SettingsPage.vue';
import { OnSegmented } from '@opennavo/ui';
import { commands } from '@/ipc/client';
import type { Permissions } from '@/ipc/bindings';
import { useSettingsStore } from '@/stores/settings';
import { useMonitorStore } from '@/stores/monitor';
import { usePermissionsStore } from '@/stores/permissions';
import { usePermissionGateStore } from '@/stores/permissionGate';
import { setup } from './helpers';

enableAutoUnmount(afterEach);
afterEach(() => {
  vi.restoreAllMocks();
  document.body.innerHTML = '';
  history.replaceState(null, '', '/');
});

async function page(section: string, ready = false) {
  const context = await setup(`/settings/${section}`);
  await useSettingsStore().load();
  const permissions = usePermissionsStore();
  await permissions.refresh();
  permissions.apply({
    ...permissions.status!,
    appManagementSwitch: ready ? 'granted' : 'denied',
    fullDiskAccess: 'granted',
    relaunchRequired: false
  });
  vi.spyOn(commands, 'permissionsGet').mockImplementation(async () => ({ status: 'ok', data: permissions.status! }));
  const wrapper = mount(SettingsPage, {
    global: { plugins: [context.pinia, context.router, i18n] },
    attachTo: document.body
  });
  await flushPromises();
  return { wrapper, permissions };
}

describe('settings permission gate', () => {
  it('does not enable CPU monitoring before the first permission query resolves', async () => {
    const context = await setup('/settings/monitor');
    const settings = useSettingsStore();
    await settings.load();
    await settings.update('monitorMenuBar', current => ({ ...current, cpu: { ...current.cpu, enabled: false } }));
    const status = await commands.permissionsGet();
    let complete!: (value: typeof status) => void;
    const query = new Promise<typeof status>(resolve => {
      complete = resolve;
    });
    vi.spyOn(commands, 'permissionsGet').mockReturnValue(query);
    const update = vi.spyOn(commands, 'settingsSet');
    const wrapper = mount(SettingsPage, {
      global: { plugins: [context.pinia, context.router, i18n] },
      attachTo: document.body
    });
    await flushPromises();
    const toggle = wrapper.get('[aria-labelledby="monitor-setting-cpu"]');
    await toggle.trigger('click');
    await flushPromises();
    expect(usePermissionsStore().status).toBeNull();
    expect(update).not.toHaveBeenCalled();
    expect(settings.value?.monitorMenuBar.cpu.enabled).toBe(false);
    complete(status);
    await flushPromises();
    expect(update).not.toHaveBeenCalled();
    // The blocked click is not replayed; a new click after the permission result can save.
    await toggle.trigger('click');
    await flushPromises();
    expect(update).toHaveBeenCalledOnce();
    expect(settings.value?.monitorMenuBar.cpu.enabled).toBe(true);
  });

  it('checks menu bar permissions in the store while the first query is pending', async () => {
    await setup('/settings/monitor');
    await useSettingsStore().load();
    const permissions = usePermissionsStore();
    let complete!: (value: Permissions | null) => void;
    vi.spyOn(permissions, 'refresh').mockImplementation(
      () =>
        new Promise(resolve => {
          complete = resolve;
        })
    );
    const update = vi.spyOn(commands, 'settingsSet');
    const saving = useMonitorStore().setMenuBar('gpu', { enabled: true });
    await flushPromises();
    expect(update).not.toHaveBeenCalled();
    complete(null);
    expect(await saving).toBe(false);
    expect(update).not.toHaveBeenCalled();
  });

  it('checks permission when saving a monitor style from an already open menu', async () => {
    const { wrapper, permissions } = await page('monitor', true);
    const store = useMonitorStore();
    const previous = store.item('cpu').style;
    await wrapper.get('[data-module="cpu"] [data-testid="menubar-style"]').trigger('click');
    await flushPromises();
    const choice = [...document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')].find(
      item => item.textContent?.trim() !== i18n.global.t('monitor.menuBar.styles.text')
    );
    expect(choice).toBeDefined();
    permissions.apply({ ...permissions.status!, appManagementSwitch: 'denied' });
    const gate = usePermissionGateStore();
    gate.show('monitor', true);
    gate.dismiss();
    const update = vi.spyOn(commands, 'settingsSet');
    choice!.click();
    await flushPromises();
    expect(update).not.toHaveBeenCalled();
    expect(gate.current?.feature).toBe('monitor');
    gate.dismiss();
    await flushPromises();
    expect(update).not.toHaveBeenCalled();
    expect(store.item('cpu').style).toBe(previous);
  });

  it('allows disabling monitoring while locked, but not a style edit bundled with disabling', async () => {
    await page('monitor');
    const store = useMonitorStore();
    const update = vi.spyOn(commands, 'settingsSet');
    await store.setMenuBar('cpu', { enabled: false });
    expect(update).toHaveBeenCalledOnce();
    expect(usePermissionGateStore().current).toBeNull();
    update.mockClear();
    const saving = store.setMenuBar('cpu', { enabled: false, style: 'line' });
    await flushPromises();
    expect(update).not.toHaveBeenCalled();
    usePermissionGateStore().dismiss();
    expect(await saving).toBe(false);
    expect(store.item('cpu').style).toBe('text');
  });

  it('checks permission at the monitor interval save entry and saves after explicit approval', async () => {
    const { wrapper, permissions } = await page('monitor');
    const update = vi.spyOn(commands, 'settingsSet');
    wrapper.getComponent(OnSegmented).vm.$emit('update:modelValue', '1000');
    await flushPromises();
    expect(update).not.toHaveBeenCalled();
    const gate = usePermissionGateStore();
    expect(gate.current?.feature).toBe('monitor');
    permissions.apply({ ...permissions.status!, appManagementSwitch: 'granted' });
    gate.proceed();
    await flushPromises();
    expect(update).toHaveBeenCalledOnce();
    expect(useSettingsStore().value?.monitorIntervalMs).toBe(1000);
  });

  it.each(['security', 'cleanup', 'monitor'])('shows a banner without opening the guide on %s entry', async section => {
    const { wrapper } = await page(section);
    expect(wrapper.text()).toContain('开启「App 管理」');
    expect(usePermissionGateStore().current).toBeNull();
  });

  it('blocks enabling a switch but writes disabling an enabled switch', async () => {
    const { wrapper } = await page('security');
    const update = vi.spyOn(commands, 'settingsSet');
    await wrapper.get('[aria-labelledby="setting-avKeepResident"]').trigger('click');
    await flushPromises();
    expect(update).not.toHaveBeenCalled();
    expect(usePermissionGateStore().current?.feature).toBe('security');
    usePermissionGateStore().dismiss();
    await wrapper.get('[aria-labelledby="setting-watchDownloads"]').trigger('click');
    await flushPromises();
    expect(update).toHaveBeenCalled();
    expect(useSettingsStore().value?.watchDownloads).toBe(false);
    expect(usePermissionGateStore().current).toBeNull();
    update.mockClear();
    await wrapper.get('[aria-labelledby="setting-watchDownloads"]').trigger('click');
    await flushPromises();
    expect(update).not.toHaveBeenCalled();
    expect(usePermissionGateStore().current?.feature).toBe('security');
  });

  it('blocks cleanup segmented changes', async () => {
    const { wrapper } = await page('cleanup');
    const update = vi.spyOn(commands, 'settingsSet');
    await wrapper.get('[role="radio"][aria-checked="false"]').trigger('click');
    await flushPromises();
    expect(update).not.toHaveBeenCalled();
    expect(usePermissionGateStore().current?.feature).toBe('cleanup');
  });

  it.each(['ArrowRight', 'ArrowLeft', 'ArrowUp', 'ArrowDown', 'Home', 'End'])(
    'blocks cleanup segmented keyboard changes with %s',
    async key => {
      const { wrapper } = await page('cleanup');
      const update = vi.spyOn(commands, 'settingsSet');
      await wrapper.get('[role="radio"][aria-checked="true"]').trigger('keydown', { key });
      await flushPromises();
      expect(update).not.toHaveBeenCalled();
      expect(useSettingsStore().value?.cleanupDeleteMode).toBe('trash');
      expect(usePermissionGateStore().current?.feature).toBe('cleanup');
    }
  );

  it('checks permission at the cleanup save entry even without a DOM event', async () => {
    const { wrapper } = await page('cleanup');
    const update = vi.spyOn(commands, 'settingsSet');
    wrapper.getComponent(OnSegmented).vm.$emit('update:modelValue', 'delete');
    await flushPromises();
    expect(usePermissionGateStore().current?.feature).toBe('cleanup');
    expect(update).not.toHaveBeenCalled();
    usePermissionGateStore().dismiss();
    await flushPromises();
    expect(update).not.toHaveBeenCalled();
    expect(useSettingsStore().value?.cleanupDeleteMode).toBe('trash');
  });

  it.each(['ArrowDown', 'ArrowUp'])('blocks opening the scan weekday menu with %s', async key => {
    const { wrapper } = await page('security');
    useSettingsStore().value!.avSchedule = 'weekly';
    await flushPromises();
    await wrapper.get('[aria-haspopup="menu"]').trigger('keydown', { key });
    await flushPromises();
    expect(document.querySelector('[role="menu"]')).toBeNull();
    expect(usePermissionGateStore().current?.feature).toBe('security');
  });

  it('checks permission when saving from a menu opened before permission was revoked', async () => {
    const { wrapper, permissions } = await page('security', true);
    const settings = useSettingsStore();
    settings.value!.avSchedule = 'weekly';
    await flushPromises();
    await wrapper.get('[aria-haspopup="menu"]').trigger('keydown', { key: 'ArrowDown' });
    await flushPromises();
    const items = document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]');
    expect(items).toHaveLength(7);
    permissions.apply({ ...permissions.status!, appManagementSwitch: 'denied' });
    await flushPromises();
    const update = vi.spyOn(commands, 'settingsSet');
    items[1]!.click();
    await flushPromises();
    expect(update).not.toHaveBeenCalled();
    expect(usePermissionGateStore().current?.feature).toBe('security');
    usePermissionGateStore().dismiss();
    await flushPromises();
    expect(update).not.toHaveBeenCalled();
    expect(settings.value?.avScheduleWeekday).toBe(1);
  });

  it('allows helper uninstall and system menu bar settings but blocks approval', async () => {
    const { wrapper } = await page('monitor');
    const store = useMonitorStore();
    store.available = [...store.available, 'fans'];
    store.helper = { status: 'enabled', support: 'unverified', manualAllowed: true };
    const uninstall = vi.spyOn(store, 'uninstallHelper').mockResolvedValue();
    const systemSettings = vi.spyOn(store, 'openMenuBarSettings').mockResolvedValue(null);
    const approval = vi.spyOn(store, 'openApproval').mockResolvedValue(null);
    await flushPromises();
    const button = (label: string) => wrapper.findAll('button').find(item => item.text() === label)!;
    await button(i18n.global.t('monitor.settings.uninstall')).trigger('click');
    await flushPromises();
    expect(uninstall).toHaveBeenCalledOnce();
    await button(i18n.global.t('monitor.settings.openSettings')).trigger('click');
    await flushPromises();
    expect(systemSettings).toHaveBeenCalledOnce();
    expect(usePermissionGateStore().current).toBeNull();
    store.helper = { status: 'requires_approval', support: 'unverified', manualAllowed: true };
    await flushPromises();
    const openButtons = wrapper
      .findAll('button')
      .filter(item => item.text() === i18n.global.t('monitor.settings.openSettings'));
    await openButtons.at(-1)!.trigger('click');
    await flushPromises();
    expect(approval).not.toHaveBeenCalled();
    expect(usePermissionGateStore().current?.feature).toBe('monitor');
  });

  it('permits changes after authorization', async () => {
    const { wrapper } = await page('security', true);
    const update = vi.spyOn(commands, 'settingsSet');
    await wrapper.get('[aria-labelledby="setting-avKeepResident"]').trigger('click');
    await flushPromises();
    expect(update).toHaveBeenCalled();
    expect(useSettingsStore().value?.avKeepResident).toBe(true);
    expect(usePermissionGateStore().current).toBeNull();
  });
});
