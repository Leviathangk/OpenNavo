// App update feeds (F6), registered in nuxt.config at <prefix>/apps/:token/feed.xml for each language like
// the article feeds: fixed paths, so they win over the cached app page routes that share the prefix.
export default defineEventHandler(event => {
  const target = feedTarget(getRequestURL(event).pathname);
  if (!target || target.section !== 'apps') throw createError({ statusCode: 404, statusMessage: 'Feed not found' });
  return packageFeed(event, target.locale, target.key);
});
