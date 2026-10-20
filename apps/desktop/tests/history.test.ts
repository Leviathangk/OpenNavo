import { flushPromises, mount } from '@vue/test-utils';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { ref } from 'vue';
import { createOnUi } from '@opennavo/ui';
import { commands } from '@/ipc/client';
import { i18n } from '@/i18n';
import HistoryList from '@/components/updates/HistoryList.vue';
import UpdatesRail from '@/components/rails/UpdatesRail.vue';
import { useToasts } from '@/composables/useToasts';
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
  useToasts().clear();
  document.body.innerHTML = '';
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

it('A finished task rereads history in the background without collapsing loaded rows', async () => {
  const context = await setup('/updates');
  const sample = context.state.history[0]!;
  context.state.history = Array.from({ length: 110 }, (_, i) => ({
    ...sample,
    op: 'upgrade',
    id: `reread-${i}`
  }));
  const list = commands.historyList;
  let holding = false;
  let release: () => void = () => {};
  const held = new Promise<void>(resolve => (release = resolve));
  const spy = vi.spyOn(commands, 'historyList').mockImplementation(async query => {
    if (holding && query.offset === 0) await held;
    return list(query);
  });
  const wrapper = mount(UpdatesPage, {
    global: { plugins: [context.pinia, context.router, i18n] }
  });
  await flushPromises();
  for (let i = 0; i < 2; i++) {
    intersect([{ isIntersecting: true } as IntersectionObserverEntry], {} as IntersectionObserver);
    await flushPromises();
  }
  expect(wrapper.findComponent(HistoryList).props('tasks')).toHaveLength(50);

  holding = true;
  useTasksStore().applyUpdate({ ...sample, id: 'just-finished', state: 'succeeded', finishedAt: Date.now() });
  await flushPromises();
  // The reread is in flight: the rows stay and no loading line replaces them.
  expect(wrapper.findComponent(HistoryList).props('tasks')).toHaveLength(50);
  expect(wrapper.text()).not.toContain(i18n.global.t('common.loading'));

  release();
  await flushPromises();
  expect(spy.mock.calls.at(-1)![0]).toMatchObject({ offset: 0, limit: 50 });
  expect(wrapper.findComponent(HistoryList).props('tasks')).toHaveLength(50);
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

// Clear history: one confirmation, then every finished record goes and the screens built from them reread.
const ui = createOnUi({ locale: ref('zh-CN' as const) });
const dialogButton = (attribute: 'data-on-cancel' | 'data-on-confirm') =>
  document.body.querySelector<HTMLButtonElement>(`[role="alertdialog"] button[${attribute}]`);

it('Clearing the history asks first, then empties the list and the month stats', async () => {
  const context = await setup('/updates');
  const sample = context.state.history[0]!;
  const now = Date.now();
  context.state.history = [
    {
      ...sample,
      id: 'worked',
      op: 'upgrade',
      state: 'succeeded',
      target: { kind: 'cask', token: 'firefox' },
      finishedAt: now
    },
    {
      ...sample,
      id: 'broke',
      op: 'upgrade',
      state: 'failed',
      target: { kind: 'cask', token: 'ghostty' },
      finishedAt: now
    }
  ];
  const global = { plugins: [context.pinia, context.router, i18n, ui] };
  const page = mount(UpdatesPage, { global, attachTo: document.body });
  const rail = mount(UpdatesRail, { global });
  try {
    await flushPromises();
    expect(page.findComponent(HistoryList).props('tasks')).toHaveLength(2);
    expect(rail.text()).toContain('50%');
    const clear = vi.spyOn(commands, 'historyClear');
    const open = () => page.findAll('button').find(button => button.text() === '清空记录')!;

    // Asking is not clearing.
    await open().trigger('click');
    await flushPromises();
    expect(document.body.textContent).toContain('确定清空全部更新记录吗？');
    expect(document.body.textContent).toContain('还能撤销的卸载和清理、还能回滚的更新会保留。');
    dialogButton('data-on-cancel')!.click();
    await flushPromises();
    expect(clear).not.toHaveBeenCalled();
    expect(page.findComponent(HistoryList).props('tasks')).toHaveLength(2);

    await open().trigger('click');
    await flushPromises();
    dialogButton('data-on-confirm')!.click();
    await flushPromises();
    expect(clear).toHaveBeenCalledTimes(1);
    expect(context.state.history).toEqual([]);
    expect(page.findComponent(HistoryList).exists()).toBe(false);
    expect(page.text()).toContain('还没有更新记录');
    expect(rail.text()).not.toContain('50%');
    expect(useToasts().items.value.map(toast => toast.title)).toContain('更新记录已清空');
    // Nothing left to clear.
    expect(open().attributes('disabled')).toBeDefined();
  } finally {
    page.unmount();
    rail.unmount();
  }
});

it('Clearing keeps what can still be undone or rolled back, and says how many', async () => {
  const context = await setup('/updates');
  const sample = context.state.history[0]!;
  const now = Date.now();
  const outcome = (restorable: boolean) => ({
    freedBytes: 3_000,
    trashedBytes: 1_000,
    trashed: 2,
    deleted: 4,
    skipped: [],
    restorable
  });
  context.state.history = [
    {
      ...sample,
      id: 'uninstall-in-trash',
      op: 'uninstall',
      target: { kind: 'cask', token: 'zoom' },
      outcome: outcome(true),
      finishedAt: now
    },
    {
      ...sample,
      id: 'update-with-kept-copy',
      op: 'upgrade',
      state: 'succeeded',
      target: { kind: 'app', token: 'local-notion' },
      targetName: 'Notion',
      options: { source: 'electron' },
      finishedAt: now - 1_000
    },
    { ...sample, id: 'clean-emptied', op: 'clean', target: null, outcome: outcome(false), finishedAt: now - 2_000 },
    {
      ...sample,
      id: 'plain-update',
      op: 'upgrade',
      state: 'failed',
      target: { kind: 'cask', token: 'firefox' },
      finishedAt: now - 3_000
    }
  ];
  const page = mount(UpdatesPage, {
    global: { plugins: [context.pinia, context.router, i18n, ui] },
    attachTo: document.body
  });
  const ids = () =>
    page
      .findComponent(HistoryList)
      .props('tasks')
      .map((task: { id: string }) => task.id);
  try {
    await flushPromises();
    expect(ids()).toHaveLength(4);
    await page
      .findAll('button')
      .find(button => button.text() === '清空记录')!
      .trigger('click');
    await flushPromises();
    dialogButton('data-on-confirm')!.click();
    await flushPromises();
    expect(context.state.history.map(task => task.id)).toEqual(['uninstall-in-trash', 'update-with-kept-copy']);
    expect(ids()).toEqual(['uninstall-in-trash', 'update-with-kept-copy']);
    expect(useToasts().items.value.map(toast => toast.title)).toContain('已清空，2 条还能撤销或回滚的记录已保留');
    // Still there to act on.
    expect(page.text()).toContain('撤销');
  } finally {
    page.unmount();
  }
});

it('A failed clear says so and keeps the records', async () => {
  const context = await setup('/updates');
  const sample = context.state.history[0]!;
  context.state.history = [{ ...sample, id: 'kept', op: 'upgrade', target: { kind: 'cask', token: 'firefox' } }];
  vi.spyOn(commands, 'historyClear').mockResolvedValue({
    status: 'error',
    error: { code: 'E_INTERNAL', message: 'database locked', detail: null }
  });
  const page = mount(UpdatesPage, {
    global: { plugins: [context.pinia, context.router, i18n, ui] },
    attachTo: document.body
  });
  try {
    await flushPromises();
    await page
      .findAll('button')
      .find(button => button.text() === '清空记录')!
      .trigger('click');
    await flushPromises();
    dialogButton('data-on-confirm')!.click();
    await flushPromises();
    expect(page.findComponent(HistoryList).props('tasks')).toHaveLength(1);
    const toast = useToasts().items.value.at(-1);
    expect(toast).toMatchObject({ tone: 'danger', title: '操作没有完成' });
    expect(useToasts().items.value.map(item => item.title)).not.toContain('更新记录已清空');
  } finally {
    page.unmount();
  }
});

it('Clearing is offered only while there is something to clear', async () => {
  const context = await setup('/updates');
  const sample = context.state.history[0]!;
  const clearButton = (page: ReturnType<typeof mount>) =>
    page.findAll('button').find(item => item.text() === '清空记录')!;
  context.state.history = [];
  const empty = mount(UpdatesPage, { global: { plugins: [context.pinia, context.router, i18n, ui] } });
  try {
    await flushPromises();
    expect(clearButton(empty).attributes('disabled')).toBeDefined();
  } finally {
    empty.unmount();
  }

  // A filter can show no rows while other records exist: clearing stays available there.
  context.state.history = [
    { ...sample, id: 'fine', op: 'upgrade', state: 'succeeded', target: { kind: 'cask', token: 'firefox' } }
  ];
  const page = mount(UpdatesPage, { global: { plugins: [context.pinia, context.router, i18n, ui] } });
  try {
    await flushPromises();
    expect(clearButton(page).attributes('disabled')).toBeUndefined();
    await page
      .findAll('button')
      .find(item => item.text() === '失败')!
      .trigger('click');
    await flushPromises();
    expect(page.findComponent(HistoryList).exists()).toBe(false);
    expect(clearButton(page).attributes('disabled')).toBeUndefined();
  } finally {
    page.unmount();
  }
});
