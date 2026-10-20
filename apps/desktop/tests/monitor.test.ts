import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils';
import type { Pinia } from 'pinia';
import type { Router } from 'vue-router';
import { defineComponent } from 'vue';
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest';
import MenuBarWidget from '@/components/monitor/MenuBarWidget.vue';
import FanControl from '@/components/monitor/FanControl.vue';
import MonitorNotifier from '@/components/monitor/MonitorNotifier.vue';
import { useMonitorNames } from '@/components/monitor/meta';
import { useHelperApproval } from '@/components/monitor/useHelperApproval';
import MonitorSettings from '@/components/settings/MonitorSettings.vue';
import MonitorProcesses from '@/components/monitor/MonitorProcesses.vue';
import MonitorPage from '@/pages/MonitorPage.vue';
import TrayMonitorPage from '@/pages/TrayMonitorPage.vue';
import { i18n } from '@/i18n';
import type { FanState, MonitorSample, SensorReading } from '@/ipc/bindings';
import { commands, events } from '@/ipc/client';
import { startLocalState, useSettingsStore } from '@/stores';
import { HISTORY, MODULES, useMonitorStore } from '@/stores/monitor';
import { useToasts } from '@/composables/useToasts';
import { mockMatchMedia, setup, stub } from './helpers';

// Computer monitoring (monitor proposal §11–§16): cards and details, health, processes, the menu bar and fan control.
const find = (root: ParentNode, selector: string, label: string) =>
  [...root.querySelectorAll<HTMLElement>(selector)].find(node => node.textContent?.trim() === label);
async function click(node: HTMLElement | undefined) {
  expect(node).toBeDefined();
  node!.click();
  await flushPromises();
}
const fanControl = () => document.querySelector<HTMLElement>('[data-testid="fan-control"]');
/** Tests mount no toast region; read the queue. */
const toasts = () => useToasts().items.value;
const toastTitles = () => toasts().map(toast => toast.title);

async function context(path = '/monitor', mock = '') {
  window.history.replaceState(null, '', mock ? `/?mock=${mock}` : '/');
  const result = await setup(path, { brew: true, tickMs: 5 });
  await startLocalState();
  return result;
}
const mountWith = (component: object, { pinia, router }: { pinia: Pinia; router: Router }) =>
  mount(component, { global: { plugins: [pinia, router, i18n] }, attachTo: document.body });
/** The page subscribes on mount; wait for the first live sample. */
async function page(ctx: { pinia: Pinia; router: Router }) {
  const wrapper = mountWith(MonitorPage, ctx);
  // Processes come only with live samples for a visible outlet, not with the seeded history.
  await vi.waitFor(() => expect(useMonitorStore().sample?.processes?.length).toBeGreaterThan(0));
  await flushPromises();
  return wrapper;
}

enableAutoUnmount(afterEach);
beforeAll(() => {
  mockMatchMedia(() => true);
});
afterEach(() => {
  document.body.innerHTML = '';
  useToasts().clear();
  vi.restoreAllMocks();
  localStorage.removeItem(VIEW_KEY);
});
const VIEW_KEY = 'opennavo.monitor.processView';

describe('Monitor page', () => {
  it('Shows a card per module this Mac has; a card opens its detail with its own menu bar switch', async () => {
    const ctx = await context('/monitor', 'desktop');
    const wrapper = await page(ctx);
    const store = useMonitorStore();
    // A desktop Mac has no battery: no card, and no menu bar item to offer.
    expect(store.modules).toEqual(MODULES.filter(module => module !== 'battery'));
    expect(wrapper.findAll('[data-testid="monitor-cards"] article')).toHaveLength(6);
    // Fans have no card of their own: as in Stats, the sensors card shows them with the temperature.
    expect(wrapper.find('[data-module="fans"]').exists()).toBe(false);
    expect(wrapper.findAll('[data-module="sensors"] [data-testid="monitor-fan"]')).toHaveLength(2);
    expect(store.views.page).not.toContain('fans');
    expect(wrapper.find('[data-testid="monitor-health"]').text()).toContain('CPU、内存和磁盘都在正常范围');
    expect(wrapper.text()).toContain('菜单栏 · 2 项');

    await click(document.querySelector<HTMLElement>('[aria-label="查看GPU详情"]')!);
    await vi.waitFor(() => expect(ctx.router.currentRoute.value.query.detail).toBe('gpu'));
    await flushPromises();
    const detail = document.querySelector<HTMLElement>('[data-testid="monitor-detail"]');
    expect(detail).not.toBeNull();
    expect(store.views.detail).toEqual(['gpu']);

    // Every module is its own menu bar item; turning one on adds it without touching the others.
    await click(detail!.querySelector<HTMLElement>('[role="switch"]')!);
    const settings = useSettingsStore();
    await vi.waitFor(() => expect(settings.value?.monitorMenuBar.gpu.enabled).toBe(true));
    expect(settings.value?.monitorMenuBar.cpu.enabled).toBe(true);
    expect(toastTitles()).toContain('已把「GPU」放到菜单栏');
    expect(wrapper.text()).toContain('菜单栏 · 3 项');

    await ctx.router.replace({ query: {} });
    await flushPromises();
    expect(store.views.detail).toBeUndefined();
  });

  it('Health names what needs attention; an app can be quit only after confirmation', async () => {
    const ctx = await context('/monitor', 'cpu-busy');
    const wrapper = await page(ctx);
    const health = wrapper.find('[data-testid="monitor-health"]');
    expect(health.text()).toContain('1 项需要注意');
    expect(health.text()).toContain('CPU 已经连续 3 分钟超过 85%');
    expect(health.text()).toContain('查看进程');

    const row = [...document.querySelectorAll<HTMLElement>('[data-testid="monitor-processes"] li')].find(item =>
      item.textContent?.includes('Xcode')
    );
    // System processes and background tools have no quit button.
    const kernel = [...document.querySelectorAll<HTMLElement>('[data-testid="monitor-processes"] li')].find(item =>
      item.textContent?.includes('kernel_task')
    );
    expect(kernel && find(kernel, 'button', '退出')).toBeUndefined();
    await click(find(row!, 'button', '退出'));
    const dialog = document.querySelector<HTMLElement>('[role="alertdialog"]');
    expect(dialog?.textContent).toContain('退出 Xcode？');
    await click(find(dialog!, 'button', '退出'));
    await vi.waitFor(() => expect(toastTitles()).toContain('已让 Xcode 退出'));
    await vi.waitFor(() =>
      expect(useMonitorStore().sample?.processes?.some(entry => entry.name === 'Xcode')).toBe(false)
    );
  });

  it('Stops sampling for the page once it is closed', async () => {
    const ctx = await context();
    const wrapper = await page(ctx);
    const store = useMonitorStore();
    const unsubscribe = vi.spyOn(commands, 'monitorUnsubscribe');
    wrapper.unmount();
    await flushPromises();
    expect(unsubscribe).toHaveBeenCalledWith('page');
    expect(store.views.page).toBeUndefined();
  });
});

