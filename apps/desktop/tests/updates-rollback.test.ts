import { flushPromises, mount } from '@vue/test-utils';
import type { DOMWrapper, VueWrapper } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ref } from 'vue';
import { createOnUi } from '@opennavo/ui';
import { useToasts } from '@/composables/useToasts';
import { i18n } from '@/i18n';
import type { Task } from '@/ipc/bindings';
import { commands } from '@/ipc/client';
import { disposeMockIpc } from '@/ipc/mock';
import HistoryList from '@/components/updates/HistoryList.vue';
import UpdatesPage from '@/pages/UpdatesPage.vue';
import { startLocalState, useUpdatesStore } from '@/stores';
import { setup } from './helpers';

// One-click rollback (store/security plan §7, P2): an update from a source other than Homebrew or the App Store kept
// the version it replaced. History offers to go back to it on the newest such update of an app; Rust checks the kept
// copy again, asks a running app to quit and skips the version rolled back from. ?mock=update-sources seeds Notion's
// Electron update 4.3.0 → 4.4.1.
const wrappers: VueWrapper[] = [];
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
  disposeMockIpc();
  wrappers.splice(0).forEach(wrapper => wrapper.unmount());
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  history.replaceState(null, '', '/');
  document.body.innerHTML = '';
});

const ui = createOnUi({ locale: ref('zh-CN' as const) });

async function mountUpdates() {
  history.replaceState(null, '', '/?mock=update-sources');
  const context = await setup('/updates', { tickMs: 5 });
  const { pinia, router } = context;
  await startLocalState();
  const wrapper = mount(UpdatesPage, { global: { plugins: [pinia, router, i18n, ui] }, attachTo: document.body });
  wrappers.push(wrapper);
  await vi.waitFor(() => expect(useUpdatesStore().loaded).toBe(true));
  await vi.waitFor(() => expect(entries(wrapper).length).toBeGreaterThan(0));
  await flushPromises();
  return { wrapper, context };
}

const entries = (wrapper: VueWrapper) => wrapper.findAllComponents(HistoryList).flatMap(list => list.findAll('li'));
const entry = (wrapper: VueWrapper, ...texts: string[]) =>
  entries(wrapper).find(item => texts.every(text => item.text().includes(text)));
const button = (row: DOMWrapper<Element> | undefined, text: string) =>
  row?.findAll('button').find(node => node.text() === text);
const dialog = () => document.querySelector('[role="alertdialog"]');

describe('Rolling back an update', () => {
  it('Offers it on the kept update only, says what happens, and runs it into history', async () => {
    const { wrapper, context } = await mountUpdates();
    const update = context.state.history.find(task => task.targetName === 'Notion')!;
    const notion = entry(wrapper, 'Notion', '4.4.1');
    expect(button(notion, '回滚')).toBeTruthy();
    // Homebrew updates keep no copy of their own.
    expect(button(entry(wrapper, 'iTerm2', '3.5.14'), '回滚')).toBeUndefined();

    const rollback = vi.spyOn(commands, 'updateRollback');
    await button(notion, '回滚')!.trigger('click');
    await flushPromises();
    // The local app's display name, as the history row shows it.
    const text = dialog()?.textContent ?? '';
    expect(text).toContain('把 Notion 工作空间 回滚到 4.3.0？');
    expect(text).toContain(
      '换回这次更新前保留的 4.3.0。如果 Notion 工作空间 正在运行，会先请它正常退出，回滚后再打开。'
    );
    expect(text).toContain('之后不再提示 4.4.1，有更新的版本时再提示。');
    expect(rollback).not.toHaveBeenCalled();
    document.querySelector<HTMLButtonElement>('[data-rollback-confirm]')!.click();
    await flushPromises();
    expect(rollback).toHaveBeenCalledWith(update.id);
    // No second rollback is offered while this one runs.
    expect(button(entry(wrapper, 'Notion', '4.3.0 → 4.4.1'), '回滚')).toBeUndefined();

    await vi.waitFor(() => expect(entry(wrapper, 'Notion', '回滚', '4.4.1 → 4.3.0')).toBeTruthy(), { timeout: 4000 });
    const done = context.state.history.find(task => task.op === 'rollback')!;
    expect(done).toMatchObject({
      state: 'succeeded',
      target: { kind: 'app', token: 'local-notion' },
      options: { source: 'electron', rollbackOf: update.id, quitRunning: true, reopen: true }
    });
    // The kept copy is spent: neither the update nor the rollback offers to go back again.
    await vi.waitFor(() => expect(useUpdatesStore().rollbackable.size).toBe(0));
    expect(entries(wrapper).some(item => button(item, '回滚'))).toBe(false);
  });

  it('Cancel leaves everything as it was', async () => {
    const { wrapper } = await mountUpdates();
    const rollback = vi.spyOn(commands, 'updateRollback');
    await button(entry(wrapper, 'Notion', '4.4.1'), '回滚')!.trigger('click');
    await flushPromises();
    document.querySelector<HTMLButtonElement>('[data-rollback-cancel]')!.click();
    await flushPromises();
    expect(dialog()).toBeNull();
    expect(rollback).not.toHaveBeenCalled();
    expect(button(entry(wrapper, 'Notion', '4.4.1'), '回滚')).toBeTruthy();
  });

  it('Shows why Rust refused, and keeps the offer to whatever Rust still allows', async () => {
    const { wrapper } = await mountUpdates();
    vi.spyOn(commands, 'updateRollback').mockResolvedValue({
      status: 'error',
      error: { code: 'E_ROLLBACK_UNAVAILABLE', message: 'installed_version_changed', detail: null }
    });
    await button(entry(wrapper, 'Notion', '4.4.1'), '回滚')!.trigger('click');
    await flushPromises();
    document.querySelector<HTMLButtonElement>('[data-rollback-confirm]')!.click();
    await flushPromises();
    expect(useToasts().items.value.at(-1)?.description).toBe('保留的旧版本已不可用，或这个 App 之后又有变化，不能回滚');
  });
});

