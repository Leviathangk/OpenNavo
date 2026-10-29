import { flushPromises, mount } from '@vue/test-utils';
import type { DOMWrapper, VueWrapper } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ref } from 'vue';
import { createOnUi } from '@opennavo/ui';
import { i18n } from '@/i18n';
import type { OutdatedItem, Task } from '@/ipc/bindings';
import { commands } from '@/ipc/client';
import { disposeMockIpc } from '@/ipc/mock';
import RunningAppsConfirm from '@/components/shell/RunningAppsConfirm.vue';
import MajorConfirm from '@/components/updates/MajorConfirm.vue';
import OutdatedList from '@/components/updates/OutdatedList.vue';
import AppStorePage from '@/pages/AppStorePage.vue';
import UpdatesPage from '@/pages/UpdatesPage.vue';
import UpdatesRail from '@/components/rails/UpdatesRail.vue';
import { useToasts } from '@/composables/useToasts';
import { startLocalState, useSettingsStore, useTasksStore, useUpdatesStore } from '@/stores';
import { updateRequest } from '@/stores/updates';
import { setup } from './helpers';
import type { TestContext } from './helpers';

// Multi-source updates (store/security plan §7, §11.4): App Store and Sparkle rows beside Homebrew's, each queued
// through its own source, with skip, beta channel, relaunch and inline notes. ?mock=update-sources adds the rows.
const wrappers: VueWrapper[] = [];
let activeContext: TestContext | undefined;
beforeEach(() => {
  vi.stubGlobal(
    'IntersectionObserver',
    class {
      observe() {}
      disconnect() {}
    }
  );
});
afterEach(() => {
  wrappers.splice(0).forEach(wrapper => wrapper.unmount());
  disposeMockIpc();
  activeContext = undefined;
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  useToasts().clear();
  history.replaceState(null, '', '/');
  document.body.innerHTML = '';
});

const ui = createOnUi({ locale: ref('zh-CN' as const) });

describe('Update source switches use serialized settings writes', () => {
  it.each([false, true])('Keeps both source choices with a pending IPC (first fails: %s)', async fails => {
    const context = await setup('/updates');
    activeContext = context;
    const settings = useSettingsStore();
    await settings.load();
    const wrapper = mount(UpdatesRail, {
      global: { plugins: [context.pinia, context.router, i18n, ui] }
    });
    wrappers.push(wrapper);
    await flushPromises();
    const realSave = commands.settingsSet;
    let finish!: () => void;
    const save = vi.spyOn(commands, 'settingsSet').mockImplementationOnce(async next => {
      await new Promise<void>(resolve => {
        finish = resolve;
      });
      return fails
        ? {
            status: 'error',
            error: {
              code: 'E_UNKNOWN',
              message: 'source_save_failed',
              detail: null
            }
          }
        : realSave(next);
    });
    const homebrew = wrapper.get('[role="switch"][aria-labelledby="source-homebrew"]');
    const mas = wrapper.get('[role="switch"][aria-labelledby="source-mas"]');
    await homebrew.trigger('click');
    await mas.trigger('click');
    await flushPromises();
    expect(save).toHaveBeenCalledTimes(1);
    finish();
    await flushPromises();
    expect(save).toHaveBeenCalledTimes(2);
    expect(homebrew.attributes('aria-checked')).toBe(fails ? 'true' : 'false');
    expect(mas.attributes('aria-checked')).toBe('false');
    expect(settings.value!.updateSources).toEqual(fails ? { mas: false } : { homebrew: false, mas: false });
    expect(await commands.settingsGet()).toEqual({
      status: 'ok',
      data: settings.value
    });
    if (fails) expect(useToasts().items.value.at(-1)?.tone).toBe('danger');
  });
});

