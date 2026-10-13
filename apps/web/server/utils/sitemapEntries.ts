import { LOCALES, LOCALE_METADATA, type Locale } from '@opennavo/shared';

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
      href: `${LOCALE_METADATA[code].prefix}${path}`
    })),
    ...(locales.includes('en-US') ? [{ hreflang: 'x-default', href: path }] : [])
  ];
  return locales.map(code => {
    const modified = lastmod(code);
    return {
      loc: `${LOCALE_METADATA[code].prefix}${path}`,
      ...(modified ? { lastmod: modified } : {}),
      alternatives,
      // Explicit group: i18n mapping would otherwise move unmarked URLs into locale sitemaps.
      _sitemap: sitemap
    };
  });
}
