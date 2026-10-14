import { onBeforeUnmount, watch } from 'vue';
import { useMonitorStore } from '@/stores/monitor';

/**
 * Registration and daemon startup can settle after an app update or approval in System Settings.
 * Recheck unresolved states while visible, so recovery never requires repeated clicks.
 */
export function useHelperApproval(every = 2000) {
  const store = useMonitorStore();
  let timer: ReturnType<typeof setInterval> | undefined;
  const unresolved = () => store.helper?.status !== 'enabled' && store.helper?.status !== 'unsupported';
  const check = () => {
    if (document.visibilityState === 'visible' && unresolved()) void store.loadHelper().catch(() => undefined);
  };
  const stop = watch(
    () => store.helper?.status,
    () => {
      clearInterval(timer);
      timer = unresolved() ? setInterval(check, every) : undefined;
    },
    { immediate: true }
  );
  document.addEventListener('visibilitychange', check);
  onBeforeUnmount(() => {
    stop();
    clearInterval(timer);
    document.removeEventListener('visibilitychange', check);
  });
}
