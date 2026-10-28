import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { defineComponent, h, ref } from 'vue';
import type { Component } from 'vue';
import { createOnUi } from '@opennavo/ui';
import { i18n } from '@/i18n';
import { commands } from '@/ipc/client';
import { firewallMock } from '@/ipc/mock';
import PermissionGate from '@/components/permissions/PermissionGate.vue';
import PermissionGateModal from '@/components/permissions/PermissionGateModal.vue';
import FirewallRulesPanel from '@/components/firewall/FirewallRulesPanel.vue';
import FirewallBlocklistsPanel from '@/components/firewall/FirewallBlocklistsPanel.vue';
import FirewallActivityPanel from '@/components/firewall/FirewallActivityPanel.vue';
import OutdatedList from '@/components/updates/OutdatedList.vue';
import MonitorProcesses from '@/components/monitor/MonitorProcesses.vue';
import { useToasts } from '@/composables/useToasts';
import { startLocalState } from '@/stores';
import { usePermissionsStore } from '@/stores/permissions';
import { usePermissionGateStore } from '@/stores/permissionGate';
import type { GateFeature } from '@/stores/permissionGate';
import { useFirewallStore } from '@/stores/firewall';
import { useUpdatesStore } from '@/stores/updates';
import { useMonitorStore } from '@/stores/monitor';
import * as files from '@/utils/files';
import { setup } from './helpers';
import type { TestContext } from './helpers';

enableAutoUnmount(afterEach);
afterEach(() => {
  firewallMock()?.stop();
  useToasts().clear();
  vi.restoreAllMocks();
  history.replaceState(null, '', '/');
  document.body.innerHTML = '';
});

async function context(feature: GateFeature) {
  history.replaceState(null, '', '/?mock=firewall-on,update-sources,cpu-busy');
  const result = await setup(`/${feature}`, { tickMs: 5 });
  await startLocalState();
  const permissions = usePermissionsStore();
  await permissions.refresh();
  permissions.apply({
    ...permissions.status!,
    appManagementSwitch: 'granted',
    fullDiskAccess: 'granted',
    relaunchRequired: false
  });
  expect(usePermissionGateStore().ready).toBe(true);
  vi.spyOn(commands, 'permissionsGet').mockImplementation(async () => ({ status: 'ok', data: permissions.status! }));
  return result;
}

function panel(
  ctx: TestContext,
  feature: GateFeature,
  component: Component,
  props: () => Record<string, unknown> = () => ({})
) {
  return mount(
    defineComponent({
      setup: () => () => [
        h(PermissionGate, { feature }, { default: () => h(component, props()) }),
        h(PermissionGateModal)
      ]
    }),
    {
      global: { plugins: [ctx.pinia, ctx.router, i18n, createOnUi({ locale: ref('zh-CN') })] },
      attachTo: document.body
    }
  );
}

function menuItem(label: string) {
  const item = [...document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')].find(
    node => node.textContent?.trim() === label
  );
  expect(item).toBeDefined();
  return item!;
}

async function escapeGate(feature: GateFeature) {
  const gate = usePermissionGateStore();
  expect(gate.current?.feature).toBe(feature);
  const prompt = document.querySelector<HTMLElement>('[role="dialog"]')!;
  expect(prompt).not.toBeNull();
  prompt.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }));
  await flushPromises();
  expect(gate.current).toBeNull();
  expect(gate.ready).toBe(false);
}

async function revoke(feature: GateFeature, layer: HTMLElement) {
  expect(layer).not.toBeNull();
  const permissions = usePermissionsStore();
  permissions.apply({ ...permissions.status!, appManagementSwitch: 'denied' });
  await flushPromises();
  await escapeGate(feature);
  expect(document.body.contains(layer)).toBe(true);
}

function grant() {
  const permissions = usePermissionsStore();
  permissions.apply({ ...permissions.status!, appManagementSwitch: 'granted' });
}