describe('Process list', () => {
  const rows = (root: ParentNode) => [
    ...root.querySelectorAll<HTMLElement>('[data-testid="monitor-processes"] > ul > li')
  ];
  const rowOf = (root: ParentNode, name: string) => rows(root).find(item => item.textContent?.includes(name));
  /** A fixed sample in an unsubscribed list: the mock stops ticking once its page is gone. */
  async function fixed(mutate: (sample: MonitorSample) => MonitorSample, props: Record<string, unknown> = {}) {
    const ctx = await context('/monitor', 'cpu-busy');
    const wrapper = await page(ctx);
    const store = useMonitorStore();
    const copy = JSON.parse(JSON.stringify(store.sample)) as MonitorSample;
    wrapper.unmount();
    await flushPromises();
    store.history = [mutate(copy)];
    return mount(MonitorProcesses, {
      props,
      global: { plugins: [ctx.pinia, ctx.router, i18n] },
      attachTo: document.body
    });
  }

  it('Says what a share is in cores and how to read the scale, so a busy Mac does not look idle', async () => {
    const ctx = await context('/monitor', 'cpu-busy');
    await page(ctx);
    const list = document.querySelector<HTMLElement>('[data-testid="monitor-processes"]')!;
    // The mock Mac has 14 logical cores: one core is 7% of it.
    expect(list.textContent).toContain('占整机比例 · 1 核 ≈ 7%');
    const xcode = rowOf(list, 'Xcode')!;
    expect(xcode.textContent).toMatch(/\d+%\s*≈\d+(\.\d)?\s*核/);
    expect(xcode.querySelector('span[title]')?.getAttribute('title')).toContain('活动监视器口径');
  });

  it('Accounts for the rest of a busy CPU under the list: other processes, finished short-lived ones, the system', async () => {
    const ctx = await context('/monitor', 'cpu-busy');
    await page(ctx);
    const breakdown = document.querySelector<HTMLElement>('[data-testid="monitor-cpu-breakdown"]')!;
    expect(breakdown.textContent).toMatch(/其余进程（\d+）/);
    expect(breakdown.textContent).toContain('已结束的短命进程');
    expect(breakdown.textContent).toContain('主要来自 node和zsh');
    expect(breakdown.textContent).toContain('系统与 root 进程');
    expect(breakdown.querySelector('[role="img"]')?.getAttribute('aria-label')).toBe('整机 CPU 的去向');
    expect(breakdown.querySelector('li[title]')?.getAttribute('title')).toBeTruthy();
  });

  it('Leaves the breakdown out when there is nothing to account for or the Mac is quiet', async () => {
    const none = await fixed(sample => ({ ...sample, cpuBreakdown: null }));
    expect(none.find('[data-testid="monitor-cpu-breakdown"]').exists()).toBe(false);
    none.unmount();
    const quiet = await fixed(sample => ({
      ...sample,
      cpuBreakdown: { ...sample.cpuBreakdown!, usage: 0.06 }
    }));
    expect(quiet.find('[data-testid="monitor-cpu-breakdown"]').exists()).toBe(false);
    // Only the CPU list explains itself this way.
    quiet.unmount();
    const memory = await fixed(sample => sample, { sort: 'memory' });
    expect(memory.find('[data-testid="monitor-cpu-breakdown"]').exists()).toBe(false);
  });

  it('Names only the parts big enough to read, and keeps the bar within the machine when they overshoot it', async () => {
    // Three rows list about 37% of the machine between them; 60% is readable, so 23% is the rest.
    const plain = await fixed(
      sample => ({
        ...sample,
        cpuBreakdown: { usage: 0.7, readable: 0.6, activeCount: 12, finished: 0.001, finishedFrom: [], other: 0 }
      }),
      { limit: 3 }
    );
    const text = plain.find('[data-testid="monitor-cpu-breakdown"]').text();
    expect(text).toMatch(/其余进程（\d+）/);
    expect(text).not.toContain('已结束的短命进程');
    expect(text).not.toContain('系统与 root 进程');
    plain.unmount();
    const over = await fixed(
      sample => ({
        ...sample,
        cpuBreakdown: { usage: 0.9, readable: 0.9, activeCount: 12, finished: 0.3, finishedFrom: [], other: 0 }
      }),
      { limit: 3 }
    );
    const widths = [...over.findAll('[data-testid="monitor-cpu-breakdown"] [role="img"] i')].map(node =>
      Number.parseFloat((node.element as HTMLElement).style.width)
    );
    expect(widths.reduce((sum, width) => sum + width, 0)).toBeCloseTo(100, 3);
  });

  it('Lists OpenNavo itself, marked, and never offers to quit it', async () => {
    const wrapper = await fixed(sample => sample, { limit: 20 });
    const self = rowOf(wrapper.element, 'OpenNavo')!;
    expect(self.textContent).toContain('本应用');
    expect(find(self, 'button', '退出')).toBeUndefined();
  });

  it('Merges an app’s helpers on request, in every list, and remembers the choice', async () => {
    const ctx = await context('/monitor', 'cpu-busy');
    await page(ctx);
    const list = () => document.querySelector<HTMLElement>('[data-testid="monitor-processes"]')!;
    expect(rowOf(list(), 'Google Chrome Helper (Renderer)')).toBeDefined();
    await click(find(list(), '[role="radio"]', '应用'));
    await vi.waitFor(() => expect(rowOf(list(), 'Google Chrome Helper (Renderer)')).toBeUndefined());
    const chrome = rowOf(list(), 'Google Chrome')!;
    expect(chrome.textContent).toContain('×3');
    // The main process leads the merged entry, so the app can still be quit.
    expect(find(chrome, 'button', '退出')).toBeDefined();
    expect(localStorage.getItem(VIEW_KEY)).toBe('app');

    // Another window of the app changes the choice back: this one follows.
    localStorage.setItem(VIEW_KEY, 'process');
    window.dispatchEvent(new StorageEvent('storage', { key: VIEW_KEY }));
    await vi.waitFor(() => expect(rowOf(list(), 'Google Chrome Helper (Renderer)')).toBeDefined());
    expect(rowOf(list(), 'Google Chrome')?.textContent).not.toContain('×3');
  });

  it('Shows three rows and the accounting in the menu bar popover, in the saved view', async () => {
    localStorage.setItem(VIEW_KEY, 'app');
    const ctx = await context('/tray-monitor?module=cpu', 'cpu-busy');
    const wrapper = mountWith(TrayMonitorPage, ctx);
    await vi.waitFor(() => expect(useMonitorStore().sample?.processes?.length).toBeGreaterThan(0));
    await flushPromises();
    const popover = wrapper.find('[data-testid="monitor-popover"]').element as HTMLElement;
    expect(rows(popover)).toHaveLength(3);
    expect(popover.querySelector('[data-testid="monitor-process-view"] [aria-checked="true"]')?.textContent).toContain(
      '应用'
    );
    expect(popover.querySelector('[data-testid="monitor-cpu-breakdown"]')?.textContent).toContain('已结束的短命进程');
    // Merged: Chrome's helpers are one entry among them, not three.
    expect(popover.textContent).not.toContain('Helper');
  });
});

