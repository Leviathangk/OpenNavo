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
