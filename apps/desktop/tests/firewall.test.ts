import { flushPromises, mount } from '@vue/test-utils';
import type { VueWrapper } from '@vue/test-utils';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { Component } from 'vue';
import { OnErrorState, OnMenu } from '@opennavo/ui';
import { useToasts } from '@/composables/useToasts';
import { i18n } from '@/i18n';
import { commands, events, unwrap } from '@/ipc/client';
import { firewallMock } from '@/ipc/mock';
import { ANSWER_DELAY_MS } from '@/components/firewall/meta';
import AppSidebar from '@/components/shell/AppSidebar.vue';
import SecurityProtectionPanel from '@/components/security/SecurityProtectionPanel.vue';
import FirewallPage from '@/pages/FirewallPage.vue';
import FirewallAlertPage from '@/pages/FirewallAlertPage.vue';
import { startFirewallAlertState, startLocalState } from '@/stores';
import { ACTIVITY_LIMIT, SWITCH_TIMEOUT_MS, useFirewallStore } from '@/stores/firewall';
import { setup } from './helpers';
import type { TestContext } from './helpers';

// App firewall (firewall plan, M7 / S4): its own Tools entry below Scan for Viruses, macOS's two confirmations on the
// way in, rules by program, activity, block lists, settings, and the question window that answers waiting connections.
const wrappers: VueWrapper[] = [];
afterEach(() => {
  wrappers.splice(0).forEach(wrapper => wrapper.unmount());
  firewallMock()?.stop();
  vi.useRealTimers();
  useToasts().clear();
  vi.restoreAllMocks();
  history.replaceState(null, '', '/');
  document.body.innerHTML = '';
});

const TICK = 15;
async function settle(ticks = 0) {
  if (ticks) await vi.advanceTimersByTimeAsync(TICK * ticks + 5);
  await flushPromises();
  await flushPromises();
}

function mountIn(context: TestContext, component: Component) {
  const wrapper = mount(component, {
    global: { plugins: [context.pinia, context.router, i18n] },
    attachTo: document.body
  });
  wrappers.push(wrapper);
  return wrapper;
}

async function open(path: string, flags = '', component: Component = FirewallPage) {
  // All firewall mock callbacks are scheduler-driven; advance them directly so CI load cannot race assertions.
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
  history.replaceState(null, '', flags ? `/?mock=${flags}` : '/');
  const context = await setup(path, { tickMs: TICK });
  await startLocalState();
  const wrapper = mountIn(context, component);
  await settle();
  return { ...context, wrapper };
}

const button = (wrapper: VueWrapper, text: string) => wrapper.findAll('button').find(item => item.text() === text);
const bodyButton = (text: string) =>
  [...document.body.querySelectorAll<HTMLButtonElement>('button')].find(item => item.textContent?.trim() === text);
/** A button inside the open dialog (sheets and confirmations render into the body). */
const dialogButton = (text: string) =>
  [...document.body.querySelectorAll<HTMLButtonElement>('[role="dialog"] button, [role="alertdialog"] button')].find(
    item => item.textContent?.trim() === text
  );
const radio = (wrapper: VueWrapper, text: string) =>
  wrapper.findAll('[role="radio"]').find(item => item.text() === text)!;
const lastToast = () => useToasts().items.value.at(-1);
const menuFor = (wrapper: VueWrapper, name: string) =>
  wrapper.findAllComponents(OnMenu).find(item => String(item.props('label')) === `「${name}」的规则操作`)!;

describe('Place in the app', () => {
  it('Sits in Tools right below Scan for Viruses and counts connections waiting for an answer', async () => {
    const { wrapper } = await open('/firewall', 'firewall-alerts', AppSidebar);
    const tools = wrapper.findAll('nav > div').find(group => group.find('div').text() === '工具')!;
    expect(tools.findAll('a').map(link => link.find('span.truncate').text())).toEqual([
      '扫描病毒',
      '应用防火墙',
      '电脑监控'
    ]);
    const link = tools.findAll('a').find(item => item.text().startsWith('应用防火墙'))!;
    expect(link.attributes('href')).toContain('/firewall');
    expect(link.find('[aria-label="2 个连接等待确认"]').text()).toBe('2');
    // Not under Library any more.
    const mine = wrapper.findAll('nav > div').find(group => group.find('div').text() === '我的')!;
    expect(mine.text()).not.toContain('扫描病毒');
  });

  it('Is no longer a "coming later" row under Scan for Viruses', async () => {
    const { wrapper } = await open('/security/protection', '', SecurityProtectionPanel);
    expect(wrapper.text()).not.toContain('应用防火墙');
    expect(wrapper.text()).toContain('S3 · 等待 Apple 授权');
  });
});

describe('Turning it on', () => {
  it('Explains first, then follows macOS through extension approval and the filter dialog', async () => {
    const enable = vi.spyOn(commands, 'firewallEnable');
    const { wrapper } = await open('/firewall');
    // Observe each transient setup state without racing the CI runner's wall clock.
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    expect(wrapper.text()).toContain('每个 App 第一次连接网络时');
    // Keep Apple programs automatic, but ask about apps that are already installed too.
    const installed = wrapper.findAll('input[type="checkbox"]')[1]!;
    await installed.setValue(false);
    await button(wrapper, '开启应用防火墙')!.trigger('click');
    await settle();
    expect(enable).toHaveBeenCalledWith({
      allowApple: true,
      allowInstalled: false
    });
    expect(wrapper.text()).toContain('正在开启应用防火墙');

    await settle(1);
    expect(wrapper.find('[aria-current="step"]').text()).toBe('在「系统设置」中允许');
    const approval = vi.spyOn(commands, 'firewallOpenApproval');
    await button(wrapper, '打开系统设置')!.trigger('click');
    await settle(2);
    expect(approval).toHaveBeenCalledOnce();
    expect(wrapper.find('[aria-current="step"]').text()).toBe('允许过滤网络内容');

    await settle(1);
    await settle();
    expect(wrapper.text()).toContain('应用防火墙已开启');
    expect(wrapper.text()).toContain('0 条规则');
    // The installed-app baseline is gathered after filtering starts; the card says so until it is done.
    expect(wrapper.get('[data-firewall-preparing]').text()).toContain('正在检查已经安装的 App');
    await settle(2);
    expect(wrapper.find('[data-firewall-preparing]').exists()).toBe(false);
    expect(wrapper.findAll('[role="tab"], a').some(item => item.text() === '连接记录')).toBe(true);
  });

  it('Says it was removed while macOS waits for a restart to delete it, and turns on again right away', async () => {
    const { wrapper } = await open('/firewall', 'firewall-removal-pending');
    expect(wrapper.get('[data-firewall-removed]').text()).toContain('剩下的组件会在下次重启 Mac 时由 macOS 清除');
    expect(wrapper.text()).not.toContain('正在开启应用防火墙');
    expect(wrapper.text()).not.toContain('重启 Mac 后完成安装');
    const enable = vi.spyOn(commands, 'firewallEnable');
    await button(wrapper, '开启应用防火墙')!.trigger('click');
    await settle(1);
    expect(enable).toHaveBeenCalledOnce();
    expect(wrapper.find('[aria-current="step"]').text()).toBe('在「系统设置」中允许');
  });

  it('Says why it cannot run in a build without the extension, with nothing to click', async () => {
    const { wrapper } = await open('/firewall', 'firewall-not-bundled');
    expect(wrapper.text()).toContain('这个版本没有包含防火墙组件');
    expect(button(wrapper, '开启应用防火墙')).toBeUndefined();
  });

  it('Stays paused with the reason when the filter dialog is declined, and can ask again', async () => {
    const { wrapper } = await open('/firewall', 'firewall-consent-denied');
    // Drive the nested installation and consent callbacks instead of waiting for wall-clock time.
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    await button(wrapper, '开启应用防火墙')!.trigger('click');
    await settle(1);
    await button(wrapper, '打开系统设置')!.trigger('click');
    await settle(4);
    await settle();
    expect(useFirewallStore().status).toMatchObject({
      filter: 'off',
      error: { code: 'E_FW_CONSENT_DENIED' }
    });
    expect(wrapper.text()).toContain('应用防火墙已暂停');
    expect(wrapper.text()).toContain('没有允许过滤网络内容，防火墙没有开启');
    const enable = vi.spyOn(commands, 'firewallEnable');
    await wrapper.get('[role="switch"][aria-label="应用防火墙"]').trigger('click');
    await settle();
    expect(enable).toHaveBeenCalledOnce();
  });
});

