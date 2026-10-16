import { canonicalRequestUrl } from '../utils/canonicalUrl';

// One 301 to the URL a page canonicalizes to (server/utils/canonicalUrl.ts): /Apps/x, /apps/x/,
// /zh/, /en/download and /apps/plus-app%2B%2B all reach their page in a single hop, with the query
// kept. Assets, payloads, the API, sitemaps, robots.txt and other files are left alone.
export default defineEventHandler(event => {
  if (event.method !== 'GET' && event.method !== 'HEAD') return;
  // H3 has decoded event.path already, but Nitro's SWR wrapper restores the raw request URL.
  const target = canonicalRequestUrl(event.node.req.url ?? event.path);
  if (target) return sendRedirect(event, target, 301);
});
