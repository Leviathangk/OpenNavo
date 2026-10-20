import { interpolate, interpolatePlural } from '@opennavo/shared';
import type { SiteLocale } from '@opennavo/shared';
import deDE from '../../i18n/locales/de-DE.json';
import enUS from '../../i18n/locales/en-US.json';
import esES from '../../i18n/locales/es-ES.json';
import frFR from '../../i18n/locales/fr-FR.json';
import itIT from '../../i18n/locales/it-IT.json';
import jaJP from '../../i18n/locales/ja-JP.json';
import ptBR from '../../i18n/locales/pt-BR.json';
import ruRU from '../../i18n/locales/ru-RU.json';
import zhCN from '../../i18n/locales/zh-CN.json';
import zhHant from '../../i18n/locales/zh-Hant.json';

// Site copy for text the server writes outside pages (F6 feeds, X4 Markdown), from the same message files as
// the pages. Keys that have not reached a translated file yet fall back to English.
type Messages = Record<string, unknown>;

const MESSAGES: Partial<Record<SiteLocale, Messages>> = {
  'en-US': enUS,
  'zh-CN': zhCN,
  'ja-JP': jaJP,
  'es-ES': esES,
  'pt-BR': ptBR,
  'ru-RU': ruRU,
  'de-DE': deDE,
  'fr-FR': frFR,
  'it-IT': itIT,
  'zh-Hant': zhHant
};

function lookup(messages: Messages | undefined, key: string): string | undefined {
  let node: unknown = messages;
  for (const part of key.split('.')) {
    if (!node || typeof node !== 'object') return undefined;
    node = (node as Record<string, unknown>)[part];
  }
  return typeof node === 'string' && node ? node : undefined;
}

/** A site message in `locale`, else English, else the key; `count` selects the plural form. */
export function siteMessage(
  locale: SiteLocale,
  key: string,
  params: Record<string, string | number> = {},
  count?: number
): string {
  const template = lookup(MESSAGES[locale], key) ?? lookup(enUS, key) ?? key;
  return count === undefined ? interpolate(template, params) : interpolatePlural(template, params, count, locale);
}
