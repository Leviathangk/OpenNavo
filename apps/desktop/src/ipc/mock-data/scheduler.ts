/** Cancellable timers used by the browser IPC mocks. */
export interface MockScheduler {
  later(run: () => void, ms: number): void;
  repeat(run: () => void, ms: number): ReturnType<typeof setInterval> | undefined;
  cancelRepeat(timer: ReturnType<typeof setInterval>): void;
  delay(ms: number): Promise<boolean>;
  stop(): void;
}

export function createMockScheduler(): MockScheduler {
  let stopped = false;
  const timeouts = new Set<ReturnType<typeof setTimeout>>();
  const intervals = new Set<ReturnType<typeof setInterval>>();
  const waiters = new Map<ReturnType<typeof setTimeout>, (active: boolean) => void>();

  const later = (run: () => void, ms: number) => {
    if (stopped) return;
    let timer!: ReturnType<typeof setTimeout>;
    timer = setTimeout(() => {
      timeouts.delete(timer);
      if (!stopped) run();
    }, ms);
    timeouts.add(timer);
  };

  const repeat = (run: () => void, ms: number) => {
    if (stopped) return undefined;
    const timer = setInterval(() => {
      if (!stopped) run();
    }, ms);
    intervals.add(timer);
    return timer;
  };

  const cancelRepeat = (timer: ReturnType<typeof setInterval>) => {
    clearInterval(timer);
    intervals.delete(timer);
  };

  const delay = (ms: number) =>
    new Promise<boolean>(resolve => {
      if (stopped) {
        resolve(false);
        return;
      }
      let timer!: ReturnType<typeof setTimeout>;
      const finish = (active: boolean) => {
        if (!waiters.has(timer)) return;
        waiters.delete(timer);
        timeouts.delete(timer);
        resolve(active);
      };
      timer = setTimeout(() => finish(!stopped), ms);
      timeouts.add(timer);
      waiters.set(timer, finish);
    });

  const stop = () => {
    if (stopped) return;
    stopped = true;
    for (const timer of timeouts) clearTimeout(timer);
    for (const timer of intervals) clearInterval(timer);
    timeouts.clear();
    intervals.clear();
    for (const finish of waiters.values()) finish(false);
    waiters.clear();
  };

  return { later, repeat, cancelRepeat, delay, stop };
}
