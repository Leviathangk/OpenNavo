import { describe, expect, it } from 'vitest';
import manifest from '../src/locales.json';
import { AUTHORING_LOCALE, DEFAULT_LOCALE, LOCALE_METADATA, LOCALES, SITE_LOCALES } from '../src/locales.gen';

describe('generated locale contract', () => {
  it('matches the single source of truth, including routing and translation metadata', () => {
    expect(SITE_LOCALES).toEqual(manifest.locales.map(locale => locale.code));
    expect(LOCALES).toEqual(
      manifest.locales.filter(locale => (locale.surfaces as string[]).includes('app')).map(locale => locale.code)
    );
    expect(LOCALE_METADATA).toEqual(Object.fromEntries(manifest.locales.map(locale => [locale.code, locale])));
    expect(DEFAULT_LOCALE).toBe(manifest.defaultLocale);
    expect(AUTHORING_LOCALE).toBe(manifest.authoringLocale);
  });

  it('uses English for both the root and new content authoring', () => {
    expect(DEFAULT_LOCALE).toBe('en-US');
    expect(AUTHORING_LOCALE).toBe('en-US');
    expect(LOCALES).toContain(DEFAULT_LOCALE);
    expect(LOCALES).toContain(AUTHORING_LOCALE);
    expect(LOCALE_METADATA[DEFAULT_LOCALE].prefix).toBe('');
    expect(LOCALE_METADATA[AUTHORING_LOCALE].prefix).toBe('');
    expect(LOCALE_METADATA['zh-CN'].prefix).toBe('/zh');
  });

  it('keeps six app languages and adds four site-only languages', () => {
    expect(LOCALES).toEqual(['en-US', 'zh-CN', 'ja-JP', 'es-ES', 'pt-BR', 'ru-RU']);
    expect(SITE_LOCALES).toEqual([...LOCALES, 'de-DE', 'fr-FR', 'it-IT', 'zh-Hant']);
    for (const locale of SITE_LOCALES) expect(LOCALE_METADATA[locale].surfaces).toContain('site');
  });

  it('gives every site language a distinct route, hreflang and valid formatting locale', () => {
    const metadata = SITE_LOCALES.map(locale => LOCALE_METADATA[locale]);
    expect(new Set(metadata.map(locale => locale.prefix)).size).toBe(SITE_LOCALES.length);
    const hreflangs = metadata.flatMap(locale => [
      locale.hreflang,
      ...('hreflangAliases' in locale ? locale.hreflangAliases : [])
    ]);
    expect(new Set(hreflangs.map(value => value.toLowerCase())).size).toBe(hreflangs.length);
    expect(LOCALE_METADATA['zh-Hant']).toMatchObject({
      prefix: '/zh-hant',
      hreflang: 'zh-Hant',
      formatLocale: 'zh-TW'
    });
    for (const locale of metadata) {
      expect(Intl.getCanonicalLocales(locale.formatLocale)).toEqual([locale.formatLocale]);
      expect(locale.name.trim()).not.toBe('');
      expect(locale.style.trim()).not.toBe('');
      if (locale.code !== DEFAULT_LOCALE) expect(locale.prefix).toMatch(/^\/[a-z]{2}(-[a-z]{4})?$/);
    }
  });

  it('gives all six app languages distinct routes and valid formatting locales', () => {
    expect(LOCALES).toHaveLength(6);
    expect(new Set(LOCALES).size).toBe(LOCALES.length);
    const metadata = LOCALES.map(locale => LOCALE_METADATA[locale]);
    expect(new Set(metadata.map(locale => locale.prefix)).size).toBe(LOCALES.length);
    expect(new Set(metadata.map(locale => locale.hreflang)).size).toBe(LOCALES.length);
    for (const locale of metadata) {
      expect(locale.code).toBe(locale.formatLocale);
      expect(Intl.getCanonicalLocales(locale.formatLocale)).toEqual([locale.code]);
      expect(locale.name.trim()).not.toBe('');
      expect(locale.style.trim()).not.toBe('');
      if (locale.code !== DEFAULT_LOCALE) expect(locale.prefix).toMatch(/^\/[a-z]{2}$/);
    }
  });
});
