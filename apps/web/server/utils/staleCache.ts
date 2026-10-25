export interface StaleCache<T> {
  (): Promise<T>;
  /**
   * The value without waiting: undefined until the first load has finished. Starts the load or a due refresh
   * behind the caller, as a call would.
   */
  peek(): T | undefined;
  /** Settles once no refresh is running (tests). */
  idle(): Promise<void>;
}

/**
 * One value per process, kept fresh for freshMs and then served stale while a single refresh runs
 * behind the response. Only the very first load waits, and only it can fail the caller: later
 * failures keep the previous value and wait retryMs before trying again.
 */
export function createStaleCache<T>(
  load: () => Promise<T>,
  options: { freshMs: number; retryMs: number; onError?: (error: unknown) => void; now?: () => number }
): StaleCache<T> {
  const now = options.now ?? Date.now;
  let value: { data: T } | undefined;
  let expires = 0;
  let pending: Promise<T> | undefined;
  const refresh = () =>
    (pending ??= load()
      .then(
        data => {
          value = { data };
          expires = now() + options.freshMs;
          return data;
        },
        (error: unknown) => {
          if (!value) throw error;
          expires = now() + options.retryMs;
          options.onError?.(error);
          return value.data;
        }
      )
      .finally(() => {
        pending = undefined;
      }));
  const get = async (): Promise<T> => {
    if (value && expires > now()) return value.data;
    if (!value) return refresh();
    void refresh();
    return value.data;
  };
  const peek = (): T | undefined => {
    // A first load that fails has no previous value to fall back on, so report it here instead of to a caller.
    if (!value) void refresh().catch((error: unknown) => options.onError?.(error));
    else if (expires <= now()) void refresh();
    return value?.data;
  };
  return Object.assign(get, {
    peek,
    idle: async () => {
      await pending?.catch(() => undefined);
    }
  });
}