describe('Monitor store', () => {
  it.each(['resolve', 'reject'] as const)(
    'A delayed settings request that %ss cannot release a newer page subscription',
    async completion => {
      await context();
      const store = useMonitorStore();
      const original = commands.monitorSubscribe;
      let release!: () => void;
      const gate = new Promise<void>(resolve => {
        release = resolve;
      });
      vi.spyOn(commands, 'monitorSubscribe').mockImplementationOnce(async (...args) => {
        const result = await original(...args);
        await gate;
        if (completion === 'reject') throw new Error('delayed settings failure');
        return result;
      });
      const unsubscribe = vi.spyOn(commands, 'monitorUnsubscribe');
      const pending = store.loadAvailable().catch(error => error);
      await flushPromises();
      await store.subscribe('page', ['cpu', 'memory']);
      // A late settings snapshot must not replace the new page's data either.
      store.available = ['cpu', 'memory'];
      release();
      const result = await pending;
      if (completion === 'reject') expect(result).toBeInstanceOf(Error);
      expect(store.views.page).toEqual(['cpu', 'memory']);
      expect(store.available).toEqual(['cpu', 'memory']);
      expect(unsubscribe).not.toHaveBeenCalled();
      await store.unsubscribe('page');
      expect(unsubscribe).toHaveBeenCalledOnce();
    }
  );

  it('Releases the settings-only subscription after loading availability', async () => {
    await context();
    const store = useMonitorStore();
    const unsubscribe = vi.spyOn(commands, 'monitorUnsubscribe');
    await store.loadAvailable();
    expect(store.available.length).toBeGreaterThan(0);
    expect(store.views.page).toBeUndefined();
    expect(unsubscribe).toHaveBeenCalledWith('page');
  });

  it('Releases a failed settings-only subscription so availability can be retried', async () => {
    await context();
    const store = useMonitorStore();
    vi.spyOn(commands, 'monitorSubscribe').mockRejectedValueOnce(new Error('unavailable'));
    await expect(store.loadAvailable()).rejects.toThrow('unavailable');
    expect(store.views.page).toBeUndefined();
    await store.loadAvailable();
    expect(store.available.length).toBeGreaterThan(0);
  });

  it('Keeps the last minute of samples and reuses the shared history', async () => {
    await context();
    const store = useMonitorStore();
    await store.subscribe('popover', ['cpu']);
    expect(store.history).toHaveLength(HISTORY);
    const last = store.history.at(-1)!;
    for (let index = 1; index <= 5; index += 1) store.applySample({ ...last, at: last.at + index } as MonitorSample);
    expect(store.history).toHaveLength(HISTORY);
    expect(store.sample?.at).toBe(last.at + 5);
    expect(store.series(sample => sample.cpu.usage)).toHaveLength(HISTORY);
    // Another outlet subscribing later does not shorten what this window already holds.
    await store.subscribe('detail', ['memory']);
    expect(store.history).toHaveLength(HISTORY);
    await store.unsubscribe('popover');
    await store.unsubscribe('detail');
    expect(store.views).toEqual({});
  });
});

