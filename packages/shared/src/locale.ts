import { DEFAULT_LOCALE, LOCALES } from './locales.gen';
import type { LocaleCode, SiteLocaleCode } from './locales.gen';

/** Chinese tags that read Traditional script: explicit Hant, or a Taiwan, Hong Kong or Macau region. */
const TRADITIONAL_CHINESE = /^zh-(hant|tw|hk|mo)(-|$)/;

/**
 * Match preferences in order, mapping regional variants to supported languages and falling back to English.
 * `supported` defaults to the app locales; the website passes SITE_LOCALES.
 */
export function matchLocale<T extends SiteLocaleCode = LocaleCode>(
  languages: readonly string[],
  supported: readonly T[] = LOCALES as readonly SiteLocaleCode[] as readonly T[]
): T {
  for (const language of languages) {
    const normalized = language.trim().replaceAll('_', '-').toLowerCase();
    const exact = supported.find(code => code.toLowerCase() === normalized);
    if (exact) return exact;
    const base = normalized.split('-')[0];
    // Chinese splits by script, so zh-TW must not fall to the first zh-* code in the list.
    if (base === 'zh') {
      const script = TRADITIONAL_CHINESE.test(normalized) ? 'zh-Hant' : 'zh-CN';
      const chinese = supported.find(code => code === script) ?? supported.find(code => code.startsWith('zh-'));
      if (chinese) return chinese;
      continue;
    }
    const matched = supported.find(code => code.split('-')[0] === base);
    if (matched) return matched;
  }
  return DEFAULT_LOCALE as T;
}

/** Sparse content: requested locale → English → source locale, then host fallback such as official name. */
export function pick(
  text: Partial<Record<SiteLocaleCode, string | null>> | null | undefined,
  locale: SiteLocaleCode,
  sourceLocale: SiteLocaleCode = DEFAULT_LOCALE,
  fallback = ''
): string {
  for (const code of [locale, DEFAULT_LOCALE, sourceLocale]) {
    const value = text?.[code];
    if (value?.trim()) return value;
  }
  return fallback;
}

// Japanese kana, including halfwidth katakana and the prolonged sound mark; punctuation and marks
// are allowed separately below, so an alias such as エディター keeps its ー.
const KANA = '\\u3040-\\u30ff\\u31f0-\\u31ff\\uff66-\\uff9f';
const LATIN_ALIAS = /^[\p{Script=Latin}\p{M}\p{Nd}\p{Z}\p{P}]*$/u;
const JAPANESE_ALIAS = new RegExp(`^[\\p{Script=Latin}\\p{M}\\p{Nd}\\p{Z}\\p{P}${KANA}]*$`, 'u');

/**
 * Alternate names for the package detail spec card, filtered by the page locale: Chinese pages keep
 * every alias; Japanese pages keep kana and/or Latin aliases (Han names are hidden); every other
 * locale keeps only Latin aliases. Digits, spaces and punctuation are allowed in either script.
 */
export function packageAliases(names: readonly string[], displayName: string, locale?: SiteLocaleCode): string[] {
  const language = (locale ?? DEFAULT_LOCALE).toLowerCase().split('-')[0];
  const aliases = names.filter(name => name !== displayName);
  if (language === 'zh') return aliases;
  const allowed = language === 'ja' ? JAPANESE_ALIAS : LATIN_ALIAS;
  return aliases.filter(name => allowed.test(name));
}

/**
 * Every alias except the displayed name, for surfaces outside the spec card: Quick facts and
 * structured data keep names in their original languages instead of filtering by page locale.
 */
export function allPackageAliases(names: readonly string[], displayName: string): string[] {
  return names.filter(name => name !== displayName);
}
