// Applies the admin edge-cache switch to every HTML and RSS response (server/utils/edgeCache.ts).
export default defineNitroPlugin(nitroApp => {
  nitroApp.hooks.hook('beforeResponse', async event => {
    const response = event.node.res;
    const contentType = String(response.getHeader('content-type') ?? '');
    if (!edgeCacheable(contentType)) return;
    const settings =
      (event.context.siteSettings as Awaited<ReturnType<typeof getSiteSettings>>) ?? (await getSiteSettings());
    const value = edgeCacheHeader({
      enabled: settings.edgeCache.enabled,
      method: event.method,
      path: event.path,
      status: response.statusCode,
      contentType,
      cacheControl: String(response.getHeader('cache-control') ?? ''),
      setCookie: response.hasHeader('set-cookie'),
      authorization: Boolean(getHeader(event, 'authorization'))
    });
    if (value) setHeader(event, 'Cloudflare-CDN-Cache-Control', value);
  });
});