describe('Fan control', () => {
  it('Ignores repeated mode clicks while the hardware command is pending and releases busy on failure', async () => {
    const ctx = await context('/monitor', 'fan-helper-ready');
    const store = useMonitorStore();
    await store.subscribe('detail', ['sensors']);
    const wrapper = mountWith(FanControl, ctx);
    await vi.waitFor(() => expect(store.helper?.status).toBe('enabled'));
    let reject!: (error: Error) => void;
    const command = vi.spyOn(commands, 'fanSetMode').mockImplementationOnce(
      () =>
        new Promise((_, fail) => {
          reject = fail;
        })
    );
    const manual = find(wrapper.element, '[role="radio"]', '手动')!;
    manual.click();
    manual.click();
    await flushPromises();
    expect(command).toHaveBeenCalledTimes(1);
    expect(wrapper.attributes('aria-busy')).toBe('true');
    expect(wrapper.find('[role="radiogroup"]').attributes('inert')).toBeDefined();
    expect(manual.getAttribute('aria-checked')).toBe('false');
    reject(new Error('unavailable'));
    await flushPromises();
    expect(wrapper.attributes('aria-busy')).toBe('false');
    expect(wrapper.find('[role="radiogroup"]').attributes('inert')).toBeUndefined();
    expect(toasts().at(-1)?.tone).toBe('danger');
    await click(manual);
    expect(command).toHaveBeenCalledTimes(2);
    await vi.waitFor(() => expect(store.fans?.every(fan => fan.mode === 'manual')).toBe(true));
  });

  it('Shares pending helper probes and ignores a stale not-installed result after installation', async () => {
    await context();
    const store = useMonitorStore();
    let resolve!: (value: Awaited<ReturnType<typeof commands.fanHelperStatus>>) => void;
    const status = vi.spyOn(commands, 'fanHelperStatus').mockImplementationOnce(
      () =>
        new Promise(done => {
          resolve = done;
        })
    );
    const first = store.loadHelper();
    const second = store.loadHelper();
    expect(status).toHaveBeenCalledTimes(1);
    await store.installHelper();
    expect(store.helper?.status).toBe('requires_approval');
    resolve({ status: 'ok', data: { status: 'not_installed', support: 'unverified', manualAllowed: true } });
    await Promise.all([first, second]);
    expect(store.helper?.status).toBe('requires_approval');
    await store.loadHelper();
    expect(status).toHaveBeenCalledTimes(2);
  });

  it('Waits for installation during polling and preserves a queued uninstall from another view', async () => {
    await context();
    const store = useMonitorStore();
    let resolve!: (value: Awaited<ReturnType<typeof commands.fanHelperInstall>>) => void;
    vi.spyOn(commands, 'fanHelperInstall').mockImplementationOnce(
      () =>
        new Promise(done => {
          resolve = done;
        })
    );
    const status = vi.spyOn(commands, 'fanHelperStatus');
    const uninstall = vi.spyOn(commands, 'fanHelperUninstall');
    const installation = store.installHelper();
    const probe = store.loadHelper();
    const removal = store.uninstallHelper();
    expect(status).not.toHaveBeenCalled();
    expect(uninstall).not.toHaveBeenCalled();
    resolve({ status: 'ok', data: { status: 'requires_approval', support: 'unverified', manualAllowed: true } });
    await Promise.all([installation, probe, removal]);
    expect(uninstall).toHaveBeenCalledTimes(1);
    expect(store.helper?.status).toBe('not_installed');
  });

  it.each(['not_installed', 'error'] as const)('Automatically recovers a transient %s after an update', async state => {
    const ctx = await context('/monitor', 'fan-helper-ready');
    const store = useMonitorStore();
    store.helper = { status: state, support: 'unverified', manualAllowed: true };
    const status = vi.spyOn(commands, 'fanHelperStatus');
    vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible');
    const Probe = defineComponent({
      setup() {
        useHelperApproval(10);
        return () => null;
      }
    });
    mountWith(Probe, ctx);
    await vi.waitFor(() => expect(store.helper?.status).toBe('enabled'));
    const calls = status.mock.calls.length;
    await new Promise(resolve => setTimeout(resolve, 40));
    expect(status.mock.calls.length).toBe(calls);
  });

  it('Shows loading instead of not installed before the first helper status arrives', async () => {
    const ctx = await context('/settings/monitor', 'fan-helper-ready');
    const store = useMonitorStore();
    await store.loadAvailable();
    let resolve!: (value: Awaited<ReturnType<typeof commands.fanHelperStatus>>) => void;
    vi.spyOn(commands, 'fanHelperStatus').mockImplementationOnce(
      () =>
        new Promise(done => {
          resolve = done;
        })
    );
    const wrapper = mountWith(MonitorSettings, ctx);
    await flushPromises();
    expect(wrapper.text()).toContain('正在加载');
    expect(wrapper.text()).not.toContain('未安装');
    resolve({ status: 'ok', data: { status: 'enabled', support: 'unverified', manualAllowed: true } });
    await flushPromises();
    expect(store.helper?.status).toBe('enabled');
  });

  it('Offers the helper right away, waits for approval, then switches to manual; Automatic hands fans back', async () => {
    const ctx = await context('/monitor?detail=sensors');
    await page(ctx);
    const store = useMonitorStore();
    await vi.waitFor(() => expect(store.helper?.status).toBe('not_installed'));
    // Every known chip is unverified, which no longer hides the helper: the offer is there without a click.
    expect(store.helper).toMatchObject({ support: 'unverified', manualAllowed: true });
    expect(fanControl()?.textContent).toContain('手动控制风扇要先装一个辅助程序');
    // The switch appears once the helper answers.
    expect(fanControl()?.querySelector('[role="radiogroup"]')).toBeNull();
    // Fans sit in the sensors detail, before the temperatures; both share one range, so it is listed once.
    const detail = document.querySelector<HTMLElement>('[data-testid="monitor-detail"]')!;
    const fansSection = detail.querySelector<HTMLElement>('[data-testid="monitor-fans"]')!;
    expect([...fansSection.querySelectorAll('dt')].map(node => node.textContent)).toEqual(['转速范围']);
    const headings = [...detail.querySelectorAll('h3')].map(node => node.textContent?.trim());
    expect(headings.indexOf('风扇')).toBeGreaterThan(-1);
    expect(headings.indexOf('风扇')).toBeLessThan(headings.indexOf('温度'));

    await click(find(fanControl()!, 'button', '安装辅助程序'));
    expect(fanControl()?.textContent).toContain('还差一步');
    expect(store.fans?.every(fan => fan.mode === 'auto')).toBe(true);
    // macOS asks for the approval in Login Items & Extensions; the button opens that pane.
    const approval = vi.spyOn(commands, 'fanHelperOpenApproval');
    await click(find(fanControl()!, 'button', '打开系统设置'));
    expect(approval).toHaveBeenCalledTimes(1);
    await click(find(fanControl()!, 'button', '我已允许'));
    await vi.waitFor(() => expect(store.helper?.status).toBe('enabled'));
    expect(toastTitles()).toContain('风扇辅助程序已就绪');
    // Ready is not manual: the user still chooses.
    expect(store.fans?.every(fan => fan.mode === 'auto')).toBe(true);
    await click(find(fanControl()!, '[role="radio"]', '手动'));
    await vi.waitFor(() => expect(store.fans?.every(fan => fan.mode === 'manual')).toBe(true));
    expect(toastTitles()).toContain('风扇已切到手动');

    // Synced by default: one slider for all fans; unsynced, each fan gets its own named slider.
    const sliders = () => [...fanControl()!.querySelectorAll<HTMLInputElement>('input[type="range"]')];
    expect(sliders().map(slider => slider.getAttribute('aria-label'))).toEqual(['所有风扇']);
    const all = sliders()[0]!;
    all.value = '3000';
    all.dispatchEvent(new Event('input'));
    all.dispatchEvent(new Event('change'));
    await vi.waitFor(() => expect(store.fans?.map(fan => fan.targetRpm)).toEqual([3000, 3000]));
    await vi.waitFor(() => expect(useSettingsStore().value?.fanLastManual).toEqual({ 0: 3000, 1: 3000 }));
    await click(fanControl()!.querySelector<HTMLElement>('[aria-label="同步所有风扇"]')!);
    expect(sliders().map(slider => slider.getAttribute('aria-label'))).toEqual(['左侧风扇', '右侧风扇']);

    await click(find(fanControl()!, 'button', '全速'));
    await vi.waitFor(() => expect(store.fans?.map(fan => fan.targetRpm)).toEqual([5800, 5800]));
    expect(toastTitles()).toContain('风扇已拉到全速');

    await click(find(fanControl()!, 'button', '恢复自动'));
    await vi.waitFor(() => expect(store.fans?.every(fan => fan.mode === 'auto')).toBe(true));
    expect(toastTitles()).toContain('风扇已交还系统控制');
    // Only hand-backs caused by something else get a notice, not the user's own switch.
    expect(toastTitles()).not.toContain('风扇已回到自动');
  });

  it('Restores the last manual speed after a relaunch', async () => {
    const ctx = await context('/monitor?detail=sensors', 'fan-helper-ready');
    await useSettingsStore().update('fanLastManual', { 0: 2400, 1: 2600 });
    await page(ctx);
    const store = useMonitorStore();
    await vi.waitFor(() => expect(store.helper?.status).toBe('enabled'));
    expect(store.fans?.every(fan => fan.mode === 'auto')).toBe(true);
    await click(find(fanControl()!, 'button', '恢复上次的手动转速'));
    await vi.waitFor(() => expect(store.fans?.map(fan => fan.targetRpm)).toEqual([2400, 2600]));
  });

  it('Allows manual control while the Mac is hot, so the fans can be turned up', async () => {
    const ctx = await context('/monitor?detail=sensors', 'fan-helper-ready,hot');
    await page(ctx);
    const store = useMonitorStore();
    await vi.waitFor(() => expect(store.helper?.status).toBe('enabled'));
    await click(find(fanControl()!, '[role="radio"]', '手动'));
    await vi.waitFor(() => expect(store.fans?.every(fan => fan.mode === 'manual')).toBe(true));
    expect(toasts().some(toast => toast.tone === 'danger')).toBe(false);
    await click(find(fanControl()!, 'button', '全速'));
    await vi.waitFor(() => expect(store.fans?.map(fan => fan.targetRpm)).toEqual([5800, 5800]));
  });

  it('Unknown chips only show fan speeds and never offer the helper', async () => {
    const ctx = await context('/monitor?detail=sensors', 'fan-unsupported');
    await page(ctx);
    await vi.waitFor(() => expect(fanControl()?.textContent).toContain('这台 Mac 的芯片暂不支持风扇控制'));
    expect(fanControl()?.querySelector('[role="radiogroup"]')).toBeNull();
    // Nothing can be switched, so there is no hand-back promise to make either.
    expect(fanControl()?.textContent).not.toContain('没有「关闭风扇」');
    const detail = document.querySelector<HTMLElement>('[data-testid="monitor-detail"]')!;
    expect(detail.querySelectorAll('[data-testid="monitor-fan"]')).toHaveLength(2);

    const settings = mountWith(MonitorSettings, ctx);
    await vi.waitFor(() => expect(settings.text()).toContain('这台 Mac 的芯片暂不支持风扇控制'));
    expect(settings.findAll('button').some(button => button.text() === '安装')).toBe(false);
  });

  it('A development build says why it cannot install the helper', async () => {
    const ctx = await context('/monitor?detail=sensors', 'fan-unsigned');
    await page(ctx);
    const store = useMonitorStore();
    await vi.waitFor(() => expect(store.helper?.status).toBe('not_installed'));
    await click(find(fanControl()!, 'button', '安装辅助程序'));
    await vi.waitFor(() =>
      expect(toasts().at(-1)).toMatchObject({
        tone: 'danger',
        description: '这份 OpenNavo 没有正式签名（比如开发版），装不了风扇辅助程序。请用正式安装包'
      })
    );
    expect(store.helper?.status).toBe('not_installed');
  });

  it('A helper that stopped answering is offered again as a reinstall, here and in Settings', async () => {
    const ctx = await context('/monitor?detail=sensors', 'fan-helper-broken');
    await page(ctx);
    const store = useMonitorStore();
    await vi.waitFor(() => expect(fanControl()?.textContent).toContain('风扇辅助程序没有响应'));
    const settings = mountWith(MonitorSettings, ctx);
    await vi.waitFor(() => expect(settings.findAll('button').some(button => button.text() === '重新安装')).toBe(true));
    const uninstall = vi.spyOn(commands, 'fanHelperUninstall');
    await click(find(fanControl()!, 'button', '重新安装辅助程序'));
    expect(uninstall).toHaveBeenCalledTimes(1);
    await vi.waitFor(() => expect(store.helper?.status).toBe('requires_approval'));
    expect(fanControl()?.textContent).toContain('还差一步');
  });

  it('Notices the approval from System Settings without another click, only while visible', async () => {
    const ctx = await context('/monitor', 'fan-helper-broken');
    const store = useMonitorStore();
    await store.reinstallHelper();
    expect(store.helper?.status).toBe('requires_approval');
    const status = vi.spyOn(commands, 'fanHelperStatus');
    const visibility = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden');
    const Probe = defineComponent({
      setup() {
        useHelperApproval(10);
        return () => null;
      }
    });
    mountWith(Probe, ctx);
    await new Promise(resolve => setTimeout(resolve, 60));
    expect(status).not.toHaveBeenCalled();
    visibility.mockReturnValue('visible');
    await vi.waitFor(() => expect(store.helper?.status).toBe('enabled'));
    const calls = status.mock.calls.length;
    await new Promise(resolve => setTimeout(resolve, 60));
    // Once allowed, it stops asking.
    expect(status.mock.calls.length).toBe(calls);
  });

  it('Announces fans that Rust changed on its own, not the user’s own changes', async () => {
    const ctx = await context();
    mountWith(MonitorNotifier, ctx);
    const store = useMonitorStore();
    const fans: FanState[] = [
      { id: 0, name: 'Left', rpm: 5800, minRpm: 1200, maxRpm: 5800, targetRpm: 5800, mode: 'manual' }
    ];
    store.applyFanChange({ fans, reason: 'user' });
    await flushPromises();
    expect(toasts()).toHaveLength(0);
    // Too hot for long: manual fans went to full speed and stayed manual.
    store.applyFanChange({ fans, reason: 'overheat' });
    await flushPromises();
    expect(toasts()).toMatchObject([
      { tone: 'warning', title: '风扇已拉到全速', description: '温度持续 10 秒达到 95 °C' }
    ]);
    // The pushed state wins until the next sample arrives.
    expect(store.fans).toEqual(fans);
    const auto: FanState[] = [{ ...fans[0]!, targetRpm: null, mode: 'auto' }];
    store.applyFanChange({ fans: auto, reason: 'heartbeat_lost' });
    await flushPromises();
    expect(toasts().at(-1)).toMatchObject({ tone: 'info', title: '风扇已回到自动', description: 'OpenNavo 没有响应' });
  });
});

