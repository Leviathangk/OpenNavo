import { createUsageTracker } from '@opennavo/api';

/** A foreground visit qualifies after 10 seconds or a trusted user interaction.
 * A window left idle across midnight cannot silently create another active day. */
export function observeUsage(tracker: ReturnType<typeof createUsageTracker>, focused = false) {
  let dwell: ReturnType<typeof setTimeout> | undefined;
  let lastInteraction = 0;
  const visible = () => document.visibilityState === 'visible' && (!focused || document.hasFocus());
  const view = () => {
    clearTimeout(dwell);
    if (visible()) {
      lastInteraction = Date.now();
      dwell = setTimeout(() => {
        if (visible()) tracker.activity();
      }, 10_000);
    }
  };
  const interact = (event: Event) => {
    if (event.isTrusted && visible()) {
      lastInteraction = Date.now();
      tracker.activity();
    }
  };
  const online = () => {
    if (visible()) void tracker.flush();
  };
  const timer = setInterval(() => {
    if (!visible()) return;
    if (Date.now() - lastInteraction < 60_000) tracker.activity();
    else void tracker.flush();
  }, 60_000);
  document.addEventListener('visibilitychange', view);
  window.addEventListener('focus', view);
  window.addEventListener('blur', view);
  window.addEventListener('online', online);
  for (const name of ['pointerdown', 'keydown', 'scroll']) document.addEventListener(name, interact, { passive: true });
  view();
  return () => {
    clearTimeout(dwell);
    clearInterval(timer);
    document.removeEventListener('visibilitychange', view);
    window.removeEventListener('focus', view);
    window.removeEventListener('blur', view);
    window.removeEventListener('online', online);
    for (const name of ['pointerdown', 'keydown', 'scroll']) document.removeEventListener(name, interact);
    tracker.dispose();
  };
}
