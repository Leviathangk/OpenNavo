import { flushPromises, mount } from '@vue/test-utils';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { defineComponent, h } from 'vue';
import { i18n } from '@/i18n';
import { usePackageState } from '@/composables/usePackageState';
import type { Task, TaskOp } from '@/ipc/bindings';
import { commands, unwrap } from '@/ipc/client';
import { startLocalState, useLibraryStore, useLocalAppsStore, useTasksStore, useUpdatesStore } from '@/stores';
import { setup } from './helpers';

afterEach(() => {
  vi.restoreAllMocks();
});

// Rust rereads the inventory and update list seconds after a task's own event. These cases keep every read
// stale meanwhile and check that buttons and lists still show the finished task's result.
async function probe(token: string) {
  const { pinia, router } = await setup('/discover');
  await startLocalState();
  await useLocalAppsStore().load();
  let state = '';
  let open!: () => Promise<void>;
  const Probe = defineComponent({
    setup() {
      const { stateOf, act } = usePackageState();
      open = () => act('cask', token, 'open');
      return () => {
        state = stateOf('cask', token).state;
        return h('div');
      };
    }
  });
  const wrapper = mount(Probe, { global: { plugins: [pinia, router, i18n] } });
  await flushPromises();
  const current = async () => {
    wrapper.vm.$forceUpdate();
    await flushPromises();
    return state;
  };
  return { current, open, wrapper, tasks: useTasksStore() };
}

function task(op: TaskOp, token: string, finishedAt: number): Task {
  return {
    id: `${op}-${token}`,
    op,
    target: { kind: 'cask', token },
    targetName: null,
    outcome: null,
    options: {},
    trigger: 'manual',
    state: 'running',
    phase: 'installing',
    percent: 90,
    bytesDone: null,
    bytesTotal: null,
    speedBps: null,
    stepIndex: null,
    stepCount: null,
    fromVersion: null,
    toVersion: '1.0.0',
    error: null,
    exitCode: null,
    logPath: null,
    createdAt: finishedAt - 30_000,
    startedAt: finishedAt - 20_000,
    finishedAt: null
  };
}

/** Freeze what Rust returns now: the inventory, app list and update list from before the task. */
async function freezeReads() {
  const [library, apps, updates, status] = await Promise.all([
    unwrap(commands.libraryList()),
    unwrap(commands.localAppsList()),
    unwrap(commands.updatesList()),
    unwrap(commands.updatesStatus())
  ]);
  const finishedAt = Date.now();
  vi.spyOn(commands, 'libraryList').mockResolvedValue({ status: 'ok', data: library });
  vi.spyOn(commands, 'localAppsList').mockResolvedValue({ status: 'ok', data: apps });
  vi.spyOn(commands, 'updatesList').mockResolvedValue({ status: 'ok', data: updates });
  const checked = vi
    .spyOn(commands, 'updatesStatus')
    .mockResolvedValue({ status: 'ok', data: { ...status, checkedAt: finishedAt - 60_000 } });
  const reread = () => Promise.all([useLibraryStore().load(), useLocalAppsStore().load(), useUpdatesStore().load()]);
  return { finishedAt, status, checked, reread };
}

