// Collection update feeds (F6), registered in nuxt.config at <prefix>/collections/:slug/feed.xml for each
// language like the article feeds: the newest releases of the collection's apps.
export default defineEventHandler(event => {
  const target = feedTarget(getRequestURL(event).pathname);
  if (!target || target.section !== 'collections')
    throw createError({ statusCode: 404, statusMessage: 'Feed not found' });
  return collectionFeed(event, target.locale, target.key);
});
