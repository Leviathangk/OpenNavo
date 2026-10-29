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

/** Alternate names for the visible locale, excluding the displayed name and unrelated scripts. */
export function packageAliases(names: readonly string[], displayName: string, locale: SiteLocaleCode): string[] {
  return names.filter(name => {
    if (name === displayName) return false;
    if (/[\p{Script=Hiragana}\p{Script=Katakana}]/u.test(name) && locale !== 'ja-JP') return false;
    if (/\p{Script=Han}/u.test(name) && !['zh-CN', 'zh-Hant', 'ja-JP'].includes(locale)) return false;
    if (/\p{Script=Cyrillic}/u.test(name) && locale !== 'ru-RU') return false;
    return !/\p{Script=Hangul}/u.test(name);
  });
}
