import { createPublicClient } from '@opennavo/api';
import { i18n } from '@/i18n';

// Vite replaces the URL with a same-origin proxy in development; builds retain the configured backend URL (06 §2).
const baseUrl = import.meta.env.VITE_API_BASE ?? 'http://localhost:8080/api/v1';

export const api = createPublicClient({
  baseUrl,
  // Limit HTTP content reads only, without affecting long native install or catalog-sync tasks.
  fetch: async (input, init) => {
    const controller = new AbortController();
    const original = init?.signal ?? (input instanceof Request ? input.signal : undefined);
    let response: Response | undefined;
    let reader: ReadableStreamDefaultReader<Uint8Array> | undefined;
    const abort = () => controller.abort(original?.reason);
    const cancelBody = () => {
      // Cancel both tee branches together to release stalled bodies; UI loading must not wait for underlying cancellation.
      void reader?.cancel(controller.signal.reason).catch(() => undefined);
      void response?.body?.cancel(controller.signal.reason).catch(() => undefined);
    };
    let rejectAbort!: (reason: unknown) => void;
    const aborted = new Promise<never>((_resolve, reject) => {
      rejectAbort = reject;
    });
    const onAbort = () => {
      cancelBody();
      rejectAbort(controller.signal.reason);
    };
    controller.signal.addEventListener('abort', onAbort, { once: true });
    if (original?.aborted) abort();
    else original?.addEventListener('abort', abort, { once: true });
    const timer = setTimeout(() => controller.abort(new DOMException('Request timed out', 'TimeoutError')), 15_000);
    try {
      return await Promise.race([
        aborted,
        (async () => {
          if (controller.signal.aborted) throw controller.signal.reason;
          response = await fetch(input, { ...init, signal: controller.signal });
          if (controller.signal.aborted) {
            cancelBody();
            throw controller.signal.reason;
          }
          // Read the cloned body fully before openapi-fetch decodes it; preserve original status, URL, headers, and body.
          reader = response.body ? response.clone().body?.getReader() : undefined;
          try {
            if (reader)
              while (!(await reader.read()).done) {
                /* End timeout protection only after the full body is read. */
              }
            if (controller.signal.aborted) throw controller.signal.reason;
            return response;
          } finally {
            reader?.releaseLock();
          }
        })()
      ]);
    } finally {
      clearTimeout(timer);
      original?.removeEventListener('abort', abort);
      controller.signal.removeEventListener('abort', onAbort);
    }
  },
  locale: () => i18n.global.locale.value,
  platform: 'desktop',
  version: import.meta.env.VITE_APP_VERSION ?? '0.0.0',
  // Link apps mix into Discover, categories, collections and search (store/security plan §4).
  capabilities: ['link']
});
