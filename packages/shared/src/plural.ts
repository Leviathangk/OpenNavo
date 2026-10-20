import { SITE_LOCALES, LOCALE_METADATA } from './locales.gen';
import type { SiteLocaleCode } from './locales.gen';

/** Category order comes from the manifest; use other for undeclared CLDR categories. */
export function pluralIndex(count: number, locale: SiteLocaleCode): number {
  const categories: readonly string[] = LOCALE_METADATA[locale].pluralCategories;
  const category = new Intl.PluralRules(locale).select(count);
  const index = categories.indexOf(category);
  return index < 0 ? categories.indexOf('other') : index;
}

/** Map vue-i18n web route codes to the same rules. */
export const pluralRules = Object.fromEntries(
  SITE_LOCALES.flatMap(locale => {
    const rule = (count: number, length: number) => (length === 1 ? 0 : pluralIndex(count, locale));
    return [
      [locale, rule],
      [LOCALE_METADATA[locale].prefix.slice(1) || 'en', rule]
    ];
  })
);

/** Shared display components have no vue-i18n instance; select forms using identical categories. */
export function selectPlural(message: string, count: number, locale: SiteLocaleCode): string {
  const forms = message.split('|');
  return forms.length === 1
    ? message
    : (forms[pluralIndex(count, locale)] ?? (forms[forms.length - 1] as string)).trim();
}