describe('Running', () => {
  it('Switches what happens to connections without a rule', async () => {
    const save = vi.spyOn(commands, 'firewallSettingsSet');
    const { wrapper } = await open('/firewall', 'firewall-on');
    expect(wrapper.text()).toContain('应用防火墙已开启');
    expect(wrapper.text()).toContain('今天阻止 14 次');
    await radio(wrapper, '阻止并记录').trigger('click');
    await settle();
    expect(save).toHaveBeenCalledWith({
      mode: 'block',
      allowApple: true,
      allowInstalled: true
    });
    expect(wrapper.text()).toContain('新的 App 都连不上网，直到你为它添加允许规则。');
  });

  it('Keeps the switch on the requested state until macOS applies it, without bouncing back', async () => {
    const disable = vi.spyOn(commands, 'firewallDisable');
    const { wrapper } = await open('/firewall', 'firewall-on');
    // Hold the intermediate state until the test explicitly advances macOS's reply.
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    const toggle = () => wrapper.get('[aria-labelledby="firewall-status"] [role="switch"]');
    await toggle().trigger('click');
    await settle();
    // The reply still says "on": the switch shows off and waits, the card still says what is true.
    expect(disable).toHaveBeenCalledOnce();
    expect(toggle().attributes('aria-checked')).toBe('false');
    expect(toggle().attributes('disabled')).toBeDefined();
    expect(wrapper.text()).toContain('应用防火墙已开启');
    await settle(1);
    expect(wrapper.text()).toContain('应用防火墙已暂停');
    expect(toggle().attributes('aria-checked')).toBe('false');
    expect(toggle().attributes('disabled')).toBeUndefined();

    // Turning it back on doesn't ask for consent again, and goes the same way.
    await toggle().trigger('click');
    await settle();
    expect(toggle().attributes('aria-checked')).toBe('true');
    expect(toggle().attributes('disabled')).toBeDefined();
    expect(wrapper.text()).toContain('应用防火墙已暂停');
    await settle(1);
    expect(wrapper.text()).toContain('应用防火墙已开启');
    expect(wrapper.text()).not.toContain('允许过滤网络内容');
    expect(toggle().attributes('disabled')).toBeUndefined();
  });

  it('Keeps a status pushed while the command ran over its older reply', async () => {
    const { wrapper } = await open('/firewall', 'firewall-on');
    const store = useFirewallStore();
    const before = store.status!;
    vi.spyOn(commands, 'firewallDisable').mockImplementationOnce(async () => {
      await events.firewallStatus.emit({ ...before, filter: 'off' });
      return { status: 'ok', data: before };
    });
    const toggle = wrapper.get('[aria-labelledby="firewall-status"] [role="switch"]');
    await toggle.trigger('click');
    await settle();
    expect(store.status?.filter).toBe('off');
    expect(wrapper.text()).toContain('应用防火墙已暂停');
    expect(toggle.attributes('aria-checked')).toBe('false');
    expect(toggle.attributes('disabled')).toBeUndefined();
  });

  it('Gives the switch back when macOS reports a failure, or after 10 seconds without news', async () => {
    const { wrapper } = await open('/firewall', 'firewall-on');
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    const store = useFirewallStore();
    const toggle = () => wrapper.get('[aria-labelledby="firewall-status"] [role="switch"]');
    const before = store.status!;
    vi.spyOn(commands, 'firewallDisable')
      .mockResolvedValueOnce({ status: 'ok', data: before })
      .mockImplementationOnce(async () => {
        await events.firewallStatus.emit({
          ...before,
          error: { code: 'E_FW_FILTER', detail: null }
        });
        return { status: 'ok', data: before };
      });
    await toggle().trigger('click');
    await settle();
    expect(toggle().attributes('disabled')).toBeDefined();
    await vi.advanceTimersByTimeAsync(SWITCH_TIMEOUT_MS - 100);
    expect(toggle().attributes('disabled')).toBeDefined();
    await vi.advanceTimersByTimeAsync(100);
    await settle();
    // Nothing changed: the switch shows the real state again.
    expect(toggle().attributes('disabled')).toBeUndefined();
    expect(toggle().attributes('aria-checked')).toBe('true');

    await toggle().trigger('click');
    await settle();
    expect(toggle().attributes('disabled')).toBeUndefined();
    expect(toggle().attributes('aria-checked')).toBe('true');
    expect(wrapper.get('[aria-labelledby="firewall-status"] [role="alert"]').text()).toBe(
      'macOS 没能更新网络过滤设置，请重试'
    );
  });

  it('Says once when the firewall stops answering, and clears it when the extension answers again', async () => {
    const { wrapper } = await open('/firewall', 'firewall-disconnected');
    const card = () => wrapper.get('[aria-labelledby="firewall-status"]');
    expect(card().get('[data-firewall-disconnected]').text()).toContain('防火墙组件没有响应。');
    expect(wrapper.find('[data-firewall-service-missing]').exists()).toBe(false);
    // The notice says it: no red copy of E_FW_DISCONNECTED under it, and settings it could not read are no page error.
    expect(card().find('[role="alert"]').exists()).toBe(false);
    expect(card().find('[role="radiogroup"]').exists()).toBe(false);
    expect(wrapper.findComponent(OnErrorState).exists()).toBe(false);
    // The rules tab says why its list is missing.
    expect(wrapper.get('[role="alert"]').text()).toBe('防火墙组件没有响应，请稍后重试');

    firewallMock()!.reconnect();
    await settle();
    expect(wrapper.find('[data-firewall-disconnected]').exists()).toBe(false);
    expect(wrapper.find('[role="alert"]').exists()).toBe(false);
    expect(wrapper.findAll('[data-rule-group]').length).toBeGreaterThan(0);
    expect(card().find('[role="radiogroup"]').exists()).toBe(true);
  });

  it('Asks for a restart when macOS started the extension without its service, in one message', async () => {
    const { wrapper } = await open('/firewall', 'firewall-service-missing');
    const card = wrapper.get('[aria-labelledby="firewall-status"]');
    const notice = card.get('[data-firewall-service-missing]');
    expect(notice.attributes('role')).toBe('alert');
    expect(notice.text()).toContain('防火墙服务没有正常启动。');
    expect(notice.text()).toContain('重启 Mac 后会恢复。');
    // One message: neither the generic "not responding" note nor the raw error line repeats it.
    expect(card.text()).not.toContain('防火墙组件没有响应');
    expect(card.findAll('[role="alert"]')).toHaveLength(1);
    expect(wrapper.findComponent(OnErrorState).exists()).toBe(false);
  });

  it('Still shows other failures under the notice, such as a filter macOS would not save', async () => {
    const { wrapper } = await open('/firewall', 'firewall-disconnected');
    const store = useFirewallStore();
    store.applyStatus({
      ...store.status!,
      error: { code: 'E_FW_FILTER', detail: null }
    });
    await settle();
    const card = wrapper.get('[aria-labelledby="firewall-status"]');
    expect(card.find('[data-firewall-disconnected]').exists()).toBe(true);
    expect(card.get('[role="alert"]').text()).toBe('macOS 没能更新网络过滤设置，请重试');
  });

  it('Shows a status pushed while the page read failed instead of the failure', async () => {
    const real = commands.firewallStatus;
    let fail!: (error: unknown) => void;
    // The app's own read at start, then the page's, which fails after a status was pushed.
    vi.spyOn(commands, 'firewallStatus')
      .mockImplementationOnce(() => real())
      .mockReturnValueOnce(new Promise((_, reject) => (fail = reject)));
    const { wrapper } = await open('/firewall', 'firewall-on');
    const pushed = await unwrap(real());
    useFirewallStore().applyStatus({ ...pushed, blockedToday: 15 });
    fail(new Error('offline'));
    await settle();
    expect(wrapper.findComponent(OnErrorState).exists()).toBe(false);
    expect(wrapper.text()).toContain('今天阻止 15 次');
  });

  it('Shows a new connection waiting for an answer and brings up the question window', async () => {
    const show = vi.spyOn(commands, 'firewallAlertShow');
    const { wrapper } = await open('/firewall', 'firewall-on');
    firewallMock()!.ask('firefox');
    await settle();
    expect(useFirewallStore().alerts).toHaveLength(1);
    expect(wrapper.text()).toContain('1 个连接等待你确认');
    await button(wrapper, '去确认')!.trigger('click');
    await settle();
    expect(show).toHaveBeenCalledOnce();
  });

  it('Lists rules by program, rules for every app first, and searches them', async () => {
    const { wrapper } = await open('/firewall', 'firewall-on');
    const groups = wrapper.findAll('[data-rule-group]');
    expect(groups[0]!.text()).toContain('所有 App');
    expect(groups[0]!.text()).toContain('*.doubleclick.net');
    const code = groups.find(group => group.text().includes('Visual Studio Code'))!;
    expect(code.text()).toContain('Microsoft Corporation');
    expect(code.text()).toContain('dc.services.visualstudio.com');
    expect(code.findAll('[data-rule]')).toHaveLength(2);
    const music = groups.find(group => group.text().includes('Music'))!;
    expect(music.text()).toContain('Apple 签名');
    expect(music.text()).toContain('Apple 程序');

    await wrapper.get('input[type="search"], input[aria-label="搜索 App、开发者或地址"]').setValue('visual');
    await settle();
    expect(wrapper.findAll('[data-rule-group]')).toHaveLength(1);
  });

  it('Turns an allow rule into a block, and deletes a rule after confirming', async () => {
    const save = vi.spyOn(commands, 'firewallRuleSave');
    const remove = vi.spyOn(commands, 'firewallRuleDelete');
    const { wrapper } = await open('/firewall', 'firewall-on');
    menuFor(wrapper, 'Google Chrome').vm.$emit('select', 'block');
    await settle();
    expect(save).toHaveBeenCalledWith(
      expect.objectContaining({
        appKey: 'EQHXZ8M8AV:com.google.Chrome',
        appPath: null,
        action: 'block',
        remote: null
      })
    );
    const chrome = () => wrapper.findAll('[data-rule-group]').find(group => group.text().includes('Google Chrome'));
    expect(chrome()!.text()).toContain('阻止');

    menuFor(wrapper, 'Google Chrome').vm.$emit('select', 'delete');
    await settle();
    bodyButton('删除')!.click();
    await settle();
    expect(remove).toHaveBeenCalledOnce();
    expect(chrome()).toBeUndefined();
    expect(lastToast()?.title).toBe('已删除规则');
  });

  it('Adds a rule for a chosen app and one destination, and refuses a rule that would cover the whole Mac', async () => {
    const save = vi.spyOn(commands, 'firewallRuleSave');
    const { wrapper } = await open('/firewall', 'firewall-on');
    await button(wrapper, '添加规则')!.trigger('click');
    await settle();
    bodyButton('选择 App 或程序…')!.click();
    await settle();
    const sheet = () => document.body.querySelector('[role="dialog"]')!;
    expect(sheet().textContent).toContain('Visual Studio Code');
    expect(sheet().textContent).toContain('Microsoft Corporation');
    const remoteRadio = [...sheet().querySelectorAll<HTMLButtonElement>('[role="radio"]')].find(
      item => item.textContent?.trim() === '指定地址'
    )!;
    remoteRadio.click();
    await settle();
    const input = sheet().querySelector<HTMLInputElement>('input[aria-label="目的地"]')!;
    input.value = 'Telemetry.Example.com';
    input.dispatchEvent(new Event('input'));
    bodyButton('保存')!.click();
    await settle();
    expect(save).toHaveBeenCalledWith({
      id: null,
      appKey: null,
      appPath: '/Applications/Visual Studio Code.app/Contents/MacOS/Electron',
      action: 'block',
      remote: 'telemetry.example.com',
      port: null
    });
    expect(lastToast()?.title).toBe('已添加规则');

    save.mockClear();
    await button(wrapper, '添加规则')!.trigger('click');
    await settle();
    [...sheet().querySelectorAll<HTMLButtonElement>('[role="radio"]')]
      .find(item => item.textContent?.trim() === '所有 App')!
      .click();
    await settle();
    expect(sheet().textContent).toContain('给所有 App 设的规则要指定地址或端口');
    bodyButton('保存')!.click();
    await settle();
    expect(save).not.toHaveBeenCalled();
  });
});

