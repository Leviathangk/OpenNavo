import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { commands, events, unwrap } from '@/ipc/client';
import { disposeMockIpc, installMockIpc } from '@/ipc/mock';
import { setup } from './helpers';

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

describe('Mock IPC session lifecycle', () => {
  it.each([0, 30])('disposes task work after %i ms without leaving timers or restarting the queue', async elapsed => {
    const { state } = await setup('/updates', { tickMs: 10 });
    const pending = commands.taskEnqueue('update', null, {}, 'manual');
    if (elapsed) {
      await unwrap(pending);
      await vi.advanceTimersByTimeAsync(elapsed);
    }
    disposeMockIpc();
    disposeMockIpc();
    await unwrap(pending);
    expect(state.active.map(task => task.state)).toEqual([elapsed ? 'running' : 'queued']);
    expect(vi.getTimerCount()).toBe(0);
    const previous = JSON.stringify([state.active, state.history, [...state.logs]]);
    await vi.runAllTimersAsync();
    expect(JSON.stringify([state.active, state.history, [...state.logs]])).toBe(previous);
  });

  it('disposes a pending download verdict after detection has already fired', async () => {
    await setup('/discover', { tickMs: 10 });
    const detected = vi.fn();
    const verified = vi.fn();
    await events.downloadDetected.listen(detected);
    await events.downloadVerified.listen(verified);
    await unwrap(commands.linkOpen('lumen-notes', { asset: 'Lumen-2.4.1-arm64.dmg' }));
    await vi.advanceTimersByTimeAsync(20);
    expect(detected).toHaveBeenCalledOnce();
    expect(verified).not.toHaveBeenCalled();
    disposeMockIpc();
    expect(vi.getTimerCount()).toBe(0);
    await vi.runAllTimersAsync();
    expect(detected).toHaveBeenCalledOnce();
    expect(verified).not.toHaveBeenCalled();
  });

  it('does not start the old queue after IPC is replaced before its scheduling microtask', async () => {
    const { state } = await setup('/updates', { tickMs: 10 });
    const pending = commands.taskEnqueue('update', null, {}, 'manual');
    installMockIpc({ tickMs: 10 });
    await unwrap(pending);
    await vi.runAllTimersAsync();
    expect(state.active.map(task => task.state)).toEqual(['queued']);
    expect(state.logs.size).toBe(0);
  });

  it('stops running ticks and queued tasks when IPC is replaced', async () => {
    const { state } = await setup('/updates', { tickMs: 10 });
    await unwrap(commands.taskEnqueue('update', null, {}, 'manual'));
    await unwrap(commands.taskEnqueue('install', { kind: 'cask', token: 'zed' }, {}, 'bundle'));
    await vi.advanceTimersByTimeAsync(30);
    expect(state.active.map(task => task.state)).toEqual(['running', 'queued']);
    const previous = JSON.stringify([state.active, state.history, [...state.logs]]);

    installMockIpc({ tickMs: 10 });
    const updated = vi.fn();
    const logged = vi.fn();
    await events.taskUpdated.listen(updated);
    await events.taskLog.listen(logged);
    await vi.runAllTimersAsync();
    expect(updated).not.toHaveBeenCalled();
    expect(logged).not.toHaveBeenCalled();
    expect(JSON.stringify([state.active, state.history, [...state.logs]])).toBe(previous);
  });

  it('cancels delayed link download events when IPC is replaced', async () => {
    await setup('/discover', { tickMs: 10 });
    await unwrap(commands.linkOpen('lumen-notes', { asset: 'Lumen-2.4.1-arm64.dmg' }));
    installMockIpc({ tickMs: 10 });
    const detected = vi.fn();
    const verified = vi.fn();
    await events.downloadDetected.listen(detected);
    await events.downloadVerified.listen(verified);
    await vi.runAllTimersAsync();
    expect(detected).not.toHaveBeenCalled();
    expect(verified).not.toHaveBeenCalled();
  });

  it('cancels delayed permission changes when IPC is replaced', async () => {
    await setup('/discover', { tickMs: 10 });
    await unwrap(commands.permissionGuideOpen('app_management'));
    installMockIpc({ tickMs: 10 });
    const changed = vi.fn();
    await events.permissionsChanged.listen(changed);
    await vi.runAllTimersAsync();
    expect(changed).not.toHaveBeenCalled();
  });
});
