import { LOCALES, LOCALE_METADATA, SITE_LOCALES, type SiteLocale as Locale } from '@opennavo/shared';
import { hreflangValues } from '../../shared/utils/hreflang';

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

/**
 * One entry per language with alternates limited to the same languages; x-default only with English. The
 * alternates carry the same hreflang values as the page head (shared/utils/hreflang.ts). Pages that are not
 * about one app or article default to the app locales: the four site-only languages join once they are open
 * for indexing (L1, activation step 6.6).
 */
export function localizedEntries(
  path: string,
  sitemap: string,
  locales: readonly Locale[] = LOCALES,
  lastmod: (locale: Locale) => string | null | undefined = () => undefined
) {
  const alternatives = [
    ...locales.flatMap(code =>
      hreflangValues(code).map(hreflang => ({ hreflang, href: sitemapHref(`${LOCALE_METADATA[code].prefix}${path}`) }))
    ),
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

const PREFIXED = SITE_LOCALES.filter(locale => LOCALE_METADATA[locale].prefix).sort(
  (a, b) => LOCALE_METADATA[b].prefix.length - LOCALE_METADATA[a].prefix.length
);

/** Site locale of a page URL or path from its language prefix (/zh-hant/x → zh-Hant); no prefix is English. */
export function pathLocale(url: string): Locale {
  const path = url.replace(/^[a-z][\w+.-]*:\/\/[^/]+/i, '') || '/';
  return (
    PREFIXED.find(locale => {
      const prefix = LOCALE_METADATA[locale].prefix;
      return path === prefix || path.startsWith(`${prefix}/`);
    }) ?? 'en-US'
  );
}

/**
 * L1: entries for pages that are not about one app or article (the module's static group) keep only the
 * languages open for indexing; their alternates point only at open languages, and zh-Hant's carry its
 * zh-TW and zh-HK aliases as the page head does.
 */
export function openLocaleEntries<T extends { loc: string; alternatives?: { hreflang: string; href: string | URL }[] }>(
  entries: T[],
  open: readonly Locale[]
): T[] {
  return entries
    .filter(entry => open.includes(pathLocale(entry.loc)))
    .map(entry =>
      entry.alternatives
        ? {
            ...entry,
            alternatives: entry.alternatives.flatMap(alternative => {
              if (alternative.hreflang === 'x-default') return open.includes('en-US') ? [alternative] : [];
              const locale = pathLocale(String(alternative.href));
              if (!open.includes(locale)) return [];
              const aliases = alternative.hreflang === LOCALE_METADATA[locale].hreflang ? hreflangValues(locale) : [];
              return aliases.length
                ? aliases
                    .filter(value => value !== alternative.hreflang)
                    .map(hreflang => ({ ...alternative, hreflang }))
                    .concat(alternative)
                : [alternative];
            })
          }
        : entry
    );
}
