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
  it('localizes country places instead of printing the code', () => {
    const japan = place({ key: 'JP', name: 'JP', countryCode: 'JP' });
    expect(developerPlaceName(japan, 'en-US')).toBe('Japan');
    expect(developerPlaceName(japan, 'ja-JP')).toBe('日本');
    expect(developerPlaceName(japan, 'zh-CN')).toBe('日本');
  });

  it('does not show legacy city text from a cached payload', () => {
    expect(developerPlaceName(place({ name: 'San Francisco' }), 'en-US')).toBe('United States');
  });

  it('falls back to the raw code when the code is not a region', () => {
    expect(developerCountryName('U', 'en-US')).toBe('U');
  });
});