describe('Names', () => {
  it('Localizes fan positions and known sensor zones; unknown names pass through', async () => {
    await context();
    let names!: ReturnType<typeof useMonitorNames>;
    mount(
      defineComponent({
        setup() {
          names = useMonitorNames();
          return () => null;
        }
      }),
      { global: { plugins: [i18n] } }
    );
    const fan = (id: number, name: string): FanState => ({
      id,
      name,
      rpm: 1300,
      minRpm: 1200,
      maxRpm: 5800,
      targetRpm: null,
      mode: 'auto'
    });
    expect(names.fan(fan(0, 'Left'), 1)).toBe('风扇');
    expect(names.fan(fan(0, 'Left side'), 2)).toBe('左侧风扇');
    expect(names.fan(fan(1, 'Right'), 2)).toBe('右侧风扇');
    expect(names.fan(fan(2, 'Exhaust'), 3)).toBe('风扇 3');
    const sensor = (name: string): SensorReading => ({ key: 'T', name, group: 'cpu', celsius: 50 });
    expect(names.sensor(sensor('CPU performance cores'))).toBe('CPU 性能核心');
    expect(names.sensor(sensor('battery'))).toBe('电池');
    expect(names.sensor(sensor('Airflow left'))).toBe('Airflow left');
  });
});

