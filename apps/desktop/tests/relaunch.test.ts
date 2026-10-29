import { flushPromises, mount } from '@vue/test-utils';
import type { VueWrapper } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { relaunch } from '@tauri-apps/plugin-process';
import PermissionGateModal from '@/components/permissions/PermissionGateModal.vue';
import RelaunchNotice from '@/components/permissions/RelaunchNotice.vue';
import { useAppUpdaterStore } from '@/stores/appUpdater';
import { usePermissionGateStore } from '@/stores/permissionGate';
import { usePermissionsStore } from '@/stores/permissions';
import { useToasts } from '@/composables/useToasts';
import { commands } from '@/ipc/client';
import { i18n } from '@/i18n';
import { setup } from './helpers';

vi.mock('@tauri-apps/plugin-process', () => ({ relaunch: vi.fn() }));

let wrapper: VueWrapper | undefined;
beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  useToasts().clear();
});
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
  document.body.innerHTML = '';
  window.history.replaceState(null, '', '/');
  vi.restoreAllMocks();
});

async function context(ineffective = false) {
  window.history.replaceState(null, '', ineffective ? '/?mock=permission-relaunch-ineffective' : '/');
  const result = await setup('/cleanup?from=installed');
  const permissions = usePermissionsStore();
  await permissions.refresh();
  permissions.apply({
    ...permissions.status!,
    fullDiskAccess: 'denied',
    fullDiskAccessPending: true,
    relaunchRequired: true
  });
  // Opening the gate rereads the same native state.
  vi.spyOn(commands, 'permissionsGet').mockResolvedValue({ status: 'ok', data: permissions.status! });
  return { plugins: [result.pinia, result.router, i18n] };
}

function primaryButton() {
  return [...document.body.querySelectorAll<HTMLButtonElement>('button')].at(-1)!;
}

describe('Permission relaunch recovery', () => {
  it('Makes reopen the enabled modal primary action and remembers the gate before native restart', async () => {
    const global = await context();
    const gate = usePermissionGateStore();
    gate.show('cleanup');
    wrapper = mount(PermissionGateModal, { global, attachTo: document.body });
    await flushPromises();
    expect(gate.ready).toBe(false);
    const button = primaryButton();
    expect(button.textContent).toBe(i18n.global.t('permission.relaunch.action'));
    expect(button.disabled).toBe(false);
    let remembered: ReturnType<typeof gate.takeRemembered> = null;
    vi.mocked(relaunch).mockImplementationOnce(async () => {
      remembered = gate.takeRemembered();
    });
    button.click();
    await flushPromises();
    expect(relaunch).toHaveBeenCalledOnce();
    expect(remembered).toEqual({ route: '/cleanup?from=installed', request: { feature: 'cleanup' } });
  });

  it('Keeps reopen out of the primary action while App Management is still off', async () => {
    const global = await context();
    const permissions = usePermissionsStore();
    permissions.apply({ ...permissions.status!, appManagementSwitch: 'denied' });
    vi.spyOn(commands, 'permissionsGet').mockResolvedValue({ status: 'ok', data: permissions.status! });
    usePermissionGateStore().show('cleanup');
    wrapper = mount(PermissionGateModal, { global, attachTo: document.body });
    await flushPromises();
    expect(primaryButton().textContent).toBe(i18n.global.t('welcome.done'));
    expect(primaryButton().disabled).toBe(true);
    // The notice still offers the reopen for whenever the user wants it.
    expect(document.body.textContent).toContain(i18n.global.t('permission.relaunch.action'));
  });

  it('Does not offer another primary restart after an ineffective relaunch', async () => {
    const global = await context(true);
    usePermissionGateStore().show('cleanup');
    wrapper = mount(PermissionGateModal, { global, attachTo: document.body });
    await flushPromises();
    expect(primaryButton().textContent).toBe(i18n.global.t('welcome.done'));
    expect(primaryButton().disabled).toBe(true);
    expect(document.body.textContent).toContain(i18n.global.t('permission.relaunch.ineffective'));
    expect(document.body.textContent).toContain(i18n.global.t('permission.relaunch.openSettings'));
    expect(relaunch).not.toHaveBeenCalled();
  });

  it.each([
    [false, false, 'body'],
    [false, true, 'updated'],
    [true, false, 'ineffective'],
    [true, true, 'ineffective']
  ] as const)('Shows the correct notice (ineffective=%s, installed=%s)', async (ineffective, installed, key) => {
    const global = await context(ineffective);
    if (installed) useAppUpdaterStore().state = 'installed';
    wrapper = mount(RelaunchNotice, { global });
    expect(wrapper.get('p').text()).toBe(i18n.global.t(`permission.relaunch.${key}`));
    expect(wrapper.get('button').text()).toBe(
      i18n.global.t(ineffective ? 'permission.relaunch.openSettings' : 'permission.relaunch.action')
    );
    const settings = vi.spyOn(commands, 'permissionSettingsOpen');
    const guide = vi.spyOn(commands, 'permissionGuideOpen');
    await wrapper.get('button').trigger('click');
    await flushPromises();
    if (ineffective) {
      expect(settings).toHaveBeenCalledExactlyOnceWith('full_disk_access');
      expect(relaunch).not.toHaveBeenCalled();
    } else {
      expect(relaunch).toHaveBeenCalledOnce();
      expect(settings).not.toHaveBeenCalled();
    }
    expect(guide).not.toHaveBeenCalled();
  });

  it.each(['active_tasks', 'exit_in_progress', 'launch failed'])(
    'Handles permission restart refusal: %s',
    async reason => {
      const global = await context();
      wrapper = mount(RelaunchNotice, { global });
      vi.mocked(relaunch).mockRejectedValueOnce(new Error(reason));
      await wrapper.get('button').trigger('click');
      await flushPromises();
      expect(wrapper.get('button').attributes('aria-busy')).toBeUndefined();
      const items = useToasts().items.value;
      if (reason === 'exit_in_progress') expect(items).toHaveLength(0);
      else {
        expect(items).toHaveLength(1);
        expect(items[0]).toMatchObject(
          reason === 'active_tasks'
            ? { tone: 'warning', title: i18n.global.t('permission.relaunch.busy') }
            : { tone: 'danger', title: i18n.global.t('errors.actionFailed'), description: reason }
        );
      }
    }
  );

  it('Reports a System Settings failure and allows retry without restarting or opening guidance', async () => {
    const global = await context(true);
    wrapper = mount(RelaunchNotice, { global });
    const settings = vi.spyOn(commands, 'permissionSettingsOpen').mockResolvedValueOnce({
      status: 'error',
      error: { code: 'internal', message: 'settings unavailable', detail: null }
    });
    const guide = vi.spyOn(commands, 'permissionGuideOpen');
    await wrapper.get('button').trigger('click');
    await flushPromises();
    expect(useToasts().items.value[0]).toMatchObject({
      tone: 'danger',
      title: i18n.global.t('errors.actionFailed'),
      description: 'settings unavailable'
    });
    expect(wrapper.get('button').attributes('aria-busy')).toBeUndefined();
    await wrapper.get('button').trigger('click');
    await flushPromises();
    expect(settings).toHaveBeenCalledTimes(2);
    expect(relaunch).not.toHaveBeenCalled();
    expect(guide).not.toHaveBeenCalled();
  });
});
