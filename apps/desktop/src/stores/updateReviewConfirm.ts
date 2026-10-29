import { ref } from 'vue';
import { defineStore } from 'pinia';
import type { OutdatedItem, UpdateConsent } from '@/ipc/bindings';

export const useUpdateReviewConfirmStore = defineStore('update-review-confirm', () => {
  const pending = ref<{ item: OutdatedItem; consent: UpdateConsent } | null>(null);
  let resolve: ((accepted: boolean) => void) | undefined;
  function ask(item: OutdatedItem, consent: UpdateConsent) {
    resolve?.(false);
    pending.value = { item, consent };
    return new Promise<boolean>(done => {
      resolve = done;
    });
  }
  function choose(accepted: boolean) {
    const done = resolve;
    resolve = undefined;
    pending.value = null;
    done?.(accepted);
  }
  return { pending, ask, choose };
});
