import { describe, expect, it } from 'vitest';
import { InFlightCounter, localReadinessStatus, readCgroupMemoryLimit } from '../server/utils/internalHealth';

describe('internal web health', () => {
  it('reads cgroup v2 and falls back to v1, while ignoring an unlimited limit', () => {
    const v2 = readCgroupMemoryLimit(path => (path.endsWith('memory.max') ? 'max' : '2097152'));
    expect(v2).toBe(2_097_152);
    expect(readCgroupMemoryLimit(() => 'max')).toBeUndefined();
  });

  it('skips RSS checks without a cgroup limit and keeps local readiness independent of the API', () => {
    expect(localReadinessStatus({ rss: 10_000, heapUsed: 100, heapLimit: 1_000, lagMs: 0 })).toBe(200);
    expect(localReadinessStatus({ rss: 900, heapUsed: 100, heapLimit: 1_000, lagMs: 0, cgroupLimit: 1_000 })).toBe(503);
    expect(localReadinessStatus({ rss: 100, heapUsed: 900, heapLimit: 1_000, lagMs: 0 })).toBe(503);
    expect(localReadinessStatus({ rss: 100, heapUsed: 100, heapLimit: 1_000, lagMs: 251 })).toBe(503);
  });

  it('releases in-flight public requests once even when close and finish both fire', () => {
    const counter = new InFlightCounter();
    const release = counter.acquire();
    expect(counter.value).toBe(1);
    release();
    release();
    expect(counter.value).toBe(0);
  });
});
