import { createError, defineEventHandler, sendError, setResponseHeader } from 'h3';
import { checkPackageOgVisibility } from '../utils/ogVisibility';

// Share images are immutable for a month, so a URL signed while a package was public must be
// revalidated against the private-aware public detail read before nuxt-og-image serves its cache.
export default defineEventHandler(async event => {
  const decision = await checkPackageOgVisibility(event);
  if (!decision) return;
  setResponseHeader(event, 'Cache-Control', 'no-store');
  return sendError(
    event,
    createError({
      statusCode: decision.status,
      statusMessage: decision.status === 404 ? 'Package share image not found' : 'Package detail unavailable'
    })
  );
});
