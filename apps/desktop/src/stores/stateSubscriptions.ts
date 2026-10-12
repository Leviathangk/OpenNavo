import { defineStore } from 'pinia';
import { onScopeDispose } from 'vue';

type Cleanup = () => void;
type Register = (cleanup: Cleanup | Promise<Cleanup>) => Promise<void>;

/** Window subscriptions belong to its Pinia lifetime, including partial startup. */
export const useStateSubscriptions = defineStore('stateSubscriptions', () => {
  const entries = new Map<string, { ready: Promise<void>; stop: Cleanup }>();

  function stopAll() {
    for (const entry of entries.values()) entry.stop();
    entries.clear();
  }
  onScopeDispose(stopAll);

  function start(key: string, setup: (register: Register) => Promise<void>): Promise<void> {
    const existing = entries.get(key);
    if (existing) return existing.ready;
    let stopped = false;
    const cleanups: Cleanup[] = [];
    const entry = {
      ready: Promise.resolve(),
      stop() {
        stopped = true;
        for (const cleanup of cleanups.splice(0)) cleanup();
      }
    };
    const register: Register = async cleanup => {
      const stop = typeof cleanup === 'function' ? cleanup : await cleanup;
      if (stopped) stop();
      else cleanups.push(stop);
    };
    entries.set(key, entry);
    entry.ready = setup(register).catch(error => {
      entry.stop();
      if (entries.get(key) === entry) entries.delete(key);
      throw error;
    });
    return entry.ready;
  }

  return { start, stopAll };
});
