import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { i18n } from '@/i18n';
import { commands } from '@/ipc/client';
import SecurityPage from '@/pages/SecurityPage.vue';
import MonitorPage from '@/pages/MonitorPage.vue';
import FirewallPage from '@/pages/FirewallPage.vue';
import { OnMenu } from '@opennavo/ui';
import { useToasts } from '@/composables/useToasts';
import { usePermissionsStore } from '@/stores/permissions';
import { usePermissionGateStore } from '@/stores/permissionGate';
import { useSecurityStore } from '@/stores/security';
import { useFirewallStore } from '@/stores/firewall';
import { useLocalAppsStore } from '@/stores/localApps';
import { useMonitorStore } from '@/stores/monitor';
import { setup } from './helpers';

enableAutoUnmount(afterEach);
afterEach(() => {
  useToasts().clear();
  document.body.innerHTML = '';
  vi.restoreAllMocks();
  history.replaceState(null, '', '/');
});

async function locked(path = '/security') {
  const context = await setup(path);
  const permissions = usePermissionsStore();
  await permissions.refresh();
  permissions.apply({ ...permissions.status!, appManagementSwitch: 'denied', fullDiskAccess: 'granted' });
  vi.spyOn(commands, 'permissionsGet').mockImplementation(async () => ({ status: 'ok', data: permissions.status! }));
  return context;
}

