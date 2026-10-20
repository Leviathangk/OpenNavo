import { describe, expect, it } from 'vitest';
import { matchLocale } from '../src/locale';
import { SITE_LOCALES } from '../src/locales.gen';
import { readFileSync } from 'node:fs';

const sharedCases: { name: string; languages: string[]; expected: string }[] = JSON.parse(
  readFileSync(new URL('../../../apps/server/testdata/locale_cases.json', import.meta.url), 'utf8')
);

describe('Locale matching', () => {
  it.each(sharedCases)('Uses the shared $name field', ({ languages, expected }) => {
    expect(matchLocale(languages)).toBe(expected);
  });
  it.each([
    [['en-US'], 'en-US'],
    [['zh-CN'], 'zh-CN'],
    [['ja-JP'], 'ja-JP'],
    [['es-ES'], 'es-ES'],
    [['pt-BR'], 'pt-BR'],
    [['ru-RU'], 'ru-RU'],
    [['fr-FR', 'JA_jp'], 'ja-JP'],
    [['zh-Hant-TW'], 'zh-CN'],
    [['pt-PT', 'en-US'], 'pt-BR'],
    [['es-MX'], 'es-ES'],
    [['en-GB'], 'en-US'],
    [['ru'], 'ru-RU'],
    [['  JA  '], 'ja-JP'],
    [[''], 'en-US'],
    [[], 'en-US']
  ])('%j → %s', (languages, expected) => expect(matchLocale(languages)).toBe(expected));

  it.each([
    [['de-AT'], 'de-DE'],
    [['fr-CA', 'en-US'], 'fr-FR'],
    [['it-CH'], 'it-IT'],
    [['zh-TW'], 'zh-Hant'],
    [['zh-HK'], 'zh-Hant'],
    [['zh-MO'], 'zh-Hant'],
    [['zh-Hant-TW'], 'zh-Hant'],
    [['zh-Hans-HK'], 'zh-CN'],
    [['zh-SG'], 'zh-CN'],
    [['zh'], 'zh-CN'],
    [['nl-NL', 'it-IT'], 'it-IT'],
    [['nl-NL'], 'en-US']
  ])('site %j → %s', (languages, expected) => expect(matchLocale(languages, SITE_LOCALES)).toBe(expected));

  it('keeps Traditional Chinese browsers on Simplified when only app locales are supported', () => {
    expect(matchLocale(['zh-TW'])).toBe('zh-CN');
  });

  it('moves on to the next preference when no Chinese locale is supported', () => {
    expect(matchLocale(['zh-TW', 'ja-JP'], ['en-US', 'ja-JP'])).toBe('ja-JP');
  });
});
