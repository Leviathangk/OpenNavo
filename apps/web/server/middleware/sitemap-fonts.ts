import { createError, defineEventHandler } from 'h3';

// Run before the sitemap response cache, including cached XML from an earlier enabled configuration.
export default defineEventHandler(async event => {
  if (
    /^\/__sitemap__\/fonts-bucket-\d+\.xml$/.test(event.path.split('?')[0] ?? '') &&
    !(await getSiteSettings()).pages.fontsSitemap
  ) {
    throw createError({ statusCode: 404, statusMessage: 'Sitemap not found' });
  }
});