async function mountUpdates(flags = 'update-sources') {
  history.replaceState(null, '', `/?mock=${flags}`);
  const context = await setup('/updates', { tickMs: 5 });
  activeContext = context;
  const { pinia, router } = context;
  await startLocalState();
  const wrapper = mount(UpdatesPage, {
    global: { plugins: [pinia, router, i18n, ui] },
    attachTo: document.body
  });
  // The major-update and running-app questions live in the app shell; mount them beside the page.
  const confirm = mount(MajorConfirm, {
    global: { plugins: [pinia, router, i18n, ui] },
    attachTo: document.body
  });
  const running = mount(RunningAppsConfirm, {
    global: { plugins: [pinia, router, i18n, ui] },
    attachTo: document.body
  });
  wrappers.push(wrapper, confirm, running);
  await vi.waitFor(() => expect(useUpdatesStore().loaded).toBe(true));
  await flushPromises();
  return wrapper;
}

// Rows of the available-updates list only; the history below can name the same apps.
const rowOf = (wrapper: VueWrapper, name: string) =>
  wrapper
    .findComponent(OutdatedList)
    .findAll('li')
    .find(item => item.text().includes(name));

const button = (row: DOMWrapper<Element> | undefined, text: string) =>
  row?.findAll('button').find(node => node.text() === text);

async function chooseMenu(row: DOMWrapper<Element> | undefined, label: string) {
  await row?.get('button[aria-haspopup="menu"]').trigger('click');
  await flushPromises();
  const item = [...document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')].find(node =>
    node.textContent?.includes(label)
  );
  expect(item).toBeTruthy();
  item!.click();
  await flushPromises();
}