describe('Operations from teleported controls after permission revocation', () => {
  it.each(['cancel', 'continue'] as const)(
    'rechecks file imports after the picker returns and handles %s',
    async choice => {
      const ctx = await context('firewall');
      let select!: (path: string) => void;
      const picker = vi.spyOn(files, 'pickListFile').mockReturnValueOnce(
        new Promise(resolve => {
          select = resolve;
        })
      );
      const wrapper = panel(ctx, 'firewall', FirewallBlocklistsPanel);
      await flushPromises();
      const input = wrapper.get('input');
      const draft = 'https://example.com/pending-hosts';
      await input.setValue(draft);
      const lists = JSON.stringify(useFirewallStore().blocklists);
      const add = vi.spyOn(commands, 'firewallBlocklistAdd');
      await wrapper
        .findAll('button')
        .find(item => item.text() === i18n.global.t('firewall.blocklists.chooseFile'))!
        .trigger('click');
      expect(picker).toHaveBeenCalledOnce();
      const permissions = usePermissionsStore();
      permissions.apply({ ...permissions.status!, appManagementSwitch: 'denied' });
      await flushPromises();
      const gate = usePermissionGateStore();
      expect(gate.current?.feature).toBe('firewall');
      const path = '~/Documents/blocklist.txt';
      select(path);
      await flushPromises();
      expect(add).not.toHaveBeenCalled();
      if (choice === 'cancel') {
        await escapeGate('firewall');
        expect(add).not.toHaveBeenCalled();
        expect(useToasts().items.value).toHaveLength(0);
        expect(JSON.stringify(useFirewallStore().blocklists)).toBe(lists);
        expect(input.element.value).toBe(draft);
        expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeUndefined();
      } else {
        grant();
        await flushPromises();
        expect(add).not.toHaveBeenCalled();
        gate.proceed();
        await flushPromises();
        expect(add).toHaveBeenCalledExactlyOnceWith(path);
        expect(input.element.value).toBe('');
        expect(useToasts().items.value.at(-1)?.tone).toBe('success');
      }
    }
  );

  it.each(['rules', 'blocklists', 'activity'] as const)('blocks and cancels firewall %s deletion', async kind => {
    const ctx = await context('firewall');
    const components = {
      rules: FirewallRulesPanel,
      blocklists: FirewallBlocklistsPanel,
      activity: FirewallActivityPanel
    };
    const wrapper = panel(ctx, 'firewall', components[kind]);
    await flushPromises();
    const store = useFirewallStore();
    if (kind === 'rules') {
      await wrapper.get('button[aria-haspopup="menu"]').trigger('click');
      menuItem(i18n.global.t('firewall.rules.menu.delete')).click();
    } else if (kind === 'blocklists') {
      const list = store.blocklists[0]!;
      const label = i18n.global.t('firewall.blocklists.remove', { name: list.name });
      await wrapper.get(`button[aria-label="${label}"]`).trigger('click');
    } else {
      await wrapper
        .findAll('button')
        .find(item => item.text() === i18n.global.t('firewall.activity.clear'))!
        .trigger('click');
    }
    await flushPromises();
    const dialog = document.querySelector<HTMLElement>('[role="alertdialog"]')!;
    const confirm = dialog.querySelector<HTMLButtonElement>('[data-on-confirm]')!;
    await revoke('firewall', dialog);
    const state = JSON.stringify({
      rules: store.rules,
      blocklists: store.blocklists,
      activity: store.activity,
      more: store.activityMore
    });
    const ipc = {
      rules: 'firewallRuleDelete',
      blocklists: 'firewallBlocklistRemove',
      activity: 'firewallEventsClear'
    } as const;
    const command = vi.spyOn(commands, ipc[kind]);
    confirm.click();
    await flushPromises();
    expect(command).not.toHaveBeenCalled();
    await escapeGate('firewall');
    expect(command).not.toHaveBeenCalled();
    expect(
      JSON.stringify({
        rules: store.rules,
        blocklists: store.blocklists,
        activity: store.activity,
        more: store.activityMore
      })
    ).toBe(state);
    expect(store.busy.size).toBe(0);
    expect(useToasts().items.value).toHaveLength(0);
    expect(document.body.contains(dialog)).toBe(true);
    // Retry the retained confirmation, then explicitly approve after restoring permission.
    confirm.click();
    await flushPromises();
    expect(command).not.toHaveBeenCalled();
    grant();
    await flushPromises();
    expect(command).not.toHaveBeenCalled();
    usePermissionGateStore().proceed();
    await flushPromises();
    expect(command).toHaveBeenCalledOnce();
    expect(document.body.contains(dialog)).toBe(false);
  });

  it.each([
    ['stable', 'updateSetChannel'],
    ['skip', 'updateSkip'],
    ['ignoreApp', 'updateIgnore']
  ] as const)('blocks and cancels update preference %s from an open menu', async (action, ipc) => {
    const ctx = await context('updates');
    const store = useUpdatesStore();
    await store.load();
    const item = store.items.find(row => row.name === 'Sequel Ace')!;
    expect(item).toBeDefined();
    const wrapper = panel(ctx, 'updates', OutdatedList, () => ({
      items: store.items.filter(row => row.name === item.name),
      summaries: new Map()
    }));
    await wrapper.get('button[aria-haspopup="menu"]').trigger('click');
    await flushPromises();
    const menu = document.querySelector<HTMLElement>('[role="menu"]')!;
    await revoke('updates', menu);
    const state = JSON.stringify(store.items);
    const command = vi.spyOn(commands, ipc);
    const reload = vi.spyOn(commands, 'updatesList');
    const label = i18n.global.t(`updates.actions.${action}`);
    menuItem(label).click();
    await flushPromises();
    expect(command).not.toHaveBeenCalled();
    await escapeGate('updates');
    expect(command).not.toHaveBeenCalled();
    expect(reload).not.toHaveBeenCalled();
    expect(JSON.stringify(store.items)).toBe(state);
    expect(useToasts().items.value).toHaveLength(0);
    grant();
    await flushPromises();
    await wrapper.get('button[aria-haspopup="menu"]').trigger('click');
    menuItem(label).click();
    await flushPromises();
    expect(command).toHaveBeenCalledOnce();
    expect(reload).toHaveBeenCalledOnce();
    expect(useToasts().items.value).toHaveLength(1);
  });

  it('blocks and cancels quitting an app from an open process confirmation', async () => {
    const ctx = await context('monitor');
    const store = useMonitorStore();
    await store.subscribe('page', ['cpu']);
    await vi.waitFor(() => expect(store.sample?.processes?.some(item => item.name === 'Xcode')).toBe(true));
    await store.unsubscribe('page');
    const wrapper = panel(ctx, 'monitor', MonitorProcesses);
    const row = wrapper.findAll('li').find(item => item.text().includes('Xcode'))!;
    await row
      .findAll('button')
      .find(item => item.text() === i18n.global.t('monitor.processes.quit'))!
      .trigger('click');
    const dialog = document.querySelector<HTMLElement>('[role="alertdialog"]')!;
    const confirm = dialog.querySelector<HTMLButtonElement>('[data-on-confirm]')!;
    await revoke('monitor', dialog);
    const command = vi.spyOn(commands, 'monitorQuitApp');
    confirm.click();
    await flushPromises();
    expect(command).not.toHaveBeenCalled();
    await escapeGate('monitor');
    expect(command).not.toHaveBeenCalled();
    expect(useToasts().items.value).toHaveLength(0);
    expect(document.body.contains(dialog)).toBe(true);
    grant();
    await flushPromises();
    confirm.click();
    await flushPromises();
    expect(command).toHaveBeenCalledOnce();
    expect(useToasts().items.value.at(-1)?.tone).toBe('success');
    expect(document.body.contains(dialog)).toBe(false);
  });
});
