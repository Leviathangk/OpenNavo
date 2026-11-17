import { describe, expect, it, vi } from 'vitest';

vi.mock('@/api', () => ({ api: {} }));

import { developerCountryName } from '@/composables/useDeveloperMap';

describe('desktop developer map country names', () => {
  it('uses the shared China affiliation overrides', () => {
    expect(developerCountryName('HK', 'zh-CN')).toBe('中国香港');
    expect(developerCountryName('TW', 'zh-Hant')).toBe('中國台灣');
    expect(developerCountryName('HK', 'en-US')).toBe('Hong Kong (China)');
    expect(developerCountryName('TW', 'ja-JP')).toBe('中国台湾');
  });

  it('keeps ordinary country names localized', () => {
    expect(developerCountryName('US', 'en-US')).toBe('United States');
    expect(developerCountryName('JP', 'ja-JP')).toBe('日本');
  });
});