describe('A finished task shows its result before Rust rereads', () => {
  it('An upgrade shows Open at once and keeps Update away until the update list catches up', async () => {
    const { current, tasks, wrapper } = await probe('visual-studio-code');
    expect(await current()).toBe('update');
    const { finishedAt, status, checked, reread } = await freezeReads();

    const upgrade = task('upgrade', 'visual-studio-code', finishedAt);
    tasks.applyUpdate(upgrade);
    expect(await current()).toBe('running');
    tasks.applyUpdate({ ...upgrade, state: 'succeeded', percent: 100, finishedAt });
    expect(await current()).toBe('open');
    await reread();
    expect(await current()).toBe('open');

    // A brew outdated run after the upgrade that still lists the app is believed.
    checked.mockResolvedValue({ status: 'ok', data: { ...status, checkedAt: finishedAt + 1000 } });
    await useUpdatesStore().load();
    expect(await current()).toBe('update');
    wrapper.unmount();
  });

  it('A pin hides Update at once and an unpin brings it back', async () => {
    const { current, tasks, wrapper } = await probe('visual-studio-code');
    expect(await current()).toBe('update');
    const { finishedAt, reread } = await freezeReads();
    const updates = useUpdatesStore();
    const actionable = updates.actionable.length;

    tasks.applyUpdate({
      ...task('pin', 'visual-studio-code', finishedAt),
      state: 'succeeded',
      finishedAt
    });
    expect(await current()).toBe('open');
    expect(useLibraryStore().find('cask', 'visual-studio-code')?.pinned).toBe(true);
    expect(updates.actionable).toHaveLength(actionable - 1);
    await reread();
    expect(await current()).toBe('open');
    expect(useLibraryStore().find('cask', 'visual-studio-code')?.pinned).toBe(true);

    tasks.applyUpdate({
      ...task('unpin', 'visual-studio-code', finishedAt + 1),
      state: 'succeeded',
      finishedAt: finishedAt + 1
    });
    expect(await current()).toBe('update');
    expect(useLibraryStore().find('cask', 'visual-studio-code')?.pinned).toBe(false);
    wrapper.unmount();
  });

  it('An install shows Open at once and the inventory record replaces the stand-in', async () => {
    const { current, tasks, wrapper } = await probe('zed');
    expect(await current()).toBe('get');
    const { finishedAt, reread } = await freezeReads();
    const library = useLibraryStore();
    const before = library.items.length;

    tasks.applyUpdate({ ...task('install', 'zed', finishedAt), state: 'succeeded', finishedAt });
    expect(await current()).toBe('open');
    expect(library.items).toHaveLength(before + 1);
    await reread();
    expect(await current()).toBe('open');

    // The reread that lists it brings the real record and no duplicate.
    const listed = await unwrap(commands.libraryList());
    const record = { ...listed[0]!, token: 'zed', name: 'Zed', installedVersion: '1.0.0' };
    vi.mocked(commands.libraryList).mockResolvedValue({ status: 'ok', data: [...listed, record] });
    await library.load();
    expect(library.items.filter(item => item.token === 'zed')).toEqual([record]);
    expect(await current()).toBe('open');
    wrapper.unmount();
  });

  it('An adoption turns the app Homebrew-managed at once', async () => {
    const { current, tasks, wrapper } = await probe('notion');
    const apps = useLocalAppsStore();
    expect(apps.find('local-notion')).toMatchObject({
      cask: null,
      catalogToken: 'notion',
      source: 'direct'
    });
    const { finishedAt, reread } = await freezeReads();

    const adoption = { ...task('install', 'notion', finishedAt), options: { adopt: true } };
    tasks.applyUpdate({ ...adoption, state: 'succeeded', finishedAt });
    expect(await current()).toBe('open');
    expect(apps.find('local-notion')).toMatchObject({ cask: 'notion', source: 'homebrew' });
    await reread();
    expect(apps.find('local-notion')).toMatchObject({ cask: 'notion', source: 'homebrew' });
    wrapper.unmount();
  });

  it('An uninstall shows Get at once and its app leaves Installed', async () => {
    const { current, tasks, wrapper } = await probe('orbstack');
    expect(await current()).toBe('open');
    const apps = useLocalAppsStore();
    expect(apps.findCask('orbstack')).toBeDefined();
    const { finishedAt, reread } = await freezeReads();

    tasks.applyUpdate({
      ...task('uninstall', 'orbstack', finishedAt),
      state: 'succeeded',
      finishedAt
    });
    expect(await current()).toBe('get');
    expect(apps.findCask('orbstack')).toBeUndefined();
    await reread();
    expect(await current()).toBe('get');
    expect(apps.findCask('orbstack')).toBeUndefined();

    // A failed uninstall changes nothing.
    tasks.applyUpdate({ ...task('uninstall', 'raycast', finishedAt), state: 'failed', finishedAt });
    expect(apps.findCask('raycast')).toBeDefined();
    expect(useLibraryStore().find('cask', 'raycast')).toBeDefined();
    wrapper.unmount();
  });
});

it('Opening a just-installed app waits for an already-running inventory refresh', async () => {
  const { tasks, open, wrapper } = await probe('zed');
  const finishedAt = Date.now();
  tasks.applyUpdate({ ...task('install', 'zed', finishedAt), state: 'succeeded', finishedAt });
  const library = useLibraryStore();
  let complete!: () => void;
  const held = new Promise<void>(resolve => (complete = resolve));
  const refresh = vi.spyOn(commands, 'libraryRefresh').mockImplementation(async () => {
    await held;
    return { status: 'ok', data: library.items };
  });
  const appOpen = vi.spyOn(commands, 'appOpen').mockResolvedValue({ status: 'ok', data: null });
  const refreshing = library.refresh();
  const opening = open();
  await flushPromises();
  expect(refresh).toHaveBeenCalledTimes(1);
  expect(appOpen).not.toHaveBeenCalled();
  complete();
  await Promise.all([refreshing, opening]);
  expect(appOpen).toHaveBeenCalledWith({ kind: 'cask', token: 'zed' });
  await open();
  expect(refresh).toHaveBeenCalledTimes(1);
  wrapper.unmount();
});

it.each([true, false])('Opening after a pre-install refresh rechecks native records (found: %s)', async found => {
  const { tasks, open, wrapper } = await probe('zed');
  const library = useLibraryStore();
  const before = [...library.items];
  let finishOld!: () => void;
  let finishNew!: () => void;
  const oldRead = new Promise<void>(resolve => (finishOld = resolve));
  const newRead = new Promise<void>(resolve => (finishNew = resolve));
  const refresh = vi
    .spyOn(commands, 'libraryRefresh')
    .mockImplementationOnce(async () => {
      await oldRead;
      return { status: 'ok', data: before };
    })
    .mockImplementationOnce(async () => {
      await newRead;
      return { status: 'ok', data: found ? [...before, { ...before[0]!, token: 'zed' }] : before };
    });
  const appOpen = vi.spyOn(commands, 'appOpen').mockResolvedValue({ status: 'ok', data: null });
  const refreshing = library.refresh();
  const finishedAt = Date.now();
  tasks.applyUpdate({ ...task('install', 'zed', finishedAt), state: 'succeeded', finishedAt });
  const opening = open();
  finishOld();
  await refreshing;
  await flushPromises();
  expect(refresh).toHaveBeenCalledTimes(2);
  expect(appOpen).not.toHaveBeenCalled();
  finishNew();
  await opening;
  expect(appOpen).toHaveBeenCalledTimes(found ? 1 : 0);
  wrapper.unmount();
});
