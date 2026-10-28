import { flushPromises, mount } from '@vue/test-utils';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { defineComponent } from 'vue';
import { createPinia, setActivePinia } from 'pinia';
import { gateReady, usePermissionsStore } from '@/stores/permissions';
import { usePermissionGateStore } from '@/stores/permissionGate';
import PermissionGate from '@/components/permissions/PermissionGate.vue';
import type { Permissions } from '@/ipc/bindings';
import { i18n } from '@/i18n';
const granted: Permissions = {
  appManagement: 'granted',
  appManagementSwitch: 'granted',
  fullDiskAccess: 'granted',
  relaunchRequired: false,
  appManagementPending: false,
  fullDiskAccessPending: false
};
function setup(status: Permissions | null = { ...granted, appManagementSwitch: 'denied' }) {
  const pinia = createPinia();
  setActivePinia(pinia);
  const permissions = usePermissionsStore();
  permissions.status = status;
  vi.spyOn(permissions, 'refresh').mockResolvedValue(status);
  return { pinia, permissions, gate: usePermissionGateStore() };
}
afterEach(() => {
  vi.restoreAllMocks();
  localStorage.clear();
  delete document.documentElement.dataset.opennavoWindow;
});
describe('Permission readiness', () => {
  it('requires the explicit App Management switch, effective disk access, and no relaunch', () => {
    expect(gateReady(null, true)).toBe(false);
    expect(gateReady(granted, false)).toBe(true);
    expect(gateReady({ ...granted, appManagementSwitch: 'denied' }, true)).toBe(false);
    expect(gateReady({ ...granted, relaunchRequired: true }, true)).toBe(false);
    expect(gateReady({ ...granted, fullDiskAccess: 'unknown' }, false)).toBe(false);
    expect(gateReady({ ...granted, appManagementSwitch: 'unknown', fullDiskAccess: 'unknown' }, true)).toBe(true);
  });
  it('bypasses the gate in auxiliary windows', async () => {
    document.documentElement.dataset.opennavoWindow = 'monitor';
    const { gate } = setup();
    expect(await gate.require({ feature: 'monitor' })).toBe(true);
    expect(gate.current).toBeNull();
  });
});
describe('Permission gate requests', () => {
  it('merges concurrent targets, waits for explicit confirmation, and cancels all callers', async () => {
    const { gate, permissions } = setup();
    const a = gate.require({ feature: 'install', targets: [{ kind: 'cask', token: 'a' }] });
    const b = gate.require({
      feature: 'install',
      targets: [
        { kind: 'cask', token: 'a' },
        { kind: 'cask', token: 'b' }
      ]
    });
    expect(gate.current?.targets).toHaveLength(2);
    gate.proceed();
    expect(gate.current).not.toBeNull();
    permissions.apply(granted);
    await flushPromises();
    expect(gate.current).not.toBeNull();
    gate.dismiss();
    expect(await a).toBe(false);
    expect(await b).toBe(false);
  });
  it('reads initial status before deciding and closes page-only requests on proceed', async () => {
    const { gate, permissions } = setup(null);
    gate.show('cleanup');
    expect(gate.current).toBeNull();
    vi.mocked(permissions.refresh).mockImplementation(async () => {
      permissions.apply(granted);
      return granted;
    });
    expect(await gate.require({ feature: 'cleanup' })).toBe(true);
    permissions.apply({ ...granted, fullDiskAccess: 'denied' });
    gate.show('cleanup');
    expect(gate.current).not.toBeNull();
    permissions.apply(granted);
    gate.proceed();
    expect(gate.current).toBeNull();
    permissions.apply({ ...granted, fullDiskAccess: 'denied' });
    gate.show('cleanup');
    expect(gate.current).toBeNull();
    gate.show('cleanup', true);
    expect(gate.current).not.toBeNull();
  });
  it('merges requests while the initial status is loading', async () => {
    const { gate, permissions } = setup(null);
    let complete!: (value: Permissions) => void;
    vi.mocked(permissions.refresh).mockImplementation(
      () =>
        new Promise(resolve => {
          complete = resolve;
        })
    );
    const a = gate.require({ feature: 'install', targets: [{ kind: 'cask', token: 'a' }] });
    const b = gate.require({ feature: 'install', targets: [{ kind: 'cask', token: 'b' }] });
    expect(gate.current).toBeNull();
    const denied = { ...granted, appManagementSwitch: 'denied' as const };
    permissions.apply(denied);
    complete(denied);
    await flushPromises();
    expect(gate.current?.targets).toHaveLength(2);
    gate.dismiss();
    expect(await a).toBe(false);
    expect(await b).toBe(false);
  });
  it('remembers only install targets when concurrent operations share the modal', () => {
    const { gate } = setup();
    void gate.require({
      feature: 'install',
      targets: [{ kind: 'cask', token: 'install-me' }],
      resume: { op: 'install', trigger: 'manual' }
    });
    void gate.require({ feature: 'installed', targets: [{ kind: 'cask', token: 'uninstall-me' }] });
    gate.rememberForRelaunch('/installed');
    expect(gate.takeRemembered()?.request.targets).toEqual([{ kind: 'cask', token: 'install-me' }]);
    gate.dismiss();
  });
  it('discards expired and malformed records', () => {
    const { gate } = setup();
    const key = 'opennavo.permissionGate.resume';
    localStorage.setItem(
      key,
      JSON.stringify({ at: Date.now() - 600001, route: '/installed', request: { feature: 'installed' } })
    );
    expect(gate.takeRemembered()).toBeNull();
    expect(localStorage.getItem(key)).toBeNull();
    localStorage.setItem(key, '{');
    expect(gate.takeRemembered()).toBeNull();
  });
  it('offers a resumed install even when already ready and consumes records only once', async () => {
    const { gate } = setup(granted);
    const pending = gate.offer({
      feature: 'install',
      targets: [{ kind: 'cask', token: 'a' }],
      resume: { op: 'install', trigger: 'manual' }
    });
    gate.rememberForRelaunch('/installed');
    expect(gate.takeRemembered()?.route).toBe('/installed');
    expect(gate.takeRemembered()).toBeNull();
    gate.proceed();
    expect(await pending).toBe(true);
  });
  it.each([false, true])('clears saved recovery when a request finishes with %s', async proceed => {
    const { gate, permissions } = setup();
    const pending = gate.require({
      feature: 'install',
      targets: [{ kind: 'cask', token: 'a' }],
      resume: { op: 'install', trigger: 'manual' }
    });
    gate.rememberForRelaunch('/installed');
    if (proceed) {
      permissions.apply(granted);
      gate.proceed();
    } else gate.dismiss();
    expect(await pending).toBe(proceed);
    // A new application instance must not recover a canceled or already completed request.
    expect(setup().gate.takeRemembered()).toBeNull();
  });
});
describe('Permission wrapper', () => {
  it('blocks actions while the initial permission query is pending or fails', async () => {
    const { pinia, permissions } = setup(null);
    let fail!: (error: Error) => void;
    const query = new Promise<null>((_, reject) => {
      fail = reject;
    });
    vi.mocked(permissions.refresh).mockReturnValue(query);
    const action = vi.fn();
    const wrapper = mount(
      defineComponent({
        components: { PermissionGate },
        setup: () => ({ action }),
        template: `<PermissionGate feature="monitor" variant="section"><button @click="action">Enable</button></PermissionGate>`
      }),
      { global: { plugins: [pinia, i18n] } }
    );
    await wrapper.get('button').trigger('click');
    expect(action).not.toHaveBeenCalled();
    fail(new Error('permission query failed'));
    await flushPromises();
    await wrapper.get('button').trigger('click');
    await flushPromises();
    expect(action).not.toHaveBeenCalled();
    wrapper.unmount();
  });

  it('intercepts actions and slider gestures while allowing tabs, filtering, links, and safety exits', async () => {
    const { pinia, gate } = setup();
    const action = vi.fn();
    const wrapper = mount(
      defineComponent({
        components: { PermissionGate },
        setup: () => ({ action }),
        template: `<PermissionGate feature="cleanup" variant="rail"><template #header><button id="header" @click="action">Header</button></template><button id="action" @click="action" @keydown="action">Action</button><input type="range" @pointerdown="action"/><button data-permission-free @click="action">Cancel</button><button role="tab" @click="action">Tab</button><input type="search" @keydown="action"/></PermissionGate>`
      }),
      { global: { plugins: [pinia, i18n] } }
    );
    await wrapper.get('#header').trigger('click');
    expect(action).not.toHaveBeenCalled();
    await wrapper.get('#action').trigger('click');
    expect(action).not.toHaveBeenCalled();
    expect(gate.current?.feature).toBe('cleanup');
    gate.dismiss();
    await wrapper.get('#action').trigger('keydown', { key: 'Enter' });
    expect(action).not.toHaveBeenCalled();
    await wrapper.get('input[type=range]').trigger('pointerdown');
    expect(action).not.toHaveBeenCalled();
    await wrapper.get('[data-permission-free]').trigger('click');
    await wrapper.get('[role=tab]').trigger('click');
    await wrapper.get('input[type=search]').trigger('keydown', { key: ' ' });
    expect(action).toHaveBeenCalledTimes(3);
    wrapper.unmount();
  });

  it('keeps keyboard edits of value fields such as a schedule time from saving, while Tab still moves on', async () => {
    const { pinia, gate } = setup();
    const save = vi.fn();
    const wrapper = mount(
      defineComponent({
        components: { PermissionGate },
        setup: () => ({ save }),
        template: `<PermissionGate feature="security" variant="section"><input id="time" type="time" value="09:00" @change="save" @input="save" /><select id="pick" @change="save"><option>a</option><option>b</option></select><input id="filter" type="text" @input="save" /></PermissionGate>`
      }),
      { global: { plugins: [pinia, i18n] } }
    );
    const tab = new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true });
    wrapper.get('#time').element.dispatchEvent(tab);
    expect(tab.defaultPrevented).toBe(false);
    expect(gate.current).toBeNull();
    const arrow = new KeyboardEvent('keydown', { key: 'ArrowUp', bubbles: true, cancelable: true });
    wrapper.get('#time').element.dispatchEvent(arrow);
    expect(arrow.defaultPrevented).toBe(true);
    expect(gate.current?.feature).toBe('security');
    gate.dismiss();
    await wrapper.get('#time').trigger('change');
    await wrapper.get('#time').trigger('input');
    await wrapper.get('#pick').trigger('change');
    expect(save).not.toHaveBeenCalled();
    await wrapper.get('#filter').trigger('input');
    expect(save).toHaveBeenCalledTimes(1);
    wrapper.unmount();
  });
});
