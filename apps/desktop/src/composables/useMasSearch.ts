import { onScopeDispose, ref, shallowRef, watch } from 'vue';
import type { Ref } from 'vue';
import { IpcError } from '@/ipc/client';
import type { MasApp } from '@/ipc/bindings';
import { useMasStore } from '@/stores/mas';

/** Wait before asking Apple: the iTunes API allows about 20 calls a minute, so every keystroke must not cost one. */
export const MAS_SEARCH_DEBOUNCE_MS = 400;
const MIN_LENGTH = 2;

export interface MasSearchState {
  /** Results for `shownQuery`; the previous query's results stay on screen until the next search lands. */
  results: Ref<MasApp[] | null>;
  shownQuery: Ref<string>;
  loading: Ref<boolean>;
  /** E_* code of the last failure for the current query, e.g. E_MAS_RATE_LIMITED or E_NETWORK. */
  error: Ref<string | null>;
}

/** App Store search beside the local catalog search (store/security plan §5): debounced, cached by the store. */
export function useMasSearch(query: Ref<string>, enabled: Ref<boolean>, limit = 20): MasSearchState {
  const mas = useMasStore();
  const results = shallowRef<MasApp[] | null>(null);
  const shownQuery = ref('');
  const loading = ref(false);
  const error = ref<string | null>(null);
  let timer: ReturnType<typeof setTimeout> | undefined;
  let sequence = 0;

  async function run(q: string) {
    const current = ++sequence;
    loading.value = true;
    try {
      const items = await mas.search(q, limit);
      if (current !== sequence) return;
      results.value = items;
      shownQuery.value = q;
      error.value = null;
    } catch (cause) {
      if (current !== sequence) return;
      error.value = cause instanceof IpcError ? cause.code : 'E_UNKNOWN';
    } finally {
      if (current === sequence) loading.value = false;
    }
  }

  watch(
    [query, enabled],
    ([value, on]) => {
      clearTimeout(timer);
      const q = value.trim();
      if (!on || q.length < MIN_LENGTH) {
        sequence++;
        results.value = null;
        shownQuery.value = '';
        loading.value = false;
        error.value = null;
        return;
      }
      timer = setTimeout(() => void run(q), MAS_SEARCH_DEBOUNCE_MS);
    },
    { immediate: true }
  );

  onScopeDispose(() => clearTimeout(timer));
  return { results, shownQuery, loading, error };
}
