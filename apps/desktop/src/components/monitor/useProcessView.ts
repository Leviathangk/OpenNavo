import { onBeforeUnmount, onMounted, ref } from 'vue';

/** One entry per process, or processes merged by app (a browser's helpers) or executable (twelve `compile`s). */
export type ProcessView = 'process' | 'app';

const KEY = 'opennavo.monitor.processView';
const read = (): ProcessView => {
  try {
    return localStorage.getItem(KEY) === 'app' ? 'app' : 'process';
  } catch {
    return 'process';
  }
};

const view = ref<ProcessView>(read());

/**
 * The choice is shared by every list in the main window and the menu bar popover. They are separate windows of one app,
 * so storage events carry it across; the window that makes the change updates itself.
 */
export function useProcessView() {
  const onStorage = (event: StorageEvent) => {
    if (event.key === KEY) view.value = read();
  };
  onMounted(() => {
    view.value = read();
    window.addEventListener('storage', onStorage);
  });
  onBeforeUnmount(() => window.removeEventListener('storage', onStorage));
  function set(next: ProcessView) {
    view.value = next;
    try {
      localStorage.setItem(KEY, next);
    } catch {
      // Not saved: it still holds for this window until it closes.
    }
  }
  return { view, set };
}
