import { createError, defineEventHandler, setHeader } from 'h3';
import { getSiteSettings } from '../utils/siteSettings';

const DISABLED_BUCKET = /^\/__sitemap__\/(fonts|versions)-bucket-\d+\.xml$/;
const UNCACHEABLE = ['Cache-Control', 'CDN-Cache-Control', 'Cloudflare-CDN-Cache-Control'] as const;

// Run before the sitemap response cache, including cached XML from an earlier enabled configuration.
export default defineEventHandler(async event => {
  const group = DISABLED_BUCKET.exec(event.path.split('?', 1)[0] ?? '')?.[1];
  if (!group) return;

  const enabled =
    group === 'fonts'
      ? (await getSiteSettings()).pages.fontsSitemap
      : (await getSiteSettings()).pages.versionsSitemap;
  if (enabled) return;

  // H3 may send a thrown error directly without running Nitro's beforeResponse hooks.
  for (const header of UNCACHEABLE) setHeader(event, header, 'no-store');
  throw createError({ statusCode: 410, statusMessage: 'Sitemap gone' });
});
