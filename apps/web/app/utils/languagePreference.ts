import { matchLocale, SITE_LOCALES, type SiteLocale as Locale } from '@opennavo/shared';

export const LANGUAGE_CHOICE_KEY = 'onv:locale-choice';
export const LANGUAGE_DISMISSED_KEY = 'onv:locale-suggestion-dismissed';

/** Do not override manual selections or dismissed prompts with browser preferences. */
export function suggestedLanguage(
  languages: readonly string[],
  current: Locale,
  saved: string | null,
  dismissed: string | null
): Locale | null {
  if (saved || dismissed === '1') return null;
  const suggested = matchLocale(languages, SITE_LOCALES);
  return suggested === current ? null : suggested;
}

export function readLanguagePreference(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

export function saveLanguagePreference(key: string, value: string): void {
  // Allow locale changes/prompt dismissal this session even when storage is disabled.
  try {
    localStorage.setItem(key, value);
  } catch {
    /* Browser storage unavailable. */
  }
}
