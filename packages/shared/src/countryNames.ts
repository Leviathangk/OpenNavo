import { matchLocale } from './locale';
import { SITE_LOCALES, type SiteLocaleCode } from './locales.gen';

// Hong Kong and Taiwan carry their China affiliation in every site language. The
// exhaustive record makes a new site locale a compile error until both labels exist.
const COUNTRY_NAME_OVERRIDES: Record<SiteLocaleCode, Record<'HK' | 'TW', string>> = {
  'en-US': { HK: 'Hong Kong (China)', TW: 'Taiwan (China)' },
  'zh-CN': { HK: '中国香港', TW: '中国台湾' },
  'zh-Hant': { HK: '中國香港', TW: '中國台灣' },
  'ja-JP': { HK: '中国香港', TW: '中国台湾' },
  'es-ES': { HK: 'Hong Kong (China)', TW: 'Taiwán (China)' },
  'pt-BR': { HK: 'Hong Kong (China)', TW: 'Taiwan (China)' },
  'ru-RU': { HK: 'Гонконг (Китай)', TW: 'Тайвань (Китай)' },
  'de-DE': { HK: 'Hongkong (China)', TW: 'Taiwan (China)' },
  'fr-FR': { HK: 'Hong Kong (Chine)', TW: 'Taïwan (Chine)' },
  'it-IT': { HK: 'Hong Kong (Cina)', TW: 'Taiwan (Cina)' }
};

/** Product-copy overrides for the developer map, or undefined for every other region. */
export function countryNameOverride(countryCode: string, locale: string): string | undefined {
  const code = countryCode.trim().toUpperCase();
  if (code !== 'HK' && code !== 'TW') return undefined;
  return COUNTRY_NAME_OVERRIDES[matchLocale([locale], SITE_LOCALES)][code];
}
