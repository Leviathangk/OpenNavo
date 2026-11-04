import { createError, defineEventHandler, setHeader } from 'h3';
import { VERSIONS_SITEMAP_PILOT_BUCKET } from '../../shared/utils/sitemapBuckets';
import { getSiteSettings } from '../utils/siteSettings';

const DISABLED_BUCKET = /^\/__sitemap__\/(fonts|versions)-bucket-\d+\.xml$/;
const PILOT_BUCKET_PATH = `/__sitemap__/${VERSIONS_SITEMAP_PILOT_BUCKET}.xml`;
const UNCACHEABLE = ['Cache-Control', 'CDN-Cache-Control', 'Cloudflare-CDN-Cache-Control'] as const;

// Run before the sitemap response cache, including cached XML from an earlier enabled configuration.
export default defineEventHandler(async event => {
  const path = event.path.split('?', 1)[0] ?? '';
  const group = DISABLED_BUCKET.exec(path)?.[1];
  if (!group) return;

  const settings = await getSiteSettings();
  const enabled =
    group === 'fonts' ? settings.pages.fontsSitemap : settings.pages.versionsSitemap || path === PILOT_BUCKET_PATH;
  if (enabled) return;

  // H3 may send a thrown error directly without running Nitro's beforeResponse hooks.
  for (const header of UNCACHEABLE) setHeader(event, header, 'no-store');
  throw createError({ statusCode: 410, statusMessage: 'Sitemap gone' });
});
