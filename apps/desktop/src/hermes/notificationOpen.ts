import { ref, watch } from 'vue';

export interface HermesNotificationOpen {
  conversationId: string;
}

const pending = ref<HermesNotificationOpen | null>(null);

/** Queue an open request until the assistant page can load its history and select the row. */
export function queueHermesNotificationOpen(open: HermesNotificationOpen) {
  pending.value = open;
}

/** Consume a request that arrived before the page mounted. */
export function takeHermesNotificationOpen(): HermesNotificationOpen | null {
  const value = pending.value;
  pending.value = null;
  return value;
}

/** Consume requests that arrive while the page is mounted. */
export function watchHermesNotificationOpen(handler: (open: HermesNotificationOpen) => void): () => void {
  return watch(pending, value => {
    if (!value) return;
    pending.value = null;
    handler(value);
  });
}
