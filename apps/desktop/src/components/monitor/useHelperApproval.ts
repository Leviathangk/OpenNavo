import { onBeforeUnmount, watch } from 'vue';
import { useMonitorStore } from '@/stores/monitor';

/**
 * macOS asks for the fan helper's approval in System Settings. While that is pending, check again every couple of
 * seconds (only while this window is visible), so the view moves on without another click.
 */
export function useHelperApproval(every = 2000) {
  const store = useMonitorStore();
  let timer: ReturnType<typeof setInterval> | undefined;
  const check = () => {
    if (document.visibilityState === 'visible') void store.loadHelper().catch(() => undefined);
  };
  const stop = watch(
    () => store.helper?.status,
    status => {
      clearInterval(timer);
      timer = status === 'requires_approval' ? setInterval(check, every) : undefined;
    },
    { immediate: true }
  );
  onBeforeUnmount(() => {
    stop();
    clearInterval(timer);
  });
}
