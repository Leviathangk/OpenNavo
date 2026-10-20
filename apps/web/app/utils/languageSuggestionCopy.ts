import { sharedMessages } from '@opennavo/shared';
import type { SiteLocale } from '@opennavo/shared';

// The banner speaks the suggested language before that language's messages load. Its copy lives in the shared
// messages, which every page already carries in all site languages, so suggesting another language never
// downloads that language's whole message pack (X3). The website's own locale files can't serve this: the i18n
// build turns them into compiled message modules without named exports.

export interface LanguageSuggestionCopy {
  message: string;
  switch: string;
  dismiss: string;
}

/** The suggestion banner's copy in the suggested language, with {language} filled in. */
export function languageSuggestionCopy(locale: SiteLocale, language: string): LanguageSuggestionCopy {
  const copy = sharedMessages[locale].languageSuggestion;
  const fill = (text: string) => text.replaceAll('{language}', language);
  return { message: fill(copy.message), switch: fill(copy.switch), dismiss: fill(copy.dismiss) };
}
