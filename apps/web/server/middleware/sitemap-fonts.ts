import { createError, defineEventHandler } from 'h3';
import { sitemapFontsEnabled } from '../utils/sitemapFonts';

// Run before the sitemap response cache, including cached XML from an earlier enabled configuration.
export default defineEventHandler(event => {
  if (
    /^\/__sitemap__\/fonts-bucket-\d+\.xml$/.test(event.path.split('?')[0] ?? '') &&
    !sitemapFontsEnabled(useRuntimeConfig(event).sitemapFonts)
  ) {
    throw createError({ statusCode: 404, statusMessage: 'Sitemap not found' });
  }
});
