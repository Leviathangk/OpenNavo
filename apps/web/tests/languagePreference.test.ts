import { describe, expect, it } from 'vitest';
import { suggestedLanguage } from '../app/utils/languagePreference';

describe('Locale suggestion conditions', () => {
  it('Follow preference order with regional variants across the site languages', () => {
    expect(suggestedLanguage(['nl', 'ja-JP', 'ru'], 'en-US', null, null)).toBe('ja-JP');
    expect(suggestedLanguage(['fr', 'ja-JP', 'ru'], 'en-US', null, null)).toBe('fr-FR');
    expect(suggestedLanguage(['pt-PT'], 'zh-CN', null, null)).toBe('pt-BR');
    expect(suggestedLanguage(['de-AT'], 'en-US', null, null)).toBe('de-DE');
    // Traditional Chinese browsers get Traditional, not the first zh-* language (L1).
    expect(suggestedLanguage(['zh-TW'], 'en-US', null, null)).toBe('zh-Hant');
    expect(suggestedLanguage(['zh-HK'], 'zh-CN', null, null)).toBe('zh-Hant');
    expect(suggestedLanguage(['zh-SG'], 'zh-Hant', null, null)).toBe('zh-CN');
  });
  it('Hide for matching language, manual selection, or dismissed prompts', () => {
    expect(suggestedLanguage(['en-GB'], 'en-US', null, null)).toBeNull();
    expect(suggestedLanguage(['ru'], 'en-US', 'zh-CN', null)).toBeNull();
    expect(suggestedLanguage(['ru'], 'en-US', null, '1')).toBeNull();
  });
  it('Unsupported preferences suggest English without switching automatically', () => {
    expect(suggestedLanguage(['nl'], 'zh-CN', null, null)).toBe('en-US');
  });
});
