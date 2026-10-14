import { isValidToken, LOCALES, LOCALE_METADATA } from '@opennavo/shared';

const prefixes = new Set(LOCALES.map(locale => LOCALE_METADATA[locale].prefix.slice(1)).filter(Boolean));

// Homebrew tokens use URL-safe ASCII, including literal + and @. Redirect escaped spellings
// before Nuxt's SSR renderer encodes event.path again; otherwise %2B becomes %252B and the
// package lookup receives a percent sign. This also covers localized tabs and Nuxt payloads.
export default defineEventHandler(event => {
  if (event.method !== 'GET' && event.method !== 'HEAD') return;
  // H3 has decoded event.path already, but Nitro's SWR wrapper restores the raw request URL.
  const requestPath = event.node.req.url ?? event.path;
  const queryAt = requestPath.indexOf('?');
  const path = queryAt < 0 ? requestPath : requestPath.slice(0, queryAt);
  const query = queryAt < 0 ? '' : requestPath.slice(queryAt);
  const parts = path.split('/');
  const appAt = prefixes.has(parts[1] ?? '') ? 2 : 1;
  const tokenAt = appAt + 1;
  const escaped = parts[tokenAt];
  if (parts[appAt] !== 'apps' || !escaped?.includes('%')) return;
  let token: string;
  try {
    token = decodeURIComponent(escaped);
  } catch {
    return;
  }
  // Decode exactly once. Encoded slashes, whitespace and nested escapes are not valid tokens.
  if (!isValidToken(token)) return;
  parts[tokenAt] = token;
  return sendRedirect(event, parts.join('/') + query, 301);
});
