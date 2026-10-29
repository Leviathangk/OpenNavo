import { getQuery, getResponseHeader, setHeader, type H3Event } from 'h3';

function isSitemapError(event: H3Event) {
  // Nuxt's HTML error render uses an internal URL; its headers are copied to the original response.
  const path = event.path.startsWith('/__nuxt_error') ? String(getQuery(event).url ?? '') : event.path;
  return /^\/(?:__sitemap__\/|sitemap[^/]*\.xml)/.test(path);
}

function errorCacheHeaders(event: H3Event, status: number) {
  // SWR can report a background render error after the stale response has already been sent.
  if (status < 400 || event.node.res.headersSent) return;
  for (const name of ['Cache-Control', 'CDN-Cache-Control', 'Cloudflare-CDN-Cache-Control'])
    setHeader(event, name, 'no-store');
  if (status === 503 && !getResponseHeader(event, 'retry-after')) {
    setHeader(event, 'retry-after', isSitemapError(event) ? 120 : 5);
  }
}

export default defineNitroPlugin(nitro => {
  // H3 can send thrown JSON errors directly, bypassing beforeResponse. Set the headers before
  // its error handler sends them, and also cover responses whose status was set while rendering.
  nitro.hooks.hook('error', (error, { event }) => {
    const status = (error as { statusCode?: number }).statusCode;
    if (event && typeof status === 'number') errorCacheHeaders(event, status);
  });
  nitro.hooks.hook('beforeResponse', event => errorCacheHeaders(event, event.node.res.statusCode));
});
