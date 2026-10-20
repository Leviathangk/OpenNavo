import { LOCALE_METADATA, SITE_LOCALES } from '@opennavo/shared';
import type { SiteLocale } from '@opennavo/shared';

/** Both Chinese scripts together, the rest by their English names, as the language menu lists them. */
export const LANGUAGE_ORDER: readonly SiteLocale[] = [
  'en-US',
  'zh-CN',
  'zh-Hant',
  'ja-JP',
  'de-DE',
  'es-ES',
  'fr-FR',
  'it-IT',
  'pt-BR',
  'ru-RU'
];

/**
 * Languages the footer links for the current page (C4): every site language, or only those whose hreflang
 * the page keeps when it limits its alternates (page:hreflangs, from the server's decision; null means all).
 */
export function languageNavLocales(hreflangs: readonly string[] | null): SiteLocale[] {
  return [...SITE_LOCALES]
    .sort((a, b) => LANGUAGE_ORDER.indexOf(a) - LANGUAGE_ORDER.indexOf(b))
    .filter(code => !hreflangs || hreflangs.includes(LOCALE_METADATA[code].hreflang));
}

/**
 * Resolves once no page data is loading: the pending set (Nuxt's async data promises) is empty and stays
 * empty across a macrotask, by which time any page setup that continues after a fetch has started its next
 * one. Server rendering renders the layout's footer before an async page finishes its setup, so the footer
 * waits on this before reading what the page decided (page:hreflangs). Gives up after `rounds`.
 */
export async function settledPageData(
  pending: () => readonly (Promise<unknown> | undefined)[],
  rounds = 50
): Promise<void> {
  for (let round = 0; round < rounds; round += 1) {
    const running = pending().filter(Boolean);
    if (running.length) {
      await Promise.allSettled(running);
      continue;
    }
    await new Promise(resolve => setTimeout(resolve, 0));
    if (!pending().some(Boolean)) return;
  }
}
