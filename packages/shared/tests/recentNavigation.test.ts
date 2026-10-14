import { describe, expect, it } from 'vitest';
import { recentReturnPath, withRecentReturn } from '../src/recentNavigation';

describe('Recent app navigation', () => {
  it('Keeps the list page and size through detail tabs', () => {
    const list = '/recent?page=3&size=28';
    const detail = withRecentReturn('/apps/example', list);
    const query = new URL(detail, 'https://example.invalid').searchParams;
    expect(recentReturnPath(query.get('returnTo'))).toBe(list);
    expect(withRecentReturn('/apps/example/versions', query.get('returnTo'))).toBe(
      '/apps/example/versions?returnTo=%2Frecent%3Fpage%3D3%26size%3D28'
    );
    expect(withRecentReturn('/apps/example', undefined)).toBe('/apps/example');
  });
  it('Rejects other destinations and sanitizes pagination', () => {
    for (const value of [
      'https://example.com/recent',
      '//example.com/recent',
      '/recently',
      '/recent/../apps',
      ['/recent']
    ]) {
      expect(recentReturnPath(value)).toBeUndefined();
    }
    expect(recentReturnPath('/recent?page=-1&size=999&other=secret')).toBe('/recent');
  });
});
