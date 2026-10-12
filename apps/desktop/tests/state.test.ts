import { flushPromises, mount } from '@vue/test-utils';
import { describe, expect, it } from 'vitest';
import { defineComponent, h } from 'vue';
import { i18n } from '@/i18n';
import { usePackageState } from '@/composables/usePackageState';
import type { Task } from '@/ipc/bindings';
import { commands, unwrap } from '@/ipc/client';
import { startLocalState, useLibraryStore, useTasksStore, useUpdatesStore } from '@/stores';
import { setup } from './helpers';

// 06 §13: derive the Get button state.
async function states(targets: [string, 'cask' | 'formula', boolean?][], options: { brew?: boolean } = {}) {
  const { pinia, router } = await setup('/discover', options);
  await startLocalState();
  let result: Record<string, string> = {};
  let progress: Record<string, number | undefined> = {};
  const Probe = defineComponent({
    setup() {
      const { stateOf } = usePackageState();
      return () => {
        const current = targets.map(([token, kind, disabled]) => [token, stateOf(kind, token, disabled)] as const);
        result = Object.fromEntries(current.map(([token, state]) => [token, state.state]));
        progress = Object.fromEntries(current.map(([token, state]) => [token, state.progress]));
        return h('div');
      };
    }
  });
  const wrapper = mount(Probe, { global: { plugins: [pinia, router, i18n] } });
  await flushPromises();
  return { result: () => result, progress: () => progress, wrapper, tasks: useTasksStore() };
}

describe('Get button state (06 §13)', () => {
  it('Uninstalled Get, disabled unavailable, updatable Update, installed apps Open; pinned excluded', async () => {
    const { result } = await states([
      ['zed', 'cask'],
      ['zed', 'cask', true],
      ['visual-studio-code', 'cask'],
      ['orbstack', 'cask'],
      ['raycast', 'cask']
    ]);
    expect(result()).toEqual({
      zed: 'unavailable',
      'visual-studio-code': 'update',
      orbstack: 'open',
      raycast: 'open'
    });
  });

  it('Cask-only local installed/update lists exclude Homebrew command-line tools', async () => {
    const { result } = await states([['gh', 'formula']]);
    // The simulated Homebrew installation really contains command-line tools; the UI store filters them out.
    const raw = await unwrap(commands.libraryList());
    expect(raw.some(item => item.kind === 'formula')).toBe(true);
    const library = useLibraryStore();
    expect(library.items.length).toBeGreaterThan(0);
    expect(library.items.every(item => item.kind === 'cask')).toBe(true);
    expect(library.find('formula', 'gh')).toBeUndefined();
    expect(useUpdatesStore().items.every(item => item.kind === 'cask')).toBe(true);
    expect(result().gh).toBe('get');
  });

  it('Tasks display queued/running states', async () => {
    const { result, progress, tasks, wrapper } = await states([['orbstack', 'cask']]);
    const task: Task = {
      id: 't1',
      op: 'reinstall',
      target: { kind: 'cask', token: 'orbstack' },
      targetName: null,
      outcome: null,
      options: {},
      trigger: 'manual',
      state: 'queued',
      phase: null,
      percent: null,
      bytesDone: null,
      bytesTotal: null,
      speedBps: null,
      stepIndex: null,
      stepCount: null,
      fromVersion: null,
      toVersion: null,
      error: null,
      exitCode: null,
      logPath: null,
      createdAt: Date.now(),
      startedAt: null,
      finishedAt: null
    };
    tasks.applyUpdate(task);
    wrapper.vm.$forceUpdate();
    await flushPromises();
    expect(result().orbstack).toBe('queued');

    // Unknown progress shows an empty ring rather than a spinner; reported progress fills it.
    tasks.applyUpdate({ ...task, state: 'running', phase: 'preparing' });
    wrapper.vm.$forceUpdate();
    await flushPromises();
    expect(result().orbstack).toBe('running');
    expect(progress().orbstack).toBe(0);
    tasks.applyUpdate({ ...task, state: 'running', phase: 'downloading', percent: 28 });
    wrapper.vm.$forceUpdate();
    await flushPromises();
    expect(progress().orbstack).toBe(28);
  });

  it('Without Homebrew always show get with Install Homebrew first and open Welcome (08 §10.15)', async () => {
    const { result } = await states([['visual-studio-code', 'cask']], { brew: false });
    expect(result()['visual-studio-code']).toBe('get');

    const { pinia, router } = await setup('/discover', { brew: false });
    await startLocalState();
    let current: ReturnType<ReturnType<typeof usePackageState>['stateOf']> | undefined;
    let act: ReturnType<typeof usePackageState>['act'] | undefined;
    const Probe = defineComponent({
      setup() {
        const state = usePackageState();
        act = state.act;
        return () => {
          current = state.stateOf('cask', 'zed');
          return h('div');
        };
      }
    });
    mount(Probe, { global: { plugins: [pinia, router, i18n] } });
    await flushPromises();
    expect(current).toEqual({ state: 'get', label: '先安装 Homebrew' });
    await act?.('cask', 'zed', 'get');
    expect(router.currentRoute.value.name).toBe('welcome');
    expect(await unwrap(commands.taskListActive())).toHaveLength(0);
  });
});
