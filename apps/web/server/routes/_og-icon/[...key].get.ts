import { isOgIconKey } from '../../../shared/utils/ogIcon';

// App icons for share-image renders (shared/utils/ogIcon.ts), read from the object store inside the
// Docker network. nuxt-og-image fetches this path in-process and marks the request with
// x-nuxt-og-image; anything else gets a 404, so it never becomes a second public address for icons.
export default defineEventHandler(async event => {
  const base = String(useRuntimeConfig(event).cdnBaseInternal ?? '').replace(/\/+$/, '');
  const key = getRouterParam(event, 'key') ?? '';
  if (!base || !getHeader(event, 'x-nuxt-og-image') || !isOgIconKey(key)) {
    throw createError({ statusCode: 404 });
  }
  const response = await fetch(`${base}/${key}`, { signal: AbortSignal.timeout(5000) }).catch(() => undefined);
  if (!response?.ok) throw createError({ statusCode: 404 });
  setHeader(event, 'Content-Type', response.headers.get('content-type') ?? 'application/octet-stream');
  setHeader(event, 'Cache-Control', 'no-store');
  return new Uint8Array(await response.arrayBuffer());
});
