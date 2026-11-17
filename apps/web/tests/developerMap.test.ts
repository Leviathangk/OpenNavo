import { describe, expect, it } from 'vitest';
import { developerCountryName, developerPlaceName, type DeveloperMapPlace } from '../app/composables/useDeveloperMap';

function place(overrides: Partial<DeveloperMapPlace>): DeveloperMapPlace {
  return {
    key: 'US',
    kind: 'country',
    name: 'US',
    countryCode: 'US',
    region: 'americas',
    lat: 37.77,
    lng: -122.42,
    developers: 10,
    apps: 12,
    installs30d: 1000,
    topDevelopers: [],
    ...overrides
  };
}

describe('developer map place labels', () => {
  it.each([
    ['en-US', 'Hong Kong (China)', 'Taiwan (China)'],
    ['en', 'Hong Kong (China)', 'Taiwan (China)'],
    ['zh-CN', '中国香港', '中国台湾'],
    ['zh', '中国香港', '中国台湾'],
    ['zh-Hant', '中國香港', '中國台灣'],
    ['zh-hant', '中國香港', '中國台灣'],
    ['zh-TW', '中國香港', '中國台灣'],
    ['zh-HK', '中國香港', '中國台灣'],
    ['ja-JP', '中国香港', '中国台湾'],
    ['es-ES', 'Hong Kong (China)', 'Taiwán (China)'],
    ['pt-BR', 'Hong Kong (China)', 'Taiwan (China)'],
    ['ru-RU', 'Гонконг (Китай)', 'Тайвань (Китай)'],
    ['de-DE', 'Hongkong (China)', 'Taiwan (China)'],
    ['fr-FR', 'Hong Kong (Chine)', 'Taïwan (Chine)'],
    ['it-IT', 'Hong Kong (Cina)', 'Taiwan (Cina)']
  ])('shows Hong Kong and Taiwan with their China affiliation in %s', (locale, hongKong, taiwan) => {
    expect(developerCountryName('HK', locale)).toBe(hongKong);
    expect(developerCountryName('TW', locale)).toBe(taiwan);
  });

  it('normalizes country codes before applying the override', () => {
    expect(developerCountryName(' hk ', 'zh-CN')).toBe('中国香港');
    expect(developerCountryName('tw', 'zh-hant')).toBe('中國台灣');
  });

  it('localizes country places instead of printing the code', () => {
    const japan = place({ key: 'JP', name: 'JP', countryCode: 'JP' });
    expect(developerPlaceName(japan, 'en-US')).toBe('Japan');
    expect(developerPlaceName(japan, 'ja-JP')).toBe('日本');
    expect(developerPlaceName(japan, 'zh-CN')).toBe('日本');
  });

  it('applies the China affiliation through place labels', () => {
    expect(developerPlaceName(place({ key: 'HK', name: 'HK', countryCode: 'HK' }), 'zh-CN')).toBe('中国香港');
    expect(developerPlaceName(place({ key: 'TW', name: 'TW', countryCode: 'TW' }), 'zh-hant')).toBe('中國台灣');
  });

  it('keeps every other country name localized by the reader locale', () => {
    expect(developerCountryName('US', 'en-US')).toBe('United States');
    expect(developerCountryName('US', 'zh-CN')).toBe('美国');
    expect(developerCountryName('JP', 'ja-JP')).toBe('日本');
  });

  it('does not show legacy city text from a cached payload', () => {
    expect(developerPlaceName(place({ name: 'San Francisco' }), 'en-US')).toBe('United States');
  });

  it('falls back to the raw code when the code is not a region', () => {
    expect(developerCountryName('U', 'en-US')).toBe('U');
  });
});