describe('Menu bar', () => {
  it('Introduces the menu bar once, after onboarding, only for items Rust created', async () => {
    const ctx = await context();
    const settings = useSettingsStore();
    await settings.update('onboardingCompleted', true);
    mountWith(MonitorNotifier, ctx);
    await vi.waitFor(() => expect(toastTitles()).toEqual(['电脑监控已经放到菜单栏']));
    await vi.waitFor(() => expect(settings.value?.monitorMenuBarIntro).toBe(true));
    expect(toasts()[0]?.actionLabel).toBe('查看');
    useToasts().runAction(toasts()[0]!.id);
    await vi.waitFor(() => expect(ctx.router.currentRoute.value.path).toBe('/monitor'));

    mountWith(MonitorNotifier, ctx);
    await flushPromises();
    expect(toasts()).toHaveLength(0);
  });

  it('The shared popover shows the clicked item and samples only while visible', async () => {
    const ctx = await context('/discover');
    ctx.router.addRoute({
      path: '/tray-monitor',
      name: 'tray-monitor',
      component: stub('popover'),
      meta: { bare: true }
    });
    await ctx.router.push('/tray-monitor?module=memory');
    const store = useMonitorStore();
    const wrapper = mountWith(TrayMonitorPage, ctx);
    await vi.waitFor(() => expect(store.views.popover).toEqual(['memory']));
    await flushPromises();
    expect(wrapper.find('[data-testid="monitor-popover"]').text()).toContain('内存');

    // Rust reuses one window for every item and says which one was clicked.
    await events.monitorPopover.emit('cpu');
    await vi.waitFor(() => expect(store.views.popover).toEqual(['cpu']));
    const open = vi.spyOn(commands, 'monitorOpenInMain');
    await click(find(document.body, 'button', '在 OpenNavo 中查看'));
    expect(open).toHaveBeenCalledWith('detail', 'cpu');
    await click(document.querySelector<HTMLElement>('button[aria-label="菜单栏设置"]')!);
    expect(open).toHaveBeenCalledWith('settings', null);

    const visibility = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden');
    document.dispatchEvent(new Event('visibilitychange'));
    await flushPromises();
    expect(store.views.popover).toBeUndefined();
    visibility.mockReturnValue('visible');
    document.dispatchEvent(new Event('visibilitychange'));
    await vi.waitFor(() => expect(store.views.popover).toEqual(['cpu']));

    // Like Stats, the sensors item opens temperatures and fans together, so Rust samples both.
    const subscribe = vi.spyOn(commands, 'monitorSubscribe');
    await events.monitorPopover.emit('sensors');
    await vi.waitFor(() => expect(store.views.popover).toEqual(['sensors']));
    expect(subscribe).toHaveBeenLastCalledWith('popover', ['sensors', 'fans']);
    await flushPromises();
    const popover = wrapper.find('[data-testid="monitor-popover"]');
    expect(popover.text()).toContain('传感器');
    expect(popover.findAll('[data-testid="monitor-fan"]')).toHaveLength(2);
    expect(popover.find('[data-testid="fan-control"]').exists()).toBe(true);

    // The helper installs right from the menu bar too. The popover has no toast region, so results show in place.
    await vi.waitFor(() => expect(store.helper?.status).toBe('not_installed'));
    await click(find(fanControl()!, 'button', '安装辅助程序'));
    expect(fanControl()?.textContent).toContain('还差一步');
    await click(find(fanControl()!, 'button', '我已允许'));
    await vi.waitFor(() =>
      expect(fanControl()?.querySelector('[data-testid="fan-notice"]')?.textContent?.trim()).toBe('风扇辅助程序已就绪')
    );
    await click(find(fanControl()!, '[role="radio"]', '手动'));
    await vi.waitFor(() => expect(store.fans?.every(fan => fan.mode === 'manual')).toBe(true));
    expect(fanControl()?.querySelector('[data-testid="fan-notice"]')?.textContent?.trim()).toBe('风扇已切到手动');
    expect(toasts()).toHaveLength(0);
  });

  it('Offers to keep OpenNavo in the menu bar when closing the window would quit it', async () => {
    const ctx = await context('/settings/monitor');
    const settings = useSettingsStore();
    await settings.update('keepInMenuBarOnClose', false);
    const store = useMonitorStore();
    await store.setMenuBar('gpu', { enabled: true });
    await store.setMenuBar('disk', { enabled: true });
    // One prompt, however many items were turned on.
    expect(toasts()).toMatchObject([{ title: '关闭窗口时菜单栏项会一起消失', actionLabel: '一起打开' }]);
    useToasts().runAction(toasts()[0]!.id);
    await vi.waitFor(() => expect(settings.value?.keepInMenuBarOnClose).toBe(true));
    await store.setMenuBar('network', { enabled: true });
    expect(toasts()).toHaveLength(0);
    expect(ctx.router.currentRoute.value.path).toBe('/settings/monitor');
  });

  it('Settings turn on any number of items and change the refresh rate', async () => {
    const ctx = await context('/settings/monitor');
    const wrapper = mountWith(MonitorSettings, ctx);
    const store = useMonitorStore();
    await vi.waitFor(() => expect(store.available.length).toBeGreaterThan(0));
    await flushPromises();
    for (const toggle of wrapper.findAll('[role="switch"][aria-checked="false"]')) {
      await toggle.trigger('click');
      await flushPromises();
    }
    await vi.waitFor(() => expect(store.menuBarCount).toBe(MODULES.length));
    await click(find(document.body, '[role="radio"]', '1 秒'));
    await vi.waitFor(() => expect(useSettingsStore().value?.monitorIntervalMs).toBe(1000));
  });
});

