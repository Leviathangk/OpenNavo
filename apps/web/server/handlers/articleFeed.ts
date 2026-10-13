// Article feeds (WP17), registered in nuxt.config at <prefix>/articles/feed.xml for each language:
// fixed paths, so they win over the cached article page routes that share the prefix.
export default defineEventHandler(event => {
  const path = getRequestURL(event).pathname.replace(/\/articles\/feed\.xml$/, '');
  const locale = feedLocale(path.slice(1) || undefined);
  if (!locale) throw createError({ statusCode: 404, statusMessage: 'Feed not found' });
  return articleFeed(event, locale);
});