describe('Updates from every source', () => {
  it('Explains a missing App Store component without blaming the account or creating a task', async () => {
    const wrapper = await mountUpdates();
    const previousHistory = activeContext!.state.history.length;
    vi.spyOn(commands, 'taskEnqueue').mockResolvedValueOnce({
      status: 'error',
      error: { code: 'E_MAS_HELPER_MISSING', message: 'store_helper_missing', detail: null }
    });
    await button(rowOf(wrapper, 'Keynote'), '更新')!.trigger('click');
    await flushPromises();
    const toast = useToasts().items.value.at(-1);
    expect(toast?.description).toContain('当前应用缺少 App Store 下载组件');
    expect(toast?.description).toContain('重新安装完整的 OpenNavo 应用');
    expect(toast?.description).not.toContain('登录');
    expect(useUpdatesStore().masSignedOut).toBe(false);
    expect(activeContext!.state.history).toHaveLength(previousHistory);
    expect(button(rowOf(wrapper, 'Keynote'), '更新')).toBeTruthy();
  });

  it('Treats a legacy account preflight failure as a past attempt and allows retry', async () => {
    const wrapper = await mountUpdates();
    const failed: Task = {
      ...activeContext!.state.history[0]!,
      id: 'legacy-account-preflight',
      op: 'upgrade',
      target: { kind: 'mas', token: '409183694' },
      targetName: 'Keynote',
      options: { source: 'mas' },
      state: 'failed',
      error: { code: 'E_MAS_NOT_SIGNED_IN', message: 'Sign in to the App Store', detail: null },
      finishedAt: Date.now()
    };
    activeContext!.state.history.unshift(failed);
    useTasksStore().applyUpdate(failed);
    await flushPromises();
    expect(wrapper.get('[role="alert"]').text()).toContain('上次 App Store 更新未能确认账户状态');
    expect(wrapper.text()).toContain('无法确认 App Store 账户状态');
    expect(wrapper.text()).not.toContain('App Store 没有登录');
    expect(wrapper.text()).not.toContain('请先在 App Store 里登录');
    const enqueue = vi.spyOn(commands, 'taskEnqueue');
    await button(rowOf(wrapper, 'Keynote'), '更新')!.trigger('click');
    await flushPromises();
    expect(enqueue).toHaveBeenCalledWith(
      'upgrade',
      { kind: 'mas', token: '409183694' },
      expect.objectContaining({ source: 'mas' }),
      'manual'
    );
    await vi.waitFor(() => expect(useUpdatesStore().masSignedOut).toBe(false));
    expect(wrapper.find('[role="alert"]').exists()).toBe(false);
  });

  it('Labels each row with its source and what to expect before updating', async () => {
    const wrapper = await mountUpdates();
    expect(rowOf(wrapper, 'Keynote')?.text()).toContain('App Store');
    const iterm = rowOf(wrapper, 'iTerm2')?.text();
    expect(iterm).toContain('App 自带更新源');
    expect(iterm).toContain('正在运行，更新前会先请你退出');
    const sequel = rowOf(wrapper, 'Sequel Ace')?.text();
    for (const text of ['重要更新', '测试版', '大版本', '更新时需要管理员密码']) expect(sequel).toContain(text);
    expect(rowOf(wrapper, 'Rectangle')?.text()).toContain('已更新到 0.90，重新打开后生效');
    expect(button(rowOf(wrapper, 'Hand Mirror'), '查看')).toBeTruthy();
    // Server rules (M4): where the file comes from, and whether the expected developer is the installed one.
    const wechat = rowOf(wrapper, 'WeChat')?.text();
    expect(wechat).toContain('来自 dldir1.qq.com');
    expect(wechat).toContain('预期 Team ID 一致');
    const keka = rowOf(wrapper, 'Keka');
    expect(keka?.text()).toContain('签名不一致');
    expect(keka?.findAll('button').some(node => node.text() === '更新')).toBe(false);
  });

  async function updateAll(wrapper: VueWrapper, answer: 'confirm' | 'skip') {
    const enqueue = vi.spyOn(commands, 'taskEnqueue');
    await wrapper
      .findAll('button')
      .find(node => node.text() === '全部更新')!
      .trigger('click');
    await flushPromises();
    // Sequel Ace is a major update: asked about once, before anything is queued.
    expect(document.body.textContent).toContain('Sequel Ace 是大版本更新');
    expect(enqueue).not.toHaveBeenCalled();
    document.querySelector<HTMLButtonElement>(`[data-major-${answer}]`)!.click();
    await flushPromises();
    await flushPromises();
    // iTerm2 is running. Rust only inspects Homebrew apps, so its row's own flag asks to quit it first.
    const running = document.body.querySelector('[role="alertdialog"]');
    expect(running?.textContent).toContain('iTerm2');
    [...document.body.querySelectorAll<HTMLButtonElement>('[role="alertdialog"] button')]
      .find(node => node.textContent?.trim() === '退出这些应用并更新')!
      .click();
    await flushPromises();
    await flushPromises();
    const iterm = enqueue.mock.calls.find(([, target]) => target?.token === 'local-iterm2');
    expect(iterm?.[2]).toMatchObject({ source: 'sparkle', quitRunning: true });
    const calls = enqueue.mock.calls.map(([op, target, options]) => ({
      op,
      target,
      source: options.source ?? null
    }));
    await vi.waitFor(() => expect(activeContext?.state.active).toHaveLength(0), { timeout: 4000 });
    expect(activeContext?.state.history.every(task => ['succeeded', 'failed', 'canceled'].includes(task.state))).toBe(
      true
    );
    return calls;
  }

  it('Update All queues each row through its own source, smallest first, never a relaunch or a link-only row', async () => {
    const calls = await updateAll(await mountUpdates(), 'confirm');
    expect(calls).toEqual(
      expect.arrayContaining([
        {
          op: 'upgrade',
          target: { kind: 'mas', token: '409183694' },
          source: 'mas'
        },
        {
          op: 'upgrade',
          target: { kind: 'app', token: 'local-iterm2' },
          source: 'sparkle'
        },
        {
          op: 'upgrade',
          target: { kind: 'app', token: 'local-sequel-ace' },
          source: 'sparkle'
        }
      ])
    );
    const tokens = calls.map(call => call.target?.token);
    expect(calls).toContainEqual({
      op: 'upgrade',
      target: { kind: 'app', token: 'local-wechat' },
      source: 'vendor'
    });
    expect(tokens).not.toContain('local-rectangle');
    expect(tokens).not.toContain('local-hand-mirror');
    // Rust would refuse an update from a developer other than the installed app's.
    expect(tokens).not.toContain('local-keka');
    // Homebrew rows keep their package targets.
    expect(calls.filter(call => call.source === null).every(call => call.target?.kind === 'cask')).toBe(true);
    // The smallest downloads go first: iTerm2 (28 MB) before Sequel Ace (41 MB) before Keynote (671 MB).
    const order = ['local-iterm2', 'local-sequel-ace', '409183694'].map(token => tokens.indexOf(token));
    expect(order).toEqual([...order].sort((a, b) => a - b));
  });

  it('Declining the major update skips only it; the others still queue', async () => {
    const tokens = (await updateAll(await mountUpdates(), 'skip')).map(call => call.target?.token);
    expect(tokens).not.toContain('local-sequel-ace');
    expect(tokens).toContain('local-iterm2');
    expect(tokens).toContain('409183694');
  });

  it('Skips a version, switches to stable and reopens an updated app by their preference keys', async () => {
    const wrapper = await mountUpdates();
    const skip = vi.spyOn(commands, 'updateSkip');
    const channel = vi.spyOn(commands, 'updateSetChannel');
    const relaunch = vi.spyOn(commands, 'appRelaunch');
    await chooseMenu(rowOf(wrapper, 'iTerm2'), '跳过这个版本');
    expect(skip).toHaveBeenCalledWith('app:local-iterm2', '3.6.0');
    expect(rowOf(wrapper, 'iTerm2')).toBeUndefined();
    await chooseMenu(rowOf(wrapper, 'Sequel Ace'), '只接收正式版');
    expect(channel).toHaveBeenCalledWith('app:local-sequel-ace', 'stable');
    expect(rowOf(wrapper, 'Sequel Ace')?.text()).not.toContain('测试版');
    await button(rowOf(wrapper, 'Rectangle'), '重新打开')!.trigger('click');
    await flushPromises();
    expect(relaunch).toHaveBeenCalledWith('local-rectangle');
    expect(rowOf(wrapper, 'Rectangle')).toBeUndefined();
  });

  it('Homebrew rows skip a version through the same preference store, keyed by package', async () => {
    const wrapper = await mountUpdates();
    const skip = vi.spyOn(commands, 'updateSkip');
    const store = useUpdatesStore();
    const brew = store.items.find(item => item.source === 'homebrew')!;
    const row = wrapper
      .findComponent(OutdatedList)
      .findAll('li')
      .find(item => item.find(`a[href="/package/${brew.kind}/${brew.token}"]`).exists());
    await chooseMenu(row, '跳过这个版本');
    expect(skip).toHaveBeenCalledWith(`homebrew:${brew.kind}:${brew.token}`, brew.currentVersion);
    expect(store.items.some(item => item.source === 'homebrew' && item.token === brew.token)).toBe(false);
  });

  it('Stops checking an app through its preference key, whatever its source', async () => {
    const wrapper = await mountUpdates();
    const ignore = vi.spyOn(commands, 'updateIgnore');
    await chooseMenu(rowOf(wrapper, 'iTerm2'), '不再检查这个 App');
    expect(ignore).toHaveBeenCalledWith('app:local-iterm2', true);
    expect(rowOf(wrapper, 'iTerm2')).toBeDefined();
    expect(useUpdatesStore().actionable.some(item => item.name === 'iTerm2')).toBe(false);
    await chooseMenu(rowOf(wrapper, 'iTerm2'), '恢复检查这个 App');
    expect(ignore).toHaveBeenLastCalledWith('app:local-iterm2', false);
    expect(useUpdatesStore().items.find(item => item.name === 'iTerm2')?.ignored).toBe(false);
    expect(useUpdatesStore().actionable.some(item => item.name === 'iTerm2')).toBe(true);
  });

  it('Shows inline notes sanitized, without loading their images', async () => {
    const wrapper = await mountUpdates();
    await button(rowOf(wrapper, 'iTerm2'), '更新内容')!.trigger('click');
    await flushPromises();
    const sheet = document.querySelector('[role="dialog"]');
    expect(sheet?.textContent).toContain('iTerm2 3.6.0 更新内容');
    expect(sheet?.textContent).toContain('Faster tmux integration');
    expect(sheet?.querySelector('img, script')).toBeNull();
  });

  it('A check that could not read some sources names them and still lists the rest', async () => {
    const wrapper = await mountUpdates('update-sources,updates-partial');
    const note = wrapper.get('p[role="status"]').text();
    expect(note).toContain('部分检查');
    expect(note).toContain('GitHub Releases');
    expect(note).toContain('开发者网站');
    expect(rowOf(wrapper, 'Keynote')).toBeTruthy();
  });

  it('Names failed apps and distinguishes rate limits from invalid configuration', async () => {
    const wrapper = await mountUpdates('update-sources,updates-app-failures');
    expect(wrapper.get('p[role="status"]').text()).toContain('已取得的结果仍正常显示');
    const details = wrapper.get('details');
    expect(details.get('summary').text()).toBe('查看检查详情');
    expect(details.text()).toContain('Keka');
    expect(details.text()).toContain('请求过多');
    expect(details.text()).toContain('Open Design');
    expect(details.text()).toContain('需要应用开发者修正');
    expect(details.text()).not.toContain('https://');
    expect(rowOf(wrapper, 'Keynote')).toBeTruthy();
  });

  it('Shows a rules outage separately from app-owned update sources', async () => {
    const wrapper = await mountUpdates('update-sources,updates-rules-failed');
    const details = wrapper.get('details').text();
    expect(details).toContain('更新规则暂时无法刷新');
    expect(details).not.toContain('Electron');
    expect(details).not.toContain('App 自带更新源');
    expect(rowOf(wrapper, 'Keynote')).toBeTruthy();
  });

  it('Routes rows the way Rust expects: package, adamId, or local app with its source', () => {
    const base = {
      kind: 'cask',
      token: 'iterm2',
      appId: 'local-iterm2'
    } as OutdatedItem;
    expect(updateRequest({ ...base, source: 'homebrew' })).toEqual({
      target: { kind: 'cask', token: 'iterm2' },
      options: { greedy: true }
    });
    expect(updateRequest({ ...base, source: 'mas', token: '409183694' })).toEqual({
      target: { kind: 'mas', token: '409183694' },
      options: { source: 'mas' }
    });
    // A Sparkle row that matched a catalog cask still updates the local app, never through Homebrew.
    expect(updateRequest({ ...base, source: 'sparkle' })).toEqual({
      target: { kind: 'app', token: 'local-iterm2' },
      options: { source: 'sparkle' }
    });
  });
});

describe('App Store installs with the bundled helper', () => {
  it('Get queues an App Store install and the button opens the app once it lands', async () => {
    history.replaceState(null, '', '/?mock=mas-engine');
    const context = await setup('/appstore/1666327168', { tickMs: 5 });
    activeContext = context;
    const { pinia, router } = context;
    await startLocalState();
    const enqueue = vi.spyOn(commands, 'taskEnqueue');
    const wrapper = mount(AppStorePage, {
      props: { id: '1666327168' },
      global: { plugins: [pinia, router, i18n, ui] }
    });
    wrappers.push(wrapper);
    await flushPromises();
    await flushPromises();
    await wrapper
      .findAll('button')
      .find(node => node.text() === '获取')!
      .trigger('click');
    await flushPromises();
    expect(enqueue).toHaveBeenCalledWith('install', { kind: 'mas', token: '1666327168' }, { source: 'mas' }, 'manual');
    await vi.waitFor(() => expect(wrapper.findAll('button').some(node => node.text() === '打开')).toBe(true), {
      timeout: 3000
    });
  });
});
