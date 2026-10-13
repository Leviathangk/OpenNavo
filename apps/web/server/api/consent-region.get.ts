// Whether GA4 must wait for consent for this visitor (WP8). Pages are cached and shared, so the
// region is looked up per request here instead of being rendered into HTML.
export default defineEventHandler(async event => {
  setHeader(event, 'Cache-Control', 'private, no-store');
  const { analytics } = await getSiteSettings();
  return { requireConsent: requiresConsent(getHeader(event, 'cf-ipcountry'), analytics.consentRegions) };
});
