import { flushPromises } from '@vue/test-utils';
import { disposePinia } from 'pinia';
import { nextTick } from 'vue';
import { afterEach, expect, it, vi } from 'vitest';
import { commands, events } from '@/ipc/client';
import { useLocalAppsStore } from '@/stores/localApps';
import type { Footprint } from '@/ipc/bindings';
import {
  startGuideState,
  startLocalState,
  startTrayState,
  useEnvStore,
  useTasksStore,
  useSettingsStore,
  useUpdatesStore,
  usePermissionsStore
} from '@/stores';
import { useStateSubscriptions } from '@/stores/stateSubscriptions';
import { useToasts } from '@/composables/useToasts';
import { setup } from './helpers';

afterEach(() => {
  useToasts().clear();
  vi.restoreAllMocks();
});

it('Hundreds of completed tasks retain only recent logs, while active task logs survive', async () => {
  const context = await setup();
  try {
    const tasks = useTasksStore();
    const sample = context.state.history[0]!;
    tasks.applyUpdate({ ...sample, id: 'active', state: 'running' });
    tasks.appendLog({ id: 'active', lines: ['still running'] });
    for (let index = 0; index < 500; index += 1) {
      const task = { ...sample, id: `completed-${index}`, state: 'running' as const };
      tasks.applyUpdate(task);
      tasks.appendLog({ id: task.id, lines: Array.from({ length: 250 }, () => 'output') });
      tasks.applyUpdate({ ...task, state: 'succeeded' });
    }
    expect(tasks.finished).toHaveLength(20);
    expect(Object.keys(tasks.logs)).toHaveLength(21);
    expect(tasks.logs.active).toEqual(['still running']);
    expect(tasks.logs['completed-499']).toHaveLength(200);
    tasks.appendLog({ id: 'completed-0', lines: ['late output'] });
    expect(tasks.logs['completed-0']).toBeUndefined();
    tasks.appendLog({ id: 'completed-499', lines: ['x'.repeat(200_000)] });
    expect(tasks.logs['completed-499']!.join('').length).toBeLessThanOrEqual(64 * 1024);
  } finally {
    disposePinia(context.pinia);
  }
});

it('Clearing or replacing toasts releases their action callbacks', () => {
  const toasts = useToasts();
  const callback = vi.fn();
  const toast = { title: 'Undo removal', actionLabel: 'Undo' };
  const cleared = toasts.pushAction(toast, callback);
  toasts.clear();
  toasts.runAction(cleared);
  const replaced = toasts.pushAction(toast, callback);
  toasts.push({ id: replaced, title: 'Replacement' });
  toasts.runAction(replaced);
  expect(callback).not.toHaveBeenCalled();
  const live = toasts.pushAction(toast, callback);
  toasts.runAction(live);
  toasts.runAction(live);
  expect(callback).toHaveBeenCalledTimes(1);
});

it.each([startLocalState, startTrayState, startGuideState])(
  '%s shares concurrent initialization and disposes window listeners',
  async start => {
    const context = await setup();
    const subscriptions = useStateSubscriptions();
    const stops: ReturnType<typeof vi.fn>[] = [];
    const listenerSpies = [
      events.taskUpdated,
      events.permissionsChanged,
      events.taskLog,
      events.libraryChanged,
      events.localAppsChanged,
      events.uninstallScanProgress,
      events.updatesChanged,
      events.catalogSynced,
      events.envChanged,
      events.localeChanged
    ].map(event =>
      vi.spyOn(event, 'listen').mockImplementation(async () => {
        const stop = vi.fn();
        stops.push(stop);
        return stop;
      })
    );
    const removeFocus = vi.spyOn(window, 'removeEventListener');
    try {
      await Promise.all([start(), start(), start()]);
      expect(listenerSpies.every(spy => spy.mock.calls.length <= 1)).toBe(true);
      expect(stops.length).toBeGreaterThan(0);
      subscriptions.stopAll();
      expect(stops.every(stop => stop.mock.calls.length === 1)).toBe(true);
      if (start === startLocalState) {
        expect(removeFocus.mock.calls.some(call => call[0] === 'focus')).toBe(true);
      }
      await start();
      disposePinia(context.pinia);
      expect(stops.every(stop => stop.mock.calls.length === 1)).toBe(true);
    } finally {
      subscriptions.stopAll();
    }
  }
);