describe('Activity', () => {
  it('Shows blocks, answers and automatic allows, and turns a block into an allow rule for the app', async () => {
    const save = vi.spyOn(commands, 'firewallRuleSave');
    const { wrapper } = await open('/firewall/activity', 'firewall-on');
    expect(wrapper.text()).toContain('telemetry.example.net');
    expect(wrapper.text()).toContain('OpenNavo 没在运行，先放行');
    // Nobody answered in time while OpenNavo was running: a different reason from OpenNavo being closed.
    expect(wrapper.text()).toContain('没有及时回答，先放行');
    const helper = wrapper.findAll('[data-event]').find(row => row.text().includes('sync-helper'))!;
    expect(helper.text()).toContain('已阻止');
    expect(helper.text()).toContain('3 次');
    await helper
      .findAll('button')
      .find(item => item.text() === '允许这个 App')!
      .trigger('click');
    await settle();
    expect(save).toHaveBeenCalledWith({
      id: null,
      appKey: 'path:/Users/mock/.local/bin/sync-helper',
      appPath: null,
      action: 'allow',
      remote: null,
      port: null
    });
    expect(helper.text()).toContain('已改为允许');

    await radio(wrapper, '只看阻止').trigger('click');
    await settle();
    const rows = wrapper.findAll('[data-event]');
    expect(rows.length).toBeGreaterThan(0);
    expect(rows.every(row => row.text().includes('已阻止'))).toBe(true);
  });

  it('Makes a rule for the destination when a narrower rule or a block list decided the row', async () => {
    const save = vi.spyOn(commands, 'firewallRuleSave');
    const reads = vi.spyOn(commands, 'firewallRules');
    const { wrapper } = await open('/firewall/activity', 'firewall-on');
    // The rules behind the rows are read with them, so each button says what it will do.
    expect(reads).toHaveBeenCalledOnce();
    const row = (host: string) => wrapper.findAll('[data-event]').find(item => item.text().includes(host))!;
    const action = (host: string) => row(host).find('button');
    // A rule for the whole app comes after rules for a destination, rules for every app and block lists.
    expect(action('pagead2.googlesyndication.com').text()).toBe('允许这个地址');
    expect(action('dc.services.visualstudio.com').text()).toBe('允许这个地址');
    expect(action('stats.g.doubleclick.net').text()).toBe('允许这个地址');
    expect(action('www.mozilla.org').text()).toBe('阻止这个地址');
    // Rows no rule decided, or the app's own rule for any destination, still get one for the app.
    expect(action('telemetry.example.net').text()).toBe('允许这个 App');
    expect(action('push.services.mozilla.com').text()).toBe('阻止这个 App');
    expect(action('init.itunes.apple.com').text()).toBe('阻止这个 App');

    await action('pagead2.googlesyndication.com').trigger('click');
    await settle();
    expect(save).toHaveBeenCalledWith({
      id: null,
      appKey: 'EQHXZ8M8AV:com.google.Chrome',
      appPath: null,
      action: 'allow',
      remote: 'pagead2.googlesyndication.com',
      port: 443
    });
    expect(lastToast()?.title).toBe('已允许「Google Chrome」连接 pagead2.googlesyndication.com:443');
    expect(row('pagead2.googlesyndication.com').text()).toContain('已改为允许');

    await action('www.mozilla.org').trigger('click');
    await settle();
    expect(save).toHaveBeenLastCalledWith({
      id: null,
      appKey: '43AQ936H96:org.mozilla.firefox',
      appPath: null,
      action: 'block',
      remote: 'www.mozilla.org',
      port: 443
    });
    expect(lastToast()?.title).toBe('已阻止「Firefox」连接 www.mozilla.org:443');
    expect(row('www.mozilla.org').text()).toContain('已改为阻止');
    // Only rows going there are settled; Firefox's other rows keep their buttons.
    expect(action('stats.g.doubleclick.net').text()).toBe('允许这个地址');
    expect(action('push.services.mozilla.com').text()).toBe('阻止这个 App');
  });

  it.each(['192.0.2.7', '192.0.2.7/32', '2001:db8::7', '2001:db8::7/128'])(
    'Reuses the blocking exact address %s even when the activity has a hostname',
    async remote => {
      const save = vi.spyOn(commands, 'firewallRuleSave');
      const { wrapper } = await open('/firewall/activity', 'firewall-on');
      const store = useFirewallStore();
      const sample = store.activity[0]!;
      const matched = {
        ...store.rules[0]!,
        id: 'exact-ip-block',
        app: sample.app,
        action: 'block' as const,
        remote,
        port: 443
      };
      store.rules = [matched];
      store.activity = [
        {
          ...sample,
          id: 'exact-ip-event',
          action: 'block',
          ruleId: matched.id,
          blocklistId: null,
          remoteAddress: remote.split('/')[0]!,
          remoteHost: 'address.example',
          port: 443
        }
      ];
      await settle();
      await wrapper.get('[data-event] button').trigger('click');
      await settle();
      expect(save).toHaveBeenLastCalledWith(
        expect.objectContaining({
          appKey: sample.app.key,
          action: 'allow',
          remote,
          port: 443
        })
      );
      expect(wrapper.get('[data-event]').text()).toContain('已改为允许');
    }
  );

  it.each(['address', 'app', 'port', 'deleted'])(
    'Does not reuse a historical rule after its %s changed',
    async changed => {
      const save = vi.spyOn(commands, 'firewallRuleSave');
      const { wrapper } = await open('/firewall/activity', 'firewall-on');
      const store = useFirewallStore();
      const sample = store.activity[0]!;
      const rule = {
        ...store.rules[0]!,
        id: 'edited-rule',
        app: sample.app,
        action: 'block' as const,
        remote: '192.0.2.7',
        port: 443
      };
      if (changed === 'address') rule.remote = '192.0.2.8';
      if (changed === 'app') rule.app = { ...sample.app, key: 'other-app' };
      if (changed === 'port') rule.port = 8443;
      store.rules = changed === 'deleted' ? [] : [rule];
      store.activity = [
        {
          ...sample,
          action: 'block',
          ruleId: rule.id,
          blocklistId: null,
          remoteAddress: '192.0.2.7',
          remoteHost: 'historical.example',
          port: 443
        }
      ];
      await settle();
      await wrapper.get('[data-event] button').trigger('click');
      await settle();
      expect(save).toHaveBeenLastCalledWith(
        expect.objectContaining({
          id: null,
          appKey: sample.app.key,
          action: 'allow',
          remote: 'historical.example',
          port: 443
        })
      );
    }
  );

  it('Retries failed removal cleanup without activating the extension', async () => {
    const { wrapper } = await open('/firewall', 'firewall-removal-pending');
    const store = useFirewallStore();
    store.status = { ...store.status!, error: { code: 'E_FW_REMOVAL', detail: null } };
    const uninstall = vi.spyOn(store, 'uninstall').mockResolvedValue(undefined);
    const enable = vi.spyOn(store, 'enable');
    await settle();
    await button(wrapper, '重试')!.trigger('click');
    await settle();
    expect(uninstall).toHaveBeenCalledOnce();
    expect(enable).not.toHaveBeenCalled();
  });

  it('Reads the rules first when a row decided by a rule is clicked before they could be read', async () => {
    // The first read fails; the click reads them again before it decides.
    vi.spyOn(commands, 'firewallRules').mockRejectedValueOnce(new Error('offline'));
    const save = vi.spyOn(commands, 'firewallRuleSave');
    const { wrapper } = await open('/firewall/activity', 'firewall-on');
    const row = wrapper.findAll('[data-event]').find(item => item.text().includes('dc.services.visualstudio.com'))!;
    await row.get('button').trigger('click');
    await settle();
    expect(save).toHaveBeenCalledWith(
      expect.objectContaining({
        action: 'allow',
        remote: 'dc.services.visualstudio.com',
        port: 443
      })
    );
  });

  it('Keeps at most 1,000 live rows, and merges none while the tab is closed', async () => {
    const { wrapper, router } = await open('/firewall/activity', 'firewall-on');
    const store = useFirewallStore();
    const sample = store.activity[0]!;
    const live = (id: string, offset = 0) => ({
      ...sample,
      id,
      at: sample.at + 1000 + offset
    });
    store.applyActivity(Array.from({ length: ACTIVITY_LIMIT + 200 }, (_, index) => live(`live-${index}`, index)));
    await settle();
    expect(store.activity).toHaveLength(ACTIVITY_LIMIT);
    expect(store.activity[0]!.id).toBe(`live-${ACTIVITY_LIMIT + 199}`);
    expect(store.activityMore).toBe(true);
    expect(wrapper.findAll('[data-event]')).toHaveLength(ACTIVITY_LIMIT);

    await router.push('/firewall');
    await settle();
    store.applyActivity([live('while-away', 5000)]);
    expect(store.activity.some(event => event.id === 'while-away')).toBe(false);
  }, 15_000); // Rendering 1,000 real rows needs headroom on shared CI runners.

  it('Reads an open tab again once the extension answers after a failed load', async () => {
    const { wrapper } = await open('/firewall/activity', 'firewall-disconnected');
    expect(wrapper.get('[role="alert"]').text()).toBe('防火墙组件没有响应，请稍后重试');
    expect(wrapper.find('[data-event]').exists()).toBe(false);
    const blocklists = vi.spyOn(commands, 'firewallBlocklists');
    firewallMock()!.reconnect();
    await settle();
    expect(wrapper.find('[role="alert"]').exists()).toBe(false);
    expect(wrapper.findAll('[data-event]').length).toBeGreaterThan(0);
    // Only what is on screen is read again.
    expect(blocklists).not.toHaveBeenCalled();
    // Live rows arrive again now that the list is there.
    const store = useFirewallStore();
    store.applyActivity([{ ...store.activity[0]!, id: 'after-reconnect', at: Date.now() }]);
    await settle();
    expect(wrapper.find('[data-event="after-reconnect"]').exists()).toBe(true);
  });
});