describe('Rollback entries in history', () => {
  const failed = (code: string): Task => ({
    id: 'rollback-1',
    op: 'rollback',
    target: { kind: 'app', token: 'local-notion' },
    targetName: 'Notion',
    outcome: null,
    options: { source: 'electron', rollbackOf: 'update-1', quitRunning: true, reopen: true },
    trigger: 'manual',
    state: code === 'E_APP_RUNNING' ? 'canceled' : 'failed',
    phase: null,
    percent: null,
    bytesDone: null,
    bytesTotal: null,
    speedBps: null,
    stepIndex: 1,
    stepCount: 2,
    fromVersion: '4.4.1',
    toVersion: '4.3.0',
    error: { code, message: 'x', detail: null },
    exitCode: null,
    logPath: null,
    createdAt: Date.now() - 60_000,
    startedAt: Date.now() - 50_000,
    finishedAt: Date.now() - 40_000
  });

  async function mountList(task: Task) {
    history.replaceState(null, '', '/?mock=update-sources');
    const context = await setup('/updates', { tickMs: 5 });
    const { pinia, router } = context;
    const wrapper = mount(HistoryList, {
      props: { tasks: [task] },
      global: { plugins: [pinia, router, i18n, ui] },
      attachTo: document.body
    });
    wrappers.push(wrapper);
    await flushPromises();
    return wrapper;
  }

  it('A rollback the app would not quit for says so in rollback words and can be retried', async () => {
    const wrapper = await mountList(failed('E_APP_RUNNING'));
    // Without the local app list loaded, the name recorded with the task stands in.
    expect(wrapper.text()).toContain('Notion 没有退出，没有回滚；退出后可以再试');
    expect(wrapper.text()).not.toContain('已暂缓更新');
    const enqueue = vi.spyOn(commands, 'taskEnqueue');
    await button(wrapper.find('li'), '重试')!.trigger('click');
    await flushPromises();
    // Rust checks the recorded update again before it takes the retry.
    expect(enqueue).toHaveBeenCalledWith(
      'rollback',
      { kind: 'app', token: 'local-notion' },
      expect.objectContaining({ rollbackOf: 'update-1', source: 'electron' }),
      'retry'
    );
  });

  it('A kept copy that fails the signature check is named as such', async () => {
    const wrapper = await mountList(failed('E_ROLLBACK_INTEGRITY'));
    expect(wrapper.text()).toContain('保留的旧版本没有通过签名校验，没有回滚');
    expect(wrapper.text()).toContain('4.4.1 → 4.3.0');
    expect(wrapper.text()).toContain('回滚');
  });
});
