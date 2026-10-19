// Share images (nuxt-og-image) inline every icon they draw. Its fetcher refuses private addresses,
// so an icon on the public CDN was fetched back through Cloudflare on every render, about 27k
// requests a day. With NUXT_CDN_BASE_INTERNAL set, renders read it from this same-origin path
// instead, in-process, and server/routes/_og-icon serves it from the object store inside the
// Docker network.
export const OG_ICON_PATH = '/_og-icon/';

// Object keys under icons/ only: no other bucket content, and no "." or ".." path segments.
const ICON_KEY = /^icons\/(?:[\w-]+\/)*[\w-]+(?:\.[\w-]+)*\.(?:png|jpe?g|webp|svg)$/i;

export function isOgIconKey(key: string): boolean {
  return ICON_KEY.test(key);
}

/** The address a share image draws an icon from: the same-origin path for icons on the public CDN. */
export function ogIconSrc(
  src: string | null | undefined,
  cdnBase: string,
  internalBase: string | undefined
): string | null | undefined {
  if (!src || !internalBase) return src;
  const base = `${cdnBase.replace(/\/+$/, '')}/`;
  if (!src.startsWith(base)) return src;
  const key = src.slice(base.length);
  return isOgIconKey(key) ? OG_ICON_PATH + key : src;
}