describe('Block lists', () => {
  it('Adds an https list, refuses other addresses, and removes a list after confirming', async () => {
    const { wrapper } = await open('/firewall/blocklists', 'firewall-on');
    expect(wrapper.text()).toContain('raw.githubusercontent.com');
    expect(wrapper.text()).toContain('84,213 条');
    const input = wrapper.get('input[aria-label="屏蔽列表的网址或文件"]');

    await input.setValue('http://lists.example.org/hosts');
    await wrapper.get('form').trigger('submit');
    await settle();
    expect(lastToast()?.description).toBe('只支持 https 网址或这台 Mac 上的文件');

    await input.setValue('https://lists.example.org/hosts');
    await wrapper.get('form').trigger('submit');
    await settle();
    expect(lastToast()?.title).toBe('已添加「lists.example.org」，共 12,500 条');
    expect(wrapper.findAll('[data-blocklist]')).toHaveLength(2);

    await wrapper.get('button[aria-label="移除「lists.example.org」"]').trigger('click');
    await settle();
    bodyButton('移除')!.click();
    await settle();
    expect(wrapper.findAll('[data-blocklist]')).toHaveLength(1);
  });

  it('Adds a list once when Return is pressed again while it is being added', async () => {
    const { wrapper } = await open('/firewall/blocklists', 'firewall-on');
    const real = commands.firewallBlocklistAdd;
    let release!: () => void;
    const downloaded = new Promise<void>(resolve => (release = resolve));
    const add = vi.spyOn(commands, 'firewallBlocklistAdd').mockImplementation(async source => {
      await downloaded;
      return real(source);
    });
    await wrapper.get('input[aria-label="屏蔽列表的网址或文件"]').setValue('https://lists.example.org/hosts');
    await wrapper.get('form').trigger('submit');
    await wrapper.get('form').trigger('submit');
    release();
    await settle();
    expect(add).toHaveBeenCalledOnce();
    expect(useToasts().items.value.map(toast => toast.title)).toEqual(['已添加「lists.example.org」，共 12,500 条']);
    expect(wrapper.findAll('[data-blocklist]')).toHaveLength(2);
  });

  it('Sends https addresses in any case and ~/ paths to Rust as typed', async () => {
    const add = vi.spyOn(commands, 'firewallBlocklistAdd');
    const { wrapper } = await open('/firewall/blocklists', 'firewall-on');
    const input = wrapper.get('input[aria-label="屏蔽列表的网址或文件"]');
    await input.setValue('HTTPS://Lists.Example.org/hosts');
    await wrapper.get('form').trigger('submit');
    await settle();
    expect(add).toHaveBeenLastCalledWith('HTTPS://Lists.Example.org/hosts');
    expect(lastToast()?.title).toBe('已添加「lists.example.org」，共 12,500 条');

    await input.setValue('  ~/Documents/blocklist.txt ');
    await wrapper.get('form').trigger('submit');
    await settle();
    expect(add).toHaveBeenLastCalledWith('~/Documents/blocklist.txt');
    expect(lastToast()?.title).toBe('已添加「blocklist.txt」，共 240 条');
    expect(wrapper.findAll('[data-blocklist]')).toHaveLength(3);
  });
});

