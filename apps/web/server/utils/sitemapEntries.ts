import { LOCALES, LOCALE_METADATA, type Locale } from '@opennavo/shared';

/**
 * Sitemap URL for a page path, spelled the way pages link and canonicalize it. Tokens and slugs are
 * URL-safe as they are (a-z 0-9 - + @ . _), so encodeURI leaves them alone and only escapes
 * characters that should never occur. Entries are marked _encoded: @nuxtjs/sitemap would otherwise
 * run its own encodePath over each loc (not over alternates), which turns + into %2B, a URL that
 * server/middleware/package-canonical.ts answers with a 301.
 */
function sitemapHref(path: string): string {
  return encodeURI(path);
}

/** One entry per language with alternates limited to the same languages; x-default only with English. */
export function localizedEntries(
  path: string,
  sitemap: string,
  locales: readonly Locale[] = LOCALES,
  lastmod: (locale: Locale) => string | null | undefined = () => undefined
) {
  const alternatives = [
    ...locales.map(code => ({
      hreflang: LOCALE_METADATA[code].hreflang,
      href: sitemapHref(`${LOCALE_METADATA[code].prefix}${path}`)
    })),
    ...(locales.includes('en-US') ? [{ hreflang: 'x-default', href: sitemapHref(path) }] : [])
  ];
  return locales.map(code => {
    const modified = lastmod(code);
    return {
      loc: sitemapHref(`${LOCALE_METADATA[code].prefix}${path}`),
      ...(modified ? { lastmod: modified } : {}),
      alternatives,
      _encoded: true as const,
      // Explicit group: i18n mapping would otherwise move unmarked URLs into locale sitemaps.
      _sitemap: sitemap
    };
  });
}