describe('M3 additions', () => {
  /** Items of the menu opened last (menus are teleported to the body). */
  const menuItems = () =>
    [
      ...([...document.querySelectorAll<HTMLElement>('[role="menu"]')].at(-1)?.querySelectorAll('[role="menuitem"]') ??
        [])
    ].map(node => node.textContent?.trim());

  it('Offers chart styles only where the value moves, and the preview draws them', async () => {
    const ctx = await context('/settings/monitor');
    const wrapper = mountWith(MonitorSettings, ctx);
    const store = useMonitorStore();
    await vi.waitFor(() => expect(store.available.length).toBeGreaterThan(0));
    await flushPromises();
    const picker = (module: string) =>
      wrapper.find(`[data-module="${module}"] [data-testid="menubar-style"]`).element as HTMLElement;
    await click(picker('disk'));
    expect(menuItems()).toEqual(['两行文字', '只显示数值']);
    await click(picker('disk'));
    expect(document.querySelectorAll('[role="menu"]')).toHaveLength(0);
    // A temperature reads on its own, as in Stats: no two-line style, and no separate fans item.
    expect(wrapper.find('[data-module="fans"]').exists()).toBe(false);
    expect(wrapper.find('[data-module="sensors"] [data-testid="menubar-style"]').text()).toBe('只显示数值');
    await click(picker('sensors'));
    expect(menuItems()).toEqual(['只显示数值', '迷你折线', '柱状']);
    await click(picker('sensors'));
    await click(picker('cpu'));
    expect(menuItems()).toEqual(['两行文字', '只显示数值', '迷你折线', '柱状']);
    await click(find(document.body, '[role="menuitem"]', '迷你折线'));
    await vi.waitFor(() => expect(useSettingsStore().value?.monitorMenuBar.cpu.style).toBe('line'));

    await store.subscribe('popover', ['cpu']);
    const line = mount(MenuBarWidget, {
      props: { module: 'cpu' },
      global: { plugins: [ctx.pinia, ctx.router, i18n] }
    });
    expect(line.find('[data-style="line"] path').attributes('d')).toMatch(/^M/);
    const bars = mount(MenuBarWidget, {
      props: { module: 'cpu', styleKind: 'bars' },
      global: { plugins: [ctx.pinia, ctx.router, i18n] }
    });
    // One bar per core, performance cores first.
    expect(bars.findAll('[data-style="bars"] rect')).toHaveLength(14);
    // Disk space and battery have no chart: a chart style saved for them is drawn as two lines.
    const disk = mount(MenuBarWidget, {
      props: { module: 'disk', styleKind: 'line' },
      global: { plugins: [ctx.pinia, ctx.router, i18n] }
    });
    expect(disk.find('[data-style]').attributes('data-style')).toBe('text');
    // The label is Rust's English one, as in the menu bar; only the accessible name is translated.
    expect(disk.text()).toContain('DISK');
    expect(disk.text()).not.toContain('磁盘');
    expect(disk.attributes('aria-label')).toMatch(/^磁盘 \d+G$/);
  });

  it('Switches a detail to the last hour from the 5-second trend, with gaps where nothing was sampled', async () => {
    const ctx = await context('/monitor?detail=cpu');
    await page(ctx);
    const store = useMonitorStore();
    const trend = vi.spyOn(commands, 'monitorTrend');
    const detail = () => document.querySelector<HTMLElement>('[data-testid="monitor-detail"]')!;
    expect(detail().textContent).toContain('60 秒前');
    await click(find(detail(), '[role="radio"]', '最近 1 小时'));
    expect(trend).toHaveBeenCalledTimes(1);
    await vi.waitFor(() => expect(detail().textContent).toContain('1 小时前'));
    const slots = store.trendSeries(point => point.cpu);
    expect(slots).toHaveLength(720);
    // Ten minutes without samples stay empty rather than being joined up.
    expect(slots.filter(value => value == null).length).toBeGreaterThanOrEqual(100);
    // Power is read while the CPU is on screen.
    const rows = [...detail().querySelectorAll('dt')].map(node => node.textContent);
    expect(rows).toContain('功耗');
  });

  it('Lists disk-heavy processes and every drive’s SMART status in the disk detail', async () => {
    const ctx = await context('/monitor?detail=disk');
    await page(ctx);
    const detail = () => document.querySelector<HTMLElement>('[data-testid="monitor-detail"]')!;
    await vi.waitFor(() =>
      expect(useMonitorStore().sample?.processes?.some(entry => entry.diskReadBytesPerSec != null)).toBe(true)
    );
    await flushPromises();
    const list = detail().querySelector<HTMLElement>('[data-testid="monitor-processes"]')!;
    expect(list.textContent).toContain('磁盘读写最多的进程');
    expect(list.querySelector('li')?.textContent).toContain('mds_stores');
    const drives = detail().querySelector<HTMLElement>('[data-testid="monitor-disk-devices"]')!;
    expect(drives.textContent).toContain('APPLE SSD AP0512Z');
    expect(drives.textContent).toContain('已验证');
    expect(drives.textContent).toContain('磨损');
    expect(drives.textContent).toContain('Samsung PSSD T7 Shield');
    expect(drives.textContent).toContain('不支持');
  });

  it('Orders the network detail’s processes by traffic', async () => {
    const ctx = await context('/monitor?detail=network');
    await page(ctx);
    await vi.waitFor(() =>
      expect(useMonitorStore().sample?.processes?.some(entry => entry.netDownBytesPerSec != null)).toBe(true)
    );
    await flushPromises();
    const list = document.querySelector<HTMLElement>(
      '[data-testid="monitor-detail"] [data-testid="monitor-processes"]'
    )!;
    expect(list.textContent).toContain('网络流量最多的进程');
    expect(list.querySelector('li')?.textContent).toContain('Dropbox');
  });

  it('Makes a failing drive a critical issue that opens the disk detail', async () => {
    const ctx = await context('/monitor', 'disk-failing');
    const wrapper = await page(ctx);
    const health = wrapper.find('[data-testid="monitor-health"]');
    expect(health.text()).toContain('「APPLE SSD AP0512Z」的 SMART 状态异常，请尽快备份');
    await click(find(health.element, 'button', '查看磁盘详情'));
    await vi.waitFor(() => expect(ctx.router.currentRoute.value.query.detail).toBe('disk'));
  });

  it('Turns on alerts from settings', async () => {
    const ctx = await context('/settings/monitor');
    const wrapper = mountWith(MonitorSettings, ctx);
    await vi.waitFor(() => expect(wrapper.find('[aria-labelledby="monitor-setting-alerts"]').exists()).toBe(true));
    await wrapper.find('[aria-labelledby="monitor-setting-alerts"]').trigger('click');
    await vi.waitFor(() => expect(useSettingsStore().value?.monitorAlerts).toBe(true));
  });
});
