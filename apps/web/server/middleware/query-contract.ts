import { cacheQuery } from '../../shared/utils/routeQuery';
import { isPagePath } from '../utils/canonicalUrl';

// The route query contract (S4) for rendering and the route cache: a page request keeps only the
// parameters its route's `cache` list names, so ?utm_source=…, ?fbclid=… and other strays neither
// split Nitro's SWR cache nor reach the page. Runs after package-canonical.ts has answered any 301.
// Nitro keys route caches on originalUrl, then url, then event.path, so all three are rewritten.
export default defineEventHandler(event => {
  if (event.method !== 'GET' && event.method !== 'HEAD') return;
  const url = event.node.req.url ?? event.path;
  const at = url.indexOf('?');
  if (at < 0) return;
  const path = url.slice(0, at);
  if (!isPagePath(path)) return;
  const query = url.slice(at + 1);
  const kept = cacheQuery(path, query);
  if (kept === query) return;
  const next = kept ? `${path}?${kept}` : path;
  const request = event.node.req as typeof event.node.req & { originalUrl?: string };
  if (request.originalUrl) request.originalUrl = next;
  request.url = next;
  // eslint-disable-next-line no-underscore-dangle -- h3 routes on _path, so the cleaned URL has to go there too
  event._path = next;
});
