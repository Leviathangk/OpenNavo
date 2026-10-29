/** These server routes, like the page routes, exist at every site locale prefix. */
export const LOCALIZED_FEEDS = [
  { route: '/articles/feed.xml', handler: '~~/server/handlers/articleFeed.ts' },
  { route: '/apps/:token/feed.xml', handler: '~~/server/handlers/packageFeed.ts' },
  { route: '/collections/:slug/feed.xml', handler: '~~/server/handlers/collectionFeed.ts' }
] as const;