it('Startup failure and disposal release subscriptions that resolve later', async () => {
  const context = await setup();
  const subscriptions = useStateSubscriptions();
  const stop = vi.fn();
  let resolve!: (cleanup: () => void) => void;
  const pending = new Promise<() => void>(done => {
    resolve = done;
  });
  const failure = subscriptions.start('failure', async register => {
    await Promise.all([register(pending), Promise.reject(new Error('listen failed'))]);
  });
  await expect(failure).rejects.toThrow('listen failed');
  resolve(stop);
  await pending;
  await Promise.resolve();
  expect(stop).toHaveBeenCalledTimes(1);

  let resolveDisposed!: (cleanup: () => void) => void;
  const stopDisposed = vi.fn();
  const ready = subscriptions.start('disposed', async register => {
    await register(
      new Promise(done => {
        resolveDisposed = done;
      })
    );
  });
  disposePinia(context.pinia);
  resolveDisposed(stopDisposed);
  await ready;
  expect(stopDisposed).toHaveBeenCalledTimes(1);
});

it('Stopping local state removes the settings watcher and focus handler', async () => {
  const context = await setup();
  await startLocalState();
  const settings = useSettingsStore();
  const refresh = vi.spyOn(useUpdatesStore(), 'loadStatus').mockResolvedValue(undefined);
  const focus = vi.spyOn(usePermissionsStore(), 'returned').mockResolvedValue(undefined);
  const subscriptions = useStateSubscriptions();
  try {
    settings.value = { ...settings.value!, checkTime: '10:00' };
    await nextTick();
    expect(refresh).toHaveBeenCalledTimes(1);
    window.dispatchEvent(new Event('focus'));
    expect(focus).toHaveBeenCalledTimes(1);
    subscriptions.stopAll();
    settings.value = { ...settings.value!, checkTime: '11:00' };
    await nextTick();
    window.dispatchEvent(new Event('focus'));
    expect(refresh).toHaveBeenCalledTimes(1);
    expect(focus).toHaveBeenCalledTimes(1);
  } finally {
    disposePinia(context.pinia);
  }
});

it('Coming back to the window looks again for Homebrew, only while it is missing', async () => {
  const context = await setup('/discover', { brew: false });
  await startLocalState();
  try {
    const env = useEnvStore();
    // Startup reads the environment in the background.
    await vi.waitFor(() => expect(env.info).not.toBeNull());
    expect(env.hasBrew).toBe(false);
    // Installed in Terminal while OpenNavo stayed open.
    const detect = vi.spyOn(commands, 'envDetect').mockResolvedValue({
      status: 'ok',
      data: { ...env.info!, brew: { path: '/opt/homebrew/bin/brew', prefix: '/opt/homebrew', version: '4.5.0' } }
    });
    window.dispatchEvent(new Event('focus'));
    await vi.waitFor(() => expect(env.hasBrew).toBe(true));
    expect(detect).toHaveBeenCalledTimes(1);
    // Found, so nothing more to wait for.
    window.dispatchEvent(new Event('focus'));
    await flushPromises();
    expect(detect).toHaveBeenCalledTimes(1);
  } finally {
    disposePinia(context.pinia);
  }
});

it('Loading the list reads native cached footprints without scheduling per-app scans', async () => {
  const context = await setup();
  const apps = useLocalAppsStore();
  let resolveScan!: (value: Awaited<ReturnType<typeof commands.localAppFootprint>>) => void;
  const scan = vi.spyOn(commands, 'localAppFootprint').mockImplementation(
    () =>
      new Promise(resolve => {
        resolveScan = resolve;
      })
  );
  await apps.load();
  expect(scan).not.toHaveBeenCalled();
  apps.footprints = {};
  const id = apps.items[0]!.id;
  const sheet = apps.footprint(id);
  expect(scan).toHaveBeenCalledTimes(1);
  disposePinia(context.pinia);
  const result: Footprint = { appBytes: 1, dataBytes: 2, cacheBytes: 3, complete: true };
  resolveScan({ status: 'ok', data: result });
  await sheet;
  expect(apps.occupiedBytes(id)).toBeNull();
  expect(scan).toHaveBeenCalledTimes(1);
});