describe('security and monitor permission boundaries', () => {
  it('rechecks permission when an already open firewall rule sheet submits', async () => {
    history.replaceState(null, '', '/?mock=firewall-on');
    const { pinia, router } = await locked('/firewall');
    const permissions = usePermissionsStore();
    permissions.apply({ ...permissions.status!, appManagementSwitch: 'granted' });
    const wrapper = mount(FirewallPage, { global: { plugins: [pinia, router, i18n] }, attachTo: document.body });
    await flushPromises();
    const menu = wrapper.findAllComponents(OnMenu).find(item => String(item.props('label')).includes('Google Chrome'))!;
    menu.vm.$emit('select', 'edit');
    await flushPromises();
    const sheet = document.querySelector<HTMLElement>('[role="dialog"]')!;
    expect(sheet).not.toBeNull();
    const form = sheet.querySelector('form')!;
    const store = useFirewallStore();
    const previous = [...store.rules];
    permissions.apply({ ...permissions.status!, appManagementSwitch: 'denied' });
    await flushPromises();
    const gate = usePermissionGateStore();
    expect(gate.current?.feature).toBe('firewall');
    gate.dismiss();
    const save = vi.spyOn(commands, 'firewallRuleSave');
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await flushPromises();
    expect(save).not.toHaveBeenCalled();
    expect(gate.current?.feature).toBe('firewall');
    gate.dismiss();
    await flushPromises();
    expect(store.rules).toEqual(previous);
    expect(store.busy.size).toBe(0);
    expect(useToasts().items.value.some(item => item.tone === 'success')).toBe(false);
    expect(document.body.contains(sheet)).toBe(true);
    // The draft can still be saved once the user grants permission and explicitly continues.
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await flushPromises();
    permissions.apply({ ...permissions.status!, appManagementSwitch: 'granted' });
    gate.proceed();
    await flushPromises();
    expect(save).toHaveBeenCalledOnce();
    expect(document.body.contains(sheet)).toBe(false);
  });

  it('does not scan startup items or preselect a preview while locked', async () => {
    const { pinia, router } = await locked('/security/startup?label=example&preview=1');
    const scan = vi.spyOn(commands, 'startupScan');
    const wrapper = mount(SecurityPage, { global: { plugins: [pinia, router, i18n] } });
    await flushPromises();
    expect(scan).not.toHaveBeenCalled();
    expect(wrapper.text()).toContain('example');
    expect(wrapper.findAll('[aria-hidden="true"] .animate-pulse')).toHaveLength(0);
  });

  it('does not load local apps for a locked scan preview', async () => {
    const { pinia, router } = await locked('/security/scan?token=firefox&preview=1');
    const scan = vi.spyOn(commands, 'avScanStart');
    const load = vi.spyOn(useLocalAppsStore(), 'load');
    const wrapper = mount(SecurityPage, { global: { plugins: [pinia, router, i18n] } });
    await flushPromises();
    expect(scan).not.toHaveBeenCalled();
    expect(wrapper.text()).toContain('firefox');
    expect(load).not.toHaveBeenCalled();
  });

  it('subscribes only after authorization and unsubscribes when it is revoked', async () => {
    const { pinia, router } = await locked('/monitor?detail=cpu');
    const monitor = useMonitorStore();
    const subscribe = vi.spyOn(monitor, 'subscribe').mockResolvedValue();
    const unsubscribe = vi.spyOn(monitor, 'unsubscribe').mockResolvedValue();
    const wrapper = mount(MonitorPage, { global: { plugins: [pinia, router, i18n] } });
    await flushPromises();
    expect(subscribe).not.toHaveBeenCalled();
    expect(wrapper.findComponent({ name: 'MonitorDetailSheet' }).props('module')).toBeNull();
    const permissions = usePermissionsStore();
    permissions.apply({ ...permissions.status!, appManagementSwitch: 'granted', relaunchRequired: false });
    await flushPromises();
    expect(subscribe).toHaveBeenCalledWith('page', expect.any(Array));
    unsubscribe.mockClear();
    permissions.apply({ ...permissions.status!, appManagementSwitch: 'denied' });
    await flushPromises();
    expect(unsubscribe).toHaveBeenCalledWith('page');
    expect(wrapper.findComponent({ name: 'MonitorDetailSheet' }).props('module')).toBeNull();
  });

  it('lists the monitor modules but shows no readings while locked, though samples exist', async () => {
    const { pinia, router } = await locked('/monitor');
    const wrapper = mount(MonitorPage, { global: { plugins: [pinia, router, i18n] } });
    await vi.waitFor(() =>
      expect(wrapper.findAll('[data-testid="monitor-cards"] [data-module]').length).toBeGreaterThan(0)
    );
    // The module-less snapshot brings recent history, and menu bar items keep sampling into the same store.
    expect(useMonitorStore().sample).not.toBeNull();
    const cards = wrapper.findAll('[data-testid="monitor-cards"] [data-module]');
    for (const card of cards) expect(card.find('header').text()).toContain('—');
    expect(wrapper.findComponent({ name: 'MonitorModuleBody' }).exists()).toBe(false);
    expect(wrapper.find('[data-testid="monitor-health"]').exists()).toBe(false);
  });

  it('dismissed actions issue no security commands', async () => {
    await locked();
    vi.spyOn(usePermissionGateStore(), 'require').mockResolvedValue(false);
    const security = useSecurityStore();
    const spies = (
      [
        'avScanStart',
        'startupItemAct',
        'startupLoginAdd',
        'quarantineRestore',
        'quarantineDelete',
        'avDbUpdate',
        'appSignatureCheck'
      ] as const
    ).map(name => vi.spyOn(commands, name));
    await security.startScan('quick');
    await security.actOnStartup(['example'], 'disable');
    await security.addLoginItem('/Applications/Example.app');
    await security.restore(['example']);
    await security.purge(['example']);
    await security.updateDatabase();
    await security.consentAndDownload();
    await security.checkSignatures();
    for (const spy of spies) expect(spy).not.toHaveBeenCalled();
    expect(security.busy.size).toBe(0);
    expect(security.checkingSignatures).toBe(false);
  });

  it('allows paste handling and firewall replies while locked', async () => {
    await locked();
    const gate = vi.spyOn(usePermissionGateStore(), 'require').mockResolvedValue(false);
    const security = useSecurityStore();
    security.paste = {
      id: 'paste-test',
      appName: 'Terminal',
      bundleId: 'com.example.terminal',
      patterns: [],
      preview: '',
      expiresAt: Date.now() + 10000
    };
    const paste = vi.spyOn(commands, 'pasteProtectionAct').mockResolvedValue({ status: 'ok', data: null });
    await security.actOnPaste('discard');
    expect(paste).toHaveBeenCalledWith('paste-test', 'discard');
    const reply = vi.spyOn(commands, 'firewallAlertReply').mockResolvedValue({ status: 'ok', data: null });
    await useFirewallStore().reply({ alertId: 'alert-test', action: 'allow', scope: 'app', until: 'always' });
    expect(reply).toHaveBeenCalledOnce();
    expect(gate).not.toHaveBeenCalled();
  });

  it('gates enabling the firewall but leaves disabling and removing available', async () => {
    await locked('/firewall');
    const gate = vi.spyOn(usePermissionGateStore(), 'require').mockResolvedValue(false);
    const firewall = useFirewallStore();
    await firewall.loadStatus();
    const enable = vi.spyOn(commands, 'firewallEnable');
    const disable = vi.spyOn(commands, 'firewallDisable').mockResolvedValue({ status: 'ok', data: firewall.status! });
    const uninstall = vi
      .spyOn(commands, 'firewallUninstall')
      .mockResolvedValue({ status: 'ok', data: firewall.status! });
    await firewall.enable(null);
    expect(enable).not.toHaveBeenCalled();
    gate.mockClear();
    await firewall.disable();
    await firewall.uninstall();
    expect(disable).toHaveBeenCalledOnce();
    expect(uninstall).toHaveBeenCalledOnce();
    expect(gate).not.toHaveBeenCalled();
  });
});
