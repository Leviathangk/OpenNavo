// IndexNow ownership file (WP4/WP24): /{key}.txt answers with the key configured in admin. The key
// never appears in public settings; the API only confirms an exact match, so other names fall
// through to the normal 404 page.
const KEY_FILE = /^\/([A-Za-z0-9-]{8,128})\.txt$/;

export default defineEventHandler(async event => {
  const key = KEY_FILE.exec(event.path.split('?', 1)[0] ?? '')?.[1];
  if (!key || (event.method !== 'GET' && event.method !== 'HEAD')) return;
  const config = useRuntimeConfig(event);
  const base = (config.apiBaseInternal || config.public.apiBase).replace(/\/$/, '');
  const clientIP = event.context.publicApiClientIP;
  try {
    // Forward the visitor address so probing counts against the prober's rate limit, not the site's.
    const response = await $fetch<{ data: { key: string } }>(`${base}/site/indexnow-keys/${key}`, {
      timeout: 3000,
      headers: typeof clientIP === 'string' ? { 'X-Forwarded-For': clientIP } : {}
    });
    if (response?.data?.key !== key) return;
  } catch {
    return;
  }
  setHeader(event, 'Content-Type', 'text/plain; charset=utf-8');
  setHeader(event, 'Cache-Control', 'public, max-age=300');
  return key;
});
