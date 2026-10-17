import { ref } from 'vue';
import { defineStore } from 'pinia';
import type { OutdatedItem } from '@/ipc/bindings';

export interface MajorRequest {
  /** The major updates in this request. */
  majors: readonly OutdatedItem[];
  /** Whether other updates go ahead whatever the answer (Update All). */
  others: boolean;
}

/**
 * A major update can change an app's interface, ask for permissions again or drop old file formats, so it is
 * confirmed once before it is queued (store/security plan §7). Declining skips only the major updates.
 */
export const useMajorConfirmStore = defineStore('major-confirm', () => {
  const pending = ref<MajorRequest | null>(null);
  let resolve: ((confirmed: boolean) => void) | undefined;
  function ask(request: MajorRequest): Promise<boolean> {
    resolve?.(false);
    pending.value = request;
    return new Promise(done => {
      resolve = done;
    });
  }
  function choose(confirmed: boolean) {
    const done = resolve;
    resolve = undefined;
    pending.value = null;
    done?.(confirmed);
  }
  return { pending, ask, choose };
});
