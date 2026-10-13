// Pages read the admin SEO settings from the request context (app/plugins/site-settings.ts), so
// rendering never waits on a separate API call. Settings are site-wide, which keeps them safe to
// carry into shared SWR page caches.
export default defineEventHandler(async event => {
  const path = event.path;
  if (path.startsWith('/_') || path.startsWith('/api/') || path.endsWith('.txt')) return;
  event.context.siteSettings = await getSiteSettings();
});
