import { onBeforeUnmount, onMounted, watch } from 'vue';
import { useAppUpdaterStore } from '@/stores/appUpdater';
import { useSettingsStore } from '@/stores/settings';

/** Only the main app shell schedules self-update checks; tray/permission/onboarding windows do not. */
export function useAppUpdateChecks() {
  const updater = useAppUpdaterStore();
  const settings = useSettingsStore();
  let ready = false;
  let startup: ReturnType<typeof setTimeout> | undefined;
  let interval: ReturnType<typeof setInterval> | undefined;
  const check = () => {
    if (ready) void updater.checkNow(true);
  };

  onMounted(() => {
    if (updater.preview) {
      void updater.showPreview().catch((error: unknown) => {
        console.error('Could not open the development update preview', error);
      });
      return;
    }
    startup = setTimeout(() => {
      ready = true;
      check();
      interval = setInterval(check, 3_600_000);
    }, 30_000);
    window.addEventListener('focus', check);
    window.addEventListener('online', check);
    document.addEventListener('visibilitychange', check);
  });

  watch(
    () => settings.value?.autoCheckAppUpdates,
    enabled => {
      if (enabled) check();
    }
  );

  onBeforeUnmount(() => {
    clearTimeout(startup);
    clearInterval(interval);
    window.removeEventListener('focus', check);
    window.removeEventListener('online', check);
    document.removeEventListener('visibilitychange', check);
  });
}
