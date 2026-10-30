import { describe, expect, it } from 'vitest';
import { allPackageAliases, matchLocale, packageAliases } from '../src/locale';
import { DESKTOP_LOCALES, SITE_LOCALES } from '../src/locales.gen';
import { readFileSync } from 'node:fs';

// `expected` is the app match; `desktop` is the Mac client's where its ten languages give a different answer.
const sharedCases: { name: string; languages: string[]; expected: string; desktop?: string }[] = JSON.parse(
  readFileSync(new URL('../../../apps/server/testdata/locale_cases.json', import.meta.url), 'utf8')
);

describe('Locale matching', () => {
  it.each(sharedCases)('Uses the shared $name field', ({ languages, expected }) => {
    expect(matchLocale(languages)).toBe(expected);
  });
  it.each(sharedCases)('Uses the shared $name field for the desktop', ({ languages, expected, desktop }) => {
    expect(matchLocale(languages, DESKTOP_LOCALES)).toBe(desktop ?? expected);
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

describe('Package alternate names', () => {
  const names = [
    'Visual Studio Code',
    'vscode',
    'VS Code',
    'VS Code 编辑器',
    'VS Code エディター',
    'コード',
    '編集',
    'Редактор VS Code',
    'VS Code 편집기'
  ];
  const allAliases = [
    'vscode',
    'VS Code',
    'VS Code 编辑器',
    'VS Code エディター',
    'コード',
    '編集',
    'Редактор VS Code',
    'VS Code 편집기'
  ];
  const latinAliases = ['vscode', 'VS Code'];
  const japaneseAliases = ['vscode', 'VS Code', 'VS Code エディター', 'コード'];

  it.each([
    ['en-US', latinAliases],
    ['de-DE', latinAliases],
    ['ja-JP', japaneseAliases],
    ['zh-CN', allAliases],
    ['zh-Hant', allAliases]
  ] as const)('filters spec-card aliases for %s', (locale, expected) => {
    expect(packageAliases(names, 'Visual Studio Code', locale)).toEqual(expected);
  });
  it('hides a kanji-only alias on Japanese pages', () => {
    expect(packageAliases(['Visual Studio Code', '編集'], 'Visual Studio Code', 'ja-JP')).toEqual([]);
  });
  it('shows a kana-only alias on Japanese pages', () => {
    expect(packageAliases(['Visual Studio Code', 'コード'], 'Visual Studio Code', 'ja-JP')).toEqual(['コード']);
  });
  it('defaults to Latin aliases when no locale is given', () => {
    expect(packageAliases(names, 'Visual Studio Code')).toEqual(latinAliases);
  });
  it('keeps every alias for Quick facts and structured data', () => {
    expect(allPackageAliases(names, 'Visual Studio Code')).toEqual(allAliases);
  });
  it('handles empty input and preserves Latin aliases with accents', () => {
    expect(packageAliases([], 'Example', 'en-US')).toEqual([]);
    expect(packageAliases(['Example', 'Éditeur'], 'Example', 'en-US')).toEqual(['Éditeur']);
  });
});
