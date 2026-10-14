/** Only accept the recent-app list and its pagination, never arbitrary return URLs. */
export function recentReturnPath(value: unknown): string | undefined {
  if (typeof value !== 'string' || !/^\/recent(?:\?|$)/.test(value)) return undefined;
  const url = new URL(value, 'https://example.invalid');
  if (url.pathname !== '/recent' || url.hash) return undefined;
  const query = new URLSearchParams();
  for (const key of ['page', 'size']) {
    const raw = url.searchParams.get(key);
    if (!raw || !/^\d{1,6}$/.test(raw)) continue;
    const number = Number(raw);
    if (number > 0 && (key !== 'size' || number <= 100)) query.set(key, String(number));
  }
  return `/recent${query.size ? `?${query}` : ''}`;
}

export function withRecentReturn(path: string, value: unknown): string {
  const target = recentReturnPath(value);
  return target ? `${path}${path.includes('?') ? '&' : '?'}returnTo=${encodeURIComponent(target)}` : path;
}
