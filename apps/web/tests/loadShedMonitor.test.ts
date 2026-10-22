import { afterEach, describe, expect, it, vi } from 'vitest';

const histogram = vi.hoisted(() => ({ mean: 20e6, enable: vi.fn(), disable: vi.fn(), reset: vi.fn() }));
const monitor = vi.hoisted(() => vi.fn(() => histogram));
vi.mock('node:perf_hooks', () => ({ monitorEventLoopDelay: monitor }));

import { LoadShedState } from '../server/utils/loadShed';

afterEach(() => vi.useRealTimers());

describe('event-loop delay monitor lifecycle', () => {
  it('uses a bounded observation window, recovers after a stall, and closes the timer and histogram', () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval', 'performance'] });
    const state = new LoadShedState();
    try {
      state.start();
      state.start();
      expect(monitor).toHaveBeenCalledExactlyOnceWith({ resolution: 20 });
      expect(vi.getTimerCount()).toBe(1);
      histogram.mean = 250e6;
      vi.advanceTimersByTime(500);
      expect(state.lagMs).toBe(250);
      histogram.mean = 20e6;
      vi.advanceTimersByTime(500);
      expect(state.lagMs).toBe(20);
      histogram.mean = NaN;
      vi.advanceTimersByTime(500);
      expect(state.lagMs).toBe(0);
      expect(histogram.reset).toHaveBeenCalledTimes(3);
    } finally {
      state.stop();
      state.stop();
    }
    expect(histogram.disable).toHaveBeenCalledTimes(1);
    expect(state.lagMs).toBe(0);
    expect(vi.getTimerCount()).toBe(0);
  });
});
