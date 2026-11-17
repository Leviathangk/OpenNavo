import { describe, expect, it } from 'vitest';
import { countryNameOverride } from '../src/countryNames';
import { SITE_LOCALES } from '../src/locales.gen';

describe('developer map country name overrides', () => {
  it.each([
    ['en-US', 'Hong Kong (China)', 'Taiwan (China)'],
    ['zh-CN', '中国香港', '中国台湾'],
    ['zh-Hant', '中國香港', '中國台灣'],
    ['ja-JP', '中国香港', '中国台湾'],
    ['es-ES', 'Hong Kong (China)', 'Taiwán (China)'],
    ['pt-BR', 'Hong Kong (China)', 'Taiwan (China)'],
    ['ru-RU', 'Гонконг (Китай)', 'Тайвань (Китай)'],
    ['de-DE', 'Hongkong (China)', 'Taiwan (China)'],
    ['fr-FR', 'Hong Kong (Chine)', 'Taïwan (Chine)'],
    ['it-IT', 'Hong Kong (Cina)', 'Taiwan (Cina)']
  ])('shows Hong Kong and Taiwan with their China affiliation in %s', (locale, hongKong, taiwan) => {
    expect(countryNameOverride('HK', locale)).toBe(hongKong);
    expect(countryNameOverride('TW', locale)).toBe(taiwan);
  });

  it('has both labels for every site locale', () => {
    for (const locale of SITE_LOCALES) {
      expect(countryNameOverride('HK', locale)).toBeTruthy();
      expect(countryNameOverride('TW', locale)).toBeTruthy();
    }
  });

  it('normalizes the country code and Chinese locale aliases', () => {
    expect(countryNameOverride(' hk ', 'zh')).toBe('中国香港');
    expect(countryNameOverride('tw', 'zh-hant')).toBe('中國台灣');
    expect(countryNameOverride('TW', 'zh-TW')).toBe('中國台灣');
    expect(countryNameOverride('HK', 'zh-HK')).toBe('中國香港');
  });

  it('leaves every other region to Intl.DisplayNames', () => {
    expect(countryNameOverride('US', 'en-US')).toBeUndefined();
    expect(countryNameOverride('CN', 'zh-CN')).toBeUndefined();
    expect(countryNameOverride('ZZ', 'en-US')).toBeUndefined();
  });
});
