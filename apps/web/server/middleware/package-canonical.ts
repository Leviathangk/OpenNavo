import { brandDownloadPath } from '../../app/utils/packageView';
import { canonicalRequestUrl } from '../utils/canonicalUrl';

// One 301 to the URL a page canonicalizes to (server/utils/canonicalUrl.ts): /Apps/x, /apps/x/,
// /zh/, /en/download and /apps/plus-app%2B%2B all reach their page in a single hop, with the query
// kept. OpenNavo's own overview, in any of those spellings, goes to its language's /download in that
// same hop: the desktop app's page is /download, and the overview would only compete with it and
// describe the app twice. Its tabs stay. Assets, payloads, the API, sitemaps, robots.txt and other
// files are left alone.
export default defineEventHandler(event => {
  if (event.method !== 'GET' && event.method !== 'HEAD') return;
  // H3 has decoded event.path already, but Nitro's SWR wrapper restores the raw request URL.
  const url = event.node.req.url ?? event.path;
  const target = canonicalRequestUrl(url);
  const page = target ?? url;
  const queryAt = page.indexOf('?');
  const download = brandDownloadPath(queryAt < 0 ? page : page.slice(0, queryAt));
  if (download) return sendRedirect(event, queryAt < 0 ? download : download + page.slice(queryAt), 301);
  if (target) return sendRedirect(event, target, 301);
});
