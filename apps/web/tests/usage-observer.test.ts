// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { createUsageTracker } from '@opennavo/api';
import { observeUsage } from '../app/utils/usage';

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  localStorage.clear();
});
it('only counts a visible dwell, stops when hidden, and never counts an idle cross-day tab', async () => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date('2026-10-22T12:00:00Z'));
  const visibility = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden');
  const send = vi.fn().mockImplementation(async () => new Date(Date.now() + 8 * 3_600_000).toISOString().slice(0, 10));
  const tracker = createUsageTracker({
    platform: 'web',
    version: () => '',
    locale: () => 'en-US',
    storage: localStorage,
    withLock: async (_name, action) => {
      action();
    },
    send
  });
  tracker.setEnabled(true);
  const stop = observeUsage(tracker);
  await vi.advanceTimersByTimeAsync(60_000);
  expect(send).not.toHaveBeenCalled();
  visibility.mockReturnValue('visible');
  document.dispatchEvent(new Event('visibilitychange'));
  await vi.advanceTimersByTimeAsync(9999);
  expect(send).not.toHaveBeenCalled();
  await vi.advanceTimersByTimeAsync(1);
  expect(send).toHaveBeenCalledTimes(1);
  await vi.advanceTimersByTimeAsync(24 * 3_600_000);
  expect(send).toHaveBeenCalledTimes(1);
  window.dispatchEvent(new Event('focus'));
  await vi.advanceTimersByTimeAsync(10_000);
  expect(send).toHaveBeenCalledTimes(2);
  stop();
  window.dispatchEvent(new Event('focus'));
  await vi.advanceTimersByTimeAsync(86_400_000);
  expect(send).toHaveBeenCalledTimes(2);
});
