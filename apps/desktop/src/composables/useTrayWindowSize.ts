import { onBeforeUnmount, onMounted } from 'vue';
import type { Ref } from 'vue';
import { isTauri } from '@tauri-apps/api/core';
import { getCurrentWindow, LogicalSize } from '@tauri-apps/api/window';

/** Size the tray window from content, not viewport height, or empty states will always fill the window. */
export function useTrayWindowSize(content: Ref<HTMLElement | null>) {
  let observer: ResizeObserver | undefined;

  onMounted(() => {
    if (!isTauri() || !content.value) return;
    const window = getCurrentWindow();
    // The updates popover and the shared monitoring popover both size themselves to content.
    if (window.label !== 'tray' && window.label !== 'tray-monitor') return;
    let previous = '';
    observer = new ResizeObserver(() => {
      if (!content.value) return;
      const bounds = content.value.getBoundingClientRect();
      const width = Math.ceil(bounds.width);
      const height = Math.ceil(bounds.height);
      const key = `${width}/${height}`;
      if (!width || !height || key === previous) return;
      previous = key;
      // DOM dimensions are logical pixels; do not multiply by the Retina scale factor again.
      void window.setSize(new LogicalSize(width, height)).catch(error => {
        previous = '';
        console.error('Failed to resize the menu-bar window', error);
      });
    });
    observer.observe(content.value);
  });

  onBeforeUnmount(() => observer?.disconnect());
}
