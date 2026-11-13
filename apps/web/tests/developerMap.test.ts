import { describe, expect, it } from 'vitest';
import { developerCountryName, developerPlaceName, type DeveloperMapPlace } from '../app/composables/useDeveloperMap';

function place(overrides: Partial<DeveloperMapPlace>): DeveloperMapPlace {
  return {
    key: 'US|bay-area',
    kind: 'city',
    name: 'San Francisco',
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
  it('shows the stored city text as submitted', () => {
    expect(developerPlaceName(place({}), 'en-US')).toBe('San Francisco');
    expect(developerPlaceName(place({ name: '杭州' }), 'ja-JP')).toBe('杭州');
  });

  it('localizes country-only places instead of printing the code', () => {
    const japan = place({ kind: 'country', key: 'JP', name: 'JP', countryCode: 'JP' });
    expect(developerPlaceName(japan, 'en-US')).toBe('Japan');
    expect(developerPlaceName(japan, 'ja-JP')).toBe('日本');
    expect(developerPlaceName(japan, 'zh-CN')).toBe('日本');
  });

  it('falls back to the raw code when the code is not a region', () => {
    expect(developerCountryName('U', 'en-US')).toBe('U');
  });
});
