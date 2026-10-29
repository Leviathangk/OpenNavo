/** Drops @nuxtjs/sitemap's ten-minute render and response caches after settings change. */
export async function clearSitemapStorage(storage = useStorage('sitemap')) {
  const keys = await storage.getKeys();
  await Promise.all(keys.map(key => storage.removeItem(key)));
}