describe('Settings', () => {
  it('Saves the automatic allows and removes the firewall after confirming', async () => {
    const save = vi.spyOn(commands, 'firewallSettingsSet');
    const uninstall = vi.spyOn(commands, 'firewallUninstall');
    const { wrapper } = await open('/firewall/settings', 'firewall-on');
    await wrapper.get('[role="switch"][aria-labelledby="firewall-allowApple"]').trigger('click');
    await settle();
    expect(save).toHaveBeenCalledWith({
      mode: 'ask',
      allowApple: false,
      allowInstalled: true
    });

    await button(wrapper, '移除')!.trigger('click');
    await settle();
    expect(document.body.textContent).toContain('之后所有 App 都能直接联网');
    dialogButton('移除')!.click();
    await settle(3);
    await settle();
    expect(uninstall).toHaveBeenCalledOnce();
    expect(wrapper.text()).toContain('每个 App 第一次连接网络时');
    // macOS deletes the stopped extension only at the next restart; that is not an installation in progress.
    expect(wrapper.get('[data-firewall-removed]').text()).toContain('应用防火墙已移除，不用马上重启');
    expect(wrapper.text()).not.toContain('正在开启应用防火墙');
    expect(button(wrapper, '开启应用防火墙')).toBeDefined();
  });

  it('Says the removal failed, not the installation, when the password prompt is dismissed', async () => {
    const { wrapper } = await open('/firewall/settings', 'firewall-on,firewall-removal-fails');
    await button(wrapper, '移除')!.trigger('click');
    await settle();
    dialogButton('移除')!.click();
    await settle(3);
    await settle();
    expect(wrapper.text()).toContain('应用防火墙已暂停');
    expect(wrapper.get('[role="alert"]').text()).toBe('没能移除应用防火墙，请重试');
    expect(wrapper.text()).not.toContain('没能开启');
    expect(button(wrapper, '移除')).toBeDefined();
  });
});

