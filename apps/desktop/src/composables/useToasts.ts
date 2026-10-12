import { createToastQueue } from '@opennavo/ui';
import type { OnToastInput, ToastQueue } from '@opennavo/ui';

// The client has one window instance and a global toast queue, rendered by the shell's OnToastRegion.
const queue = createToastQueue();
const actions = new Map<string, () => void>();

export interface DesktopToasts extends ToastQueue {
  /** Toast with an action button such as Undo; the handler runs at most once and closes the toast. */
  pushAction: (toast: OnToastInput & { actionLabel: string }, handler: () => void) => string;
  /** OnToastRegion action event. */
  runAction: (id: string) => void;
}

function dismiss(id: string) {
  actions.delete(id);
  queue.dismiss(id);
}

const toasts: DesktopToasts = {
  ...queue,
  dismiss,
  clear() {
    actions.clear();
    queue.clear();
  },
  push(toast) {
    if (toast.id !== undefined) actions.delete(toast.id);
    return queue.push(toast);
  },
  pushAction(toast, handler) {
    const id = queue.push(toast);
    actions.set(id, handler);
    return id;
  },
  runAction(id) {
    const handler = actions.get(id);
    dismiss(id);
    handler?.();
  }
};

export function useToasts(): DesktopToasts {
  return toasts;
}
