import { computed, onBeforeUnmount, onMounted, watch } from 'vue';
import { useRefreshOnReturn } from '@/composables/useRefreshOnReturn';
import { useSecurityStore } from '@/stores/security';

/**
 * Accessibility is granted in System Settings, and macOS does not tell OpenNavo when it changes. Read it when the page
 * opens, when the switch changes and when the window comes back, and keep asking while the switch is on but access is
 * missing, so the button that sends the user there goes away as soon as it is allowed. Returns whether it should show.
 */
export function usePasteAccess(every = 2000) {
  const store = useSecurityStore();
  let timer: ReturnType<typeof setInterval> | undefined;
  const needsAccess = computed(
    () => store.securitySettings.pasteProtection && store.pasteStatus?.accessibilityGranted === false
  );
  const load = () => void store.loadPasteStatus().catch(() => undefined);
  const check = () => {
    if (document.visibilityState === 'visible') load();
  };
  watch(
    needsAccess,
    waiting => {
      clearInterval(timer);
      timer = waiting ? setInterval(check, every) : undefined;
    },
    { immediate: true }
  );
  watch(() => store.securitySettings.pasteProtection, load);
  onMounted(load);
  useRefreshOnReturn(load);
  onBeforeUnmount(() => clearInterval(timer));
  return needsAccess;
}