describe('Question window', () => {
  async function openAlerts(flags = 'firewall-alerts') {
    history.replaceState(null, '', `/?mock=${flags}`);
    const context = await setup('/firewall', { tickMs: TICK });
    await startFirewallAlertState();
    // The buttons wait a moment after each new question; tests step over it instead of sleeping.
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    const wrapper = mountIn(context, FirewallAlertPage);
    await settle();
    return wrapper;
  }
  /** Lets the buttons answer the question on screen. */
  async function armed() {
    await vi.advanceTimersByTimeAsync(ANSWER_DELAY_MS);
    await settle();
  }

  it('Answers the oldest connection for just its address until the app quits, then shows the next one', async () => {
    const reply = vi.spyOn(commands, 'firewallAlertReply');
    const wrapper = await openAlerts();
    expect(wrapper.find('h1').text()).toBe('「Firefox」想连接网络');
    expect(wrapper.text()).toContain('开发者：Mozilla Corporation');
    expect(wrapper.text()).toContain('incoming.telemetry.mozilla.org');
    expect(wrapper.text()).toContain('这个 App 还有 2 个连接在等这次回答');
    // Held TCP connections are let through after 2 minutes without an answer; the window counts down.
    expect(wrapper.get('[data-alert-deadline]').text()).toMatch(/^[12]:\d\d 内不回答会先放行这次连接$/);
    expect(wrapper.find('[data-alert-released]').exists()).toBe(false);
    // No default button: nothing has focus until the user clicks.
    expect(document.activeElement?.tagName).not.toBe('BUTTON');

    await radio(wrapper, '只限这个地址').trigger('click');
    await radio(wrapper, '直到 App 退出').trigger('click');
    expect(wrapper.text()).toContain('incoming.telemetry.mozilla.org:443');
    const first = useFirewallStore().alerts[0]!.id;
    await armed();
    await button(wrapper, '允许')!.trigger('click');
    await settle();
    expect(reply).toHaveBeenCalledWith({
      alertId: first,
      action: 'allow',
      scope: 'destination',
      until: 'app_quits'
    });

    expect(wrapper.find('h1').text()).toBe('「python3」想连接网络');
    expect(wrapper.text()).toContain('这个程序只有本机签名，无法确认是谁开发的。');
    expect(wrapper.text()).toContain('python3 ← zsh ← iTerm2');
    expect(wrapper.text()).toContain('sync.py --upload');
    // Each connection starts again from the whole app, forever.
    expect(radio(wrapper, '这个 App 的所有连接').attributes('aria-checked')).toBe('true');
    expect(radio(wrapper, '永久').attributes('aria-checked')).toBe('true');

    await armed();
    await button(wrapper, '阻止')!.trigger('click');
    await settle();
    expect(reply).toHaveBeenLastCalledWith(
      expect.objectContaining({
        action: 'block',
        scope: 'app',
        until: 'always'
      })
    );
    expect(wrapper.text()).toContain('没有等待确认的连接');
  });

  it('Keeps a question whose connections were already let through, and still turns the answer into a rule', async () => {
    const reply = vi.spyOn(commands, 'firewallAlertReply');
    const wrapper = await openAlerts('firewall-alert-released');
    expect(wrapper.find('h1').text()).toBe('「Firefox」想连接网络');
    expect(wrapper.get('[data-alert-released]').text()).toBe('这次连接已先放行，你的回答仍会记成规则。');
    expect(wrapper.find('[data-alert-deadline]').exists()).toBe(false);
    const first = useFirewallStore().alerts[0]!.id;
    await armed();
    await button(wrapper, '阻止')!.trigger('click');
    await settle();
    expect(reply).toHaveBeenCalledWith({
      alertId: first,
      action: 'block',
      scope: 'app',
      until: 'always'
    });
    // The next question still has its connections on hold.
    expect(wrapper.find('h1').text()).toBe('「python3」想连接网络');
    expect(wrapper.find('[data-alert-deadline]').exists()).toBe(true);
  });

  it('Treats a connection already answered elsewhere as done', async () => {
    const wrapper = await openAlerts();
    vi.spyOn(commands, 'firewallAlertReply').mockResolvedValueOnce({
      status: 'error',
      error: {
        code: 'E_FW_UNKNOWN_ALERT',
        message: 'alert_resolved',
        detail: null
      }
    });
    const load = vi.spyOn(commands, 'firewallAlerts');
    await armed();
    await button(wrapper, '允许')!.trigger('click');
    await settle();
    expect(load).toHaveBeenCalled();
    expect(wrapper.find('[role="alert"]').exists()).toBe(false);
  });

  it('Ignores clicks as the window shows up, and the second click of a double click on the next question', async () => {
    const reply = vi.spyOn(commands, 'firewallAlertReply');
    const wrapper = await openAlerts();
    // The window can pop up under the cursor: a click right away does nothing.
    await button(wrapper, '允许')!.trigger('click');
    await settle();
    expect(reply).not.toHaveBeenCalled();

    await armed();
    // Rust sends the shorter list before the reply returns, so the second click lands on the next app's question.
    await button(wrapper, '允许')!.trigger('click');
    await button(wrapper, '允许')!.trigger('click');
    await settle();
    expect(reply).toHaveBeenCalledOnce();
    expect(wrapper.find('h1').text()).toBe('「python3」想连接网络');
    await vi.advanceTimersByTimeAsync(ANSWER_DELAY_MS - 50);
    await button(wrapper, '阻止')!.trigger('click');
    await settle();
    expect(reply).toHaveBeenCalledOnce();

    // Shown again (say, after it was hidden): the buttons wait once more.
    await armed();
    expect(document.visibilityState).toBe('visible');
    document.dispatchEvent(new Event('visibilitychange'));
    await button(wrapper, '阻止')!.trigger('click');
    await settle();
    expect(reply).toHaveBeenCalledOnce();
    await armed();
    await button(wrapper, '阻止')!.trigger('click');
    await settle();
    expect(reply).toHaveBeenCalledTimes(2);
    expect(reply).toHaveBeenLastCalledWith(
      expect.objectContaining({
        action: 'block',
        scope: 'app',
        until: 'always'
      })
    );
  });

  it('Answers nothing else while an answer is on its way, and spins the button that was clicked', async () => {
    const real = commands.firewallAlertReply;
    let release!: () => void;
    const delivered = new Promise<void>(resolve => (release = resolve));
    // Slow to come back, after Rust already sent the shorter list.
    const reply = vi.spyOn(commands, 'firewallAlertReply').mockImplementation(async answer => {
      const result = await real(answer);
      await delivered;
      return result;
    });
    const wrapper = await openAlerts();
    await armed();
    await button(wrapper, '允许')!.trigger('click');
    await settle();
    expect(wrapper.find('h1').text()).toBe('「python3」想连接网络');
    // The spinner belongs to the answered question, not to the next one's buttons.
    expect(wrapper.find('button[aria-busy="true"]').exists()).toBe(false);
    await armed();
    await button(wrapper, '阻止')!.trigger('click');
    await settle();
    expect(reply).toHaveBeenCalledOnce();
    release();
    await settle();
    await button(wrapper, '阻止')!.trigger('click');
    await settle();
    expect(reply).toHaveBeenCalledTimes(2);
  });

  it('Spins Allow, not Block, while an allow is on its way', async () => {
    let release!: () => void;
    const delivered = new Promise<void>(resolve => (release = resolve));
    const real = commands.firewallAlertReply;
    vi.spyOn(commands, 'firewallAlertReply').mockImplementation(async answer => {
      await delivered;
      return real(answer);
    });
    const wrapper = await openAlerts();
    await armed();
    await button(wrapper, '允许')!.trigger('click');
    await settle();
    expect(button(wrapper, '允许')!.attributes('aria-busy')).toBe('true');
    expect(button(wrapper, '阻止')!.attributes('aria-busy')).toBeUndefined();
    expect(button(wrapper, '阻止')!.attributes('disabled')).toBeDefined();
    release();
    await settle();
    expect(wrapper.find('h1').text()).toBe('「python3」想连接网络');
    expect(wrapper.find('button[aria-busy="true"]').exists()).toBe(false);
  });

  it('Says nothing is waiting for an answer whose connections were already let through', async () => {
    const wrapper = await openAlerts('firewall-alert-released');
    expect(wrapper.find('[data-alert-released]').exists()).toBe(true);
    expect(wrapper.text()).not.toContain('在等这次回答');
    expect(wrapper.text()).toContain('还有 1 个连接等待确认');
    // Even from a host that still counts them.
    const store = useFirewallStore();
    const [released, next] = store.alerts;
    store.applyAlerts([{ ...released!, waiting: 2 }, next!]);
    await settle();
    expect(wrapper.text()).not.toContain('在等这次回答');
    expect(wrapper.text()).toContain('还有 1 个连接等待确认');
  });
});

