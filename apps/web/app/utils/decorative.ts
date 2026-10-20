/**
 * Decorative data (S7): sections a page can do without, such as orbit icons or filter counts. Their
 * failure degrades that section to `fallback` and is logged on the server; it never becomes a captured
 * useAsyncData error, so plugins/ssr-errors.server.ts keeps the page's own status. Data the page cannot
 * exist without stays undecorated and still answers 503.
 */
export function decorative<T, F>(request: Promise<T>, fallback: F, label: string): Promise<T | F> {
  return request.catch((reason: unknown) => {
    if (import.meta.server) console.warn(`[decorative] ${label}:`, reason instanceof Error ? reason.message : reason);
    return fallback;
  });
}
