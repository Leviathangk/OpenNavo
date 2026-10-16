import type { H3Event } from 'h3';
import { GONE_STATUSES, routeCacheKey } from '../utils/pageCache';

// Nitro's SWR route cache never stores an error response, yet it keeps serving a URL's last 200
// while every background revalidation fails: a list page past its new end, or an app that is gone,
// would stay a cached 200 until the entry's storage TTL (an hour in Redis, for ever in memory). When
// a cached route now answers 404 or 410, its entry is dropped so the next request gets the real
// status. Thrown errors (useListPage, unknown apps) arrive through the error hook, rendered 410
// pages through render:response; outages (5xx) keep the cached page.
export default defineNitroPlugin(nitroApp => {
  const drop = async (event: H3Event | undefined, status: unknown) => {
    if (!event || typeof status !== 'number' || !GONE_STATUSES.has(status)) return;
    const key = await routeCacheKey(event);
    if (key) await useStorage('cache').removeItem(key);
  };
  const report = (error: unknown) => console.error('[opennavo] could not drop a cached error page', error);
  nitroApp.hooks.hook('error', (error, { event }) =>
    drop(event, (error as { statusCode?: unknown }).statusCode).catch(report)
  );
  nitroApp.hooks.hook('render:response', (response, { event }) => drop(event, response.statusCode).catch(report));
});