describe('Persisted settings on restart', () => {
  it('Loads disabled automatic allowances before a mode change', async () => {
    let finish!: (value: Awaited<ReturnType<typeof commands.firewallSettings>>) => void;
    const pending = new Promise<Awaited<ReturnType<typeof commands.firewallSettings>>>(resolve => {
      finish = resolve;
    });
    vi.spyOn(commands, 'firewallSettings').mockReturnValue(pending);
    const save = vi.spyOn(commands, 'firewallSettingsSet');
    const { wrapper } = await open('/firewall', 'firewall-on');
    expect(wrapper.find('[aria-labelledby="firewall-status"] [role="radiogroup"]').exists()).toBe(false);
    expect(save).not.toHaveBeenCalled();
    finish({
      status: 'ok',
      data: { mode: 'ask', allowApple: false, allowInstalled: false }
    });
    await settle();
    await radio(wrapper, '阻止并记录').trigger('click');
    await settle();
    expect(save).toHaveBeenCalledWith({
      mode: 'block',
      allowApple: false,
      allowInstalled: false
    });
  });

  it('Does not expose settings controls when persisted settings cannot be read', async () => {
    vi.spyOn(commands, 'firewallSettings').mockRejectedValue(new Error('offline'));
    const save = vi.spyOn(commands, 'firewallSettingsSet');
    const { wrapper } = await open('/firewall', 'firewall-on');
    expect(wrapper.find('[aria-labelledby="firewall-status"] [role="radiogroup"]').exists()).toBe(false);
    expect(save).not.toHaveBeenCalled();
  });
});

