import { ref } from 'vue';
import { defineStore } from 'pinia';

export interface TapConsent {
  name: string;
  tap: string;
}

/**
 * Casks from a third-party tap are not reviewed by Homebrew, and installing one makes brew clone and trust
 * that tap's cask. Each install needs separate consent; closing or unmounting the dialog never counts as consent.
 */
export const useTapConfirmStore = defineStore('tap-confirm', () => {
  const pending = ref<TapConsent | null>(null);
  let resolve: ((confirmed: boolean) => void) | undefined;
  function ask(request: TapConsent): Promise<boolean> {
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
