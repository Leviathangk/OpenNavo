import { ref } from 'vue';
import { defineStore } from 'pinia';
import type { OutdatedItem } from '@/ipc/bindings';

export const useUnsignedUpdateConfirmStore = defineStore('unsigned-update-confirm', () => {
  const pending = ref<readonly OutdatedItem[] | null>(null);
  let resolve: ((confirmed: boolean) => void) | undefined;
  function ask(items: readonly OutdatedItem[]): Promise<boolean> {
    resolve?.(false);
    pending.value = items;
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