describe('Settings loading and offline recovery', () => {
  it('Disables automatic allowances until the persisted settings arrive', async () => {
    let finish!: (value: Awaited<ReturnType<typeof commands.firewallSettings>>) => void;
    const pending = new Promise<Awaited<ReturnType<typeof commands.firewallSettings>>>(resolve => {
      finish = resolve;
    });
    vi.spyOn(commands, 'firewallSettings').mockReturnValue(pending);
    const save = vi.spyOn(commands, 'firewallSettingsSet');
    const { wrapper } = await open('/firewall/settings', 'firewall-on');
    const apple = wrapper.get('[role="switch"][aria-labelledby="firewall-allowApple"]');
    const installed = wrapper.get('[role="switch"][aria-labelledby="firewall-allowInstalled"]');
    expect(apple.attributes('disabled')).toBeDefined();
    expect(installed.attributes('disabled')).toBeDefined();
    await apple.trigger('click');
    expect(save).not.toHaveBeenCalled();
    finish({
      status: 'ok',
      data: { mode: 'block', allowApple: true, allowInstalled: false }
    });
    await settle();
    expect(apple.attributes('disabled')).toBeUndefined();
    await apple.trigger('click');
    await settle();
    expect(save).toHaveBeenCalledWith({
      mode: 'block',
      allowApple: false,
      allowInstalled: false
    });
  });

  it('Keeps restart guidance, disabling and uninstall available when settings fail', async () => {
    vi.spyOn(commands, 'firewallSettings').mockRejectedValue(new Error('offline'));
    const disable = vi.spyOn(commands, 'firewallDisable');
    const uninstall = vi.spyOn(commands, 'firewallUninstall');
    const save = vi.spyOn(commands, 'firewallSettingsSet');
    const { wrapper } = await open('/firewall/settings', 'firewall-service-missing');
    // Freeze the mock's delayed filter-off event until the pending state has been checked.
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    expect(wrapper.find('[data-firewall-service-missing]').exists()).toBe(true);
    expect(wrapper.get('[role="switch"][aria-labelledby="firewall-allowApple"]').attributes('disabled')).toBeDefined();
    const toggle = wrapper.get('[aria-labelledby="firewall-status"] [role="switch"]');
    expect(toggle.attributes('disabled')).toBeUndefined();
    await toggle.trigger('click');
    await settle();
    expect(disable).toHaveBeenCalledOnce();
    expect(save).not.toHaveBeenCalled();
    // Removing waits until macOS has turned filtering off.
    expect(button(wrapper, '移除')!.attributes('disabled')).toBeDefined();
    await vi.advanceTimersByTimeAsync(TICK - 1);
    expect(button(wrapper, '移除')!.attributes('disabled')).toBeDefined();
    await vi.advanceTimersByTimeAsync(1);
    await settle();
    expect(button(wrapper, '移除')!.attributes('disabled')).toBeUndefined();
    await button(wrapper, '移除')!.trigger('click');
    await settle();
    dialogButton('移除')!.click();
    await settle();
    expect(uninstall).toHaveBeenCalledOnce();
  });
});

describe('Serialized settings writes', () => {
  it.each([false, true])('Preserves newer edits when the first save fails: %s', async fail => {
    await open('/firewall/settings', 'firewall-on');
    const store = useFirewallStore();
    let finish!: (value: Awaited<ReturnType<typeof commands.firewallSettingsSet>>) => void;
    const first = new Promise<Awaited<ReturnType<typeof commands.firewallSettingsSet>>>(resolve => {
      finish = resolve;
    });
    const save = vi
      .spyOn(commands, 'firewallSettingsSet')
      .mockReturnValueOnce(first)
      .mockImplementation(async settings => ({
        status: 'ok',
        data: settings
      }));
    const initial = { ...store.settings! };
    const one = store.saveSettings({ ...initial, allowApple: false }).catch(() => undefined);
    const two = store.saveSettings({
      ...store.settings!,
      allowInstalled: false
    });
    await settle();
    expect(save).toHaveBeenCalledTimes(1);
    // Change events received during a write cannot read back an intermediate state.
    store.applyChanged('settings');
    finish(
      fail
        ? {
            status: 'error',
            error: {
              code: 'E_FW_DISCONNECTED',
              message: 'offline',
              detail: null
            }
          }
        : { status: 'ok', data: { ...initial, allowApple: false } }
    );
    await Promise.all([one, two]);
    expect(save).toHaveBeenCalledTimes(2);
    expect(store.settings).toEqual({
      ...initial,
      allowApple: false,
      allowInstalled: false
    });
    await store.saveSettings({ ...store.settings!, mode: 'block' });
    expect(save).toHaveBeenLastCalledWith({
      mode: 'block',
      allowApple: false,
      allowInstalled: false
    });
  });

  it('Rolls back to the last confirmed state when both queued writes fail', async () => {
    await open('/firewall', 'firewall-on');
    const store = useFirewallStore();
    const initial = { ...store.settings! };
    vi.spyOn(commands, 'firewallSettingsSet').mockRejectedValue(new Error('offline'));
    const one = store.saveSettings({ ...initial, allowApple: false });
    const two = store.saveSettings({
      ...store.settings!,
      allowInstalled: false
    });
    await Promise.allSettled([one, two]);
    expect(store.settings).toEqual(initial);
  });

  it('Ignores a read started before newer saves', async () => {
    await open('/firewall', 'firewall-on');
    const store = useFirewallStore();
    const initial = { ...store.settings! };
    let finish!: (value: Awaited<ReturnType<typeof commands.firewallSettings>>) => void;
    vi.spyOn(commands, 'firewallSettings').mockReturnValueOnce(
      new Promise(resolve => {
        finish = resolve;
      })
    );
    const read = store.loadSettings();
    await store.saveSettings({
      ...initial,
      allowApple: false,
      allowInstalled: false
    });
    finish({ status: 'ok', data: initial });
    await read;
    expect(store.settings?.allowApple).toBe(false);
    expect(store.settings?.allowInstalled).toBe(false);
  });

  it('Reactivates an old extension without supplying replacement settings', async () => {
    vi.spyOn(commands, 'firewallSettings').mockRejectedValue(new Error('old service'));
    const enable = vi.spyOn(commands, 'firewallEnable');
    const { wrapper } = await open('/firewall/settings', 'firewall-service-missing');
    const toggle = wrapper.get('[aria-labelledby="firewall-status"] [role="switch"]');
    await toggle.trigger('click');
    // The switch is back once the status shows filtering off.
    await settle(1);
    expect(toggle.attributes('disabled')).toBeUndefined();
    await toggle.trigger('click');
    await settle();
    expect(enable).toHaveBeenCalledWith(null);
  });
});
