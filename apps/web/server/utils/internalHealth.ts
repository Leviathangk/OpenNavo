import { readFileSync } from 'node:fs';

const CGROUP_MEMORY_FILES = ['/sys/fs/cgroup/memory.max', '/sys/fs/cgroup/memory/memory.limit_in_bytes'];
const UNLIMITED_MEMORY = 2 ** 60;

type ReadText = (path: string) => string;

/** Read the container memory limit from cgroup v2, then the v1 fallback. */
export function readCgroupMemoryLimit(read: ReadText = path => readFileSync(path, 'utf8')): number | undefined {
  for (const path of CGROUP_MEMORY_FILES) {
    try {
      const value = read(path).trim();
      if (!value || value === 'max') continue;
      const limit = Number(value);
      if (Number.isFinite(limit) && limit > 0 && limit < UNLIMITED_MEMORY) return limit;
    } catch {
      // A host may expose neither cgroup version to the process. RSS checks are skipped then.
    }
  }
  return undefined;
}

export type ReadinessInput = {
  rss: number;
  heapUsed: number;
  heapLimit: number;
  lagMs: number;
  cgroupLimit?: number;
};

/** Count public request lifetimes; the internal metrics server never calls acquire(). */
export class InFlightCounter {
  private current = 0;

  get value() {
    return this.current;
  }

  acquire(): () => void {
    this.current += 1;
    let released = false;
    return () => {
      if (released) return;
      released = true;
      this.current = Math.max(0, this.current - 1);
    };
  }
}

/** Return the local readiness status without contacting another service. */
export function localReadinessStatus(input: ReadinessInput): 200 | 503 {
  const rssOverBudget = input.cgroupLimit !== undefined && input.rss > input.cgroupLimit * 0.85;
  const heapOverBudget = input.heapLimit > 0 && input.heapUsed > input.heapLimit * 0.85;
  return rssOverBudget || heapOverBudget || input.lagMs > 250 ? 503 : 200;
}
