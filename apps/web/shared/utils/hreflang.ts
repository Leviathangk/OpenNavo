import { LOCALE_METADATA, SITE_LOCALES, type SiteLocale } from '@opennavo/shared';

/**
 * Bare languages answered by one of their regional or script pages, as i18n's catch-all alternates do in
 * the page head: zh → zh-Hans (nuxt.config marks zh-CN isCatchallLocale), pt → pt-BR. The first site
 * locale carrying a subtag wins, so zh-Hant never takes over zh.
 */
const CATCHALL = new Map<string, SiteLocale>();
for (const code of SITE_LOCALES) {
  const { hreflang } = LOCALE_METADATA[code];
  const base = hreflang.split('-')[0] ?? hreflang;
  if (base !== hreflang && !CATCHALL.has(base)) CATCHALL.set(base, code);
}

/**
 * Every hreflang value a site locale's page answers for, in the head and in the sitemap alike: its own,
 * its aliases (zh-Hant also as zh-TW and zh-HK) and the bare-language catch-all it owns.
 */
export function hreflangValues(code: SiteLocale): string[] {
  const meta = LOCALE_METADATA[code];
  const base = meta.hreflang.split('-')[0] ?? meta.hreflang;
  return [
    meta.hreflang,
    ...('hreflangAliases' in meta ? meta.hreflangAliases : []),
    ...(base !== meta.hreflang && CATCHALL.get(base) === code ? [base] : [])
  ];
}

/**
 * hreflang values a page keeps for the languages it is published in: each language's values above, plus
 * x-default with English.
 */
export function hreflangsFor(locales: readonly SiteLocale[]): string[] {
  const out = new Set(locales.flatMap(locale => hreflangValues(locale)));
  if (locales.includes('en-US')) out.add('x-default');
  return [...out];
}

/** Aliases a primary hreflang value brings along (zh-Hant → zh-TW, zh-HK); none for other values. */
export function hreflangAliases(hreflang: string): readonly string[] {
  const code = SITE_LOCALES.find(locale => LOCALE_METADATA[locale].hreflang === hreflang);
  const meta = code ? LOCALE_METADATA[code] : undefined;
  return meta && 'hreflangAliases' in meta ? meta.hreflangAliases : [];
}
