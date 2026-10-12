import { onScopeDispose, ref, shallowRef, watch } from 'vue';
import type { WatchSource } from 'vue';
import { registerRefresh } from './usePageRefresh';

/** Keep content when rereading the same object; clear it when identity changes. Late responses and unmounted pages cannot write data. */
export function useLoader<T>(
  fetcher: () => Promise<T>,
  sources: WatchSource[] = [],
  onAccepted?: (value: T) => void,
  identitySources: WatchSource[] = sources,
  initialData?: () => T | undefined
) {
  const data = shallowRef<T | undefined>(initialData?.());
  const error = shallowRef<unknown>();
  const loading = ref(false);
  const updatedAt = ref(0);
  const sequence = ref(0);
  let pending: Promise<void> | undefined;
  let disposed = false;

  function load(): Promise<void> {
    if (disposed) return Promise.resolve();
    if (pending) return pending;
    const current = ++sequence.value;
    loading.value = true;
    error.value = undefined;
    let request: Promise<T>;
    try {
      request = fetcher();
    } catch (cause) {
      request = Promise.reject(cause);
    }
    pending = request
      .then(value => {
        if (current !== sequence.value || disposed) return;
        onAccepted?.(value);
        if (current === sequence.value) {
          data.value = value;
          updatedAt.value = Date.now();
        }
      })
      .catch((cause: unknown) => {
        if (current === sequence.value && !disposed) error.value = cause;
      })
      .finally(() => {
        if (current === sequence.value) {
          loading.value = false;
          pending = undefined;
        }
      });
    return pending;
  }

  const resetRefresh = registerRefresh({
    reload: load,
    loading,
    error,
    updatedAt,
    generation: sequence
  });
  watch(
    identitySources,
    () => {
      sequence.value += 1;
      pending = undefined;
      data.value = initialData?.();
      updatedAt.value = 0;
    },
    { flush: 'sync' }
  );
  watch(
    sources,
    () => {
      // Nonidentity dependencies such as catalog revisions must also invalidate in-flight results.
      resetRefresh();
      sequence.value += 1;
      pending = undefined;
      void load();
    },
    { immediate: true, flush: 'sync' }
  );
  onScopeDispose(() => {
    disposed = true;
    sequence.value += 1;
  });
  return { data, error, loading, updatedAt, reload: load };
}
