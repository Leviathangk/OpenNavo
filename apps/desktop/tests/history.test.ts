import { flushPromises, mount } from '@vue/test-utils';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { commands } from '@/ipc/client';
import { i18n } from '@/i18n';
import HistoryList from '@/components/updates/HistoryList.vue';
import UpdatesPage from '@/pages/UpdatesPage.vue';
import InstalledPage from '@/pages/InstalledPage.vue';
import RunningTask from '@/components/updates/RunningTask.vue';
import { useTasksStore } from '@/stores';
import { setup } from './helpers';

let intersect: IntersectionObserverCallback;
beforeEach(() => {
  vi.stubGlobal(
    'IntersectionObserver',
    class {
      constructor(callback: IntersectionObserverCallback) {
        intersect = callback;
      }
      observe() {}
      disconnect() {}
    }
  );
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

it('Cache cleanup progress belongs to Installed, while upgrade progress stays in Updates', async () => {
  const context = await setup('/installed');
  const tasks = useTasksStore();
  const task = {
    ...context.state.history[0]!,
    id: 'cleanup-progress',
    op: 'cleanup' as const,
    state: 'running' as const,
    target: null,
    percent: 42,
    finishedAt: null
  };
  tasks.applyUpdate(task);
  const global = { plugins: [context.pinia, context.router, i18n] };
  const installed = mount(InstalledPage, { global });
  const updates = mount(UpdatesPage, { global });
  try {
    await flushPromises();
    expect(installed.findComponent(RunningTask).props('task').percent).toBe(42);
    expect(updates.findComponent(RunningTask).exists()).toBe(false);
    tasks.applyUpdate({ ...task, percent: 85 });
    await flushPromises();
    expect(installed.findComponent(RunningTask).props('task').percent).toBe(85);
    tasks.applyUpdate({ ...task, state: 'succeeded', percent: 100 });
    await flushPromises();
    expect(installed.findComponent(RunningTask).exists()).toBe(false);
    tasks.applyUpdate({
      ...task,
      id: 'upgrade-progress',
      op: 'upgrade',
      target: { kind: 'cask', token: 'firefox' }
    });
    await flushPromises();
    expect(installed.findComponent(RunningTask).exists()).toBe(false);
    expect(updates.findComponent(RunningTask).props('task').op).toBe('upgrade');
  } finally {
    installed.unmount();
    updates.unmount();
  }
});

it('Updates history loads 20 initially and stops at the latest 50', async () => {
  const context = await setup('/updates');
  const sample = context.state.history[0]!;
  context.state.history = Array.from({ length: 110 }, (_, i) => ({
    ...sample,
    op: 'upgrade',
    id: `scroll-${i}`
  }));
  const spy = vi.spyOn(commands, 'historyList');
  const wrapper = mount(UpdatesPage, {
    global: { plugins: [context.pinia, context.router, i18n] }
  });
  await flushPromises();
  expect(wrapper.findComponent(HistoryList).props('tasks')).toHaveLength(20);
  for (const count of [40, 50]) {
    intersect([{ isIntersecting: true } as IntersectionObserverEntry], {} as IntersectionObserver);
    await flushPromises();
    expect(wrapper.findComponent(HistoryList).props('tasks')).toHaveLength(count);
  }
  expect(spy.mock.calls.map(([query]) => [query.offset, query.limit])).toEqual([
    [0, 20],
    [20, 20],
    [40, 10]
  ]);
  expect(wrapper.findAll('button').some(button => button.text().includes(i18n.global.t('common.loadMore')))).toBe(
    false
  );
  wrapper.unmount();
});

it('Monthly history traverses all pages without 200/500-entry truncation', async () => {
  const { loadHistory } = await import('@/utils/history');
  const context = await setup('/updates');
  const sample = context.state.history[0]!;
  const now = Date.now();
  context.state.history = Array.from({ length: 521 }, (_, i) => ({
    ...sample,
    id: `month-${i}`,
    op: 'upgrade',
    state: i % 2 ? 'failed' : 'succeeded',
    finishedAt: now
  }));
  const result = await loadHistory({
    q: null,
    ops: ['upgrade'],
    states: ['succeeded', 'failed'],
    target: null,
    includeScheduledUpdates: false,
    from: now - 1,
    to: null,
    limit: Number.MAX_SAFE_INTEGER,
    offset: 0
  });
  expect(result.total).toBe(521);
  expect(result.items).toHaveLength(521);
  expect(new Set(result.items.map(task => task.id)).size).toBe(521);
  expect(result.items.filter(task => task.state === 'succeeded')).toHaveLength(261);
});

it('Legacy formula rows do not consume visible history pages or the 50-row cap', async () => {
  const context = await setup('/updates');
  const sample = context.state.history[0]!;
  context.state.history = [
    ...Array.from({ length: 60 }, (_, i) => ({
      ...sample,
      op: 'upgrade' as const,
      id: `formula-${i}`,
      target: { kind: 'formula' as const, token: 'node' }
    })),
    ...Array.from({ length: 70 }, (_, i) => ({
      ...sample,
      op: 'upgrade' as const,
      id: `app-${i}`,
      target: { kind: 'cask' as const, token: 'firefox' }
    }))
  ];
  const wrapper = mount(UpdatesPage, {
    global: { plugins: [context.pinia, context.router, i18n] }
  });
  try {
    await flushPromises();
    expect(wrapper.findComponent(HistoryList).props('tasks')).toHaveLength(20);
    for (const count of [40, 50]) {
      intersect([{ isIntersecting: true } as IntersectionObserverEntry], {} as IntersectionObserver);
      await flushPromises();
      expect(wrapper.findComponent(HistoryList).props('tasks')).toHaveLength(count);
    }
    expect(
      wrapper
        .findComponent(HistoryList)
        .props('tasks')
        .every((task: { id: string }) => task.id.startsWith('app-'))
    ).toBe(true);
    expect(wrapper.findAll('button').some(button => button.text().includes(i18n.global.t('common.loadMore')))).toBe(
      false
    );
  } finally {
    wrapper.unmount();
  }
});

it('History includes clean runs and local app leftovers', async () => {
  const context = await setup('/history');
  const sample = context.state.history[0]!;
  context.state.history = [
    { ...sample, id: 'clean-run', op: 'clean', target: null },
    {
      ...sample,
      id: 'leftovers-run',
      op: 'remove_leftovers',
      target: { kind: 'app', token: 'local-app' }
    }
  ];
  const wrapper = mount(UpdatesPage, {
    global: { plugins: [context.pinia, context.router, i18n] }
  });
  await flushPromises();
  expect(
    wrapper
      .findComponent(HistoryList)
      .props('tasks')
      .map((task: { id: string }) => task.id)
  ).toEqual(['clean-run', 'leftovers-run']);
  wrapper.unmount();
});

it.each([
  ['uninstall', 'app', '/installed'],
  ['uninstall', 'cask', '/installed'],
  ['clean', 'app', '/cleanup'],
  ['remove_leftovers', 'app', '/cleanup']
] as const)('Routes failed planned %s tasks to review without replaying a consumed plan', async (op, kind, route) => {
  const context = await setup('/updates');
  const task = {
    ...context.state.history[0]!,
    op,
    state: 'failed' as const,
    target: { kind, token: 'local-editor' },
    options: { planId: 'consumed-before-restart' }
  };
  const enqueue = vi.spyOn(commands, 'taskEnqueue');
  const wrapper = mount(HistoryList, {
    props: { tasks: [task] },
    global: { plugins: [context.pinia, context.router, i18n] }
  });
  await wrapper
    .findAll('button')
    .find(button => button.text() === '重新检查')!
    .trigger('click');
  await flushPromises();
  expect(context.router.currentRoute.value.path).toBe(route);
  expect(enqueue).not.toHaveBeenCalled();
  wrapper.unmount();
});

it.each(['install', 'upgrade', 'uninstall'] as const)('Keeps ordinary Homebrew %s history retry', async op => {
  const context = await setup('/updates');
  const task = {
    ...context.state.history[0]!,
    op,
    state: 'failed' as const,
    target: { kind: 'cask' as const, token: 'ghostty' },
    options: { zap: false },
    error: null
  };
  const enqueue = vi.spyOn(useTasksStore(), 'enqueue').mockResolvedValue(null);
  const wrapper = mount(HistoryList, {
    props: { tasks: [task] },
    global: { plugins: [context.pinia, context.router, i18n] }
  });
  await wrapper
    .findAll('button')
    .find(button => button.text() === '重试')!
    .trigger('click');
  expect(enqueue).toHaveBeenCalledWith(op, task.target, 'retry', task.options);
  wrapper.unmount();
});
