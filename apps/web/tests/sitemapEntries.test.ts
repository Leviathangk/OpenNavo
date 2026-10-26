import { labelPath } from '../shared/utils/backgroundLabels';
import { describe, expect, it } from 'vitest';
import { localizedEntries } from '../server/utils/sitemapEntries';

describe('Sitemap entries', () => {
  it('spell tokens the way pages canonicalize them, + and @ included', () => {
    for (const token of ['nextpad++', 'logi-options+', 'font-m+-nerd-font', 'visual-studio-code@insiders', 'a.b_c-1']) {
      const entries = localizedEntries(`/apps/${token}`, 'apps-bucket-0', ['en-US', 'zh-CN']);
      expect(entries.map(entry => entry.loc)).toEqual([`/apps/${token}`, `/zh/apps/${token}`]);
      // Marked as encoded, so @nuxtjs/sitemap does not run encodePath (+ → %2B) over the loc.
      expect(entries.every(entry => entry['_encoded'] === true)).toBe(true);
      // en, zh-Hans, its zh catch-all (the same values the page head writes), and x-default.
      expect(entries[0]!.alternatives.map(alternative => alternative.href)).toEqual([
        `/apps/${token}`,
        `/zh/apps/${token}`,
        `/zh/apps/${token}`,
        `/apps/${token}`
      ]);
    }
  });

  it('still escapes characters a URL cannot carry', () => {
    const [entry] = localizedEntries('/apps/odd token', 'apps-bucket-0', ['en-US']);
    expect(entry!.loc).toBe('/apps/odd%20token');
    expect(entry!.alternatives[0]!.href).toBe('/apps/odd%20token');
  });
});

it('preserves encoded label paths in loc and every alternate without encoding them again', () => {
  for (const label of ['Google Drive', 'agent.*', "a(b)'!", 'a@b+c:d', '后台', 'literal%25']) {
    const path = labelPath(label);
    const entries = localizedEntries(path, 'background-items', ['en-US', 'zh-CN']);
    expect(entries.map(entry => entry.loc)).toEqual([path, `/zh${path}`]);
    expect(entries[0]!.alternatives.map(entry => entry.href)).toEqual([path, `/zh${path}`, `/zh${path}`, path]);
  }
});

it('normalizes content dates to second precision in UTC and omits unknown or invalid dates', () => {
  const entry = (date?: string) => localizedEntries('/apps/example', 'apps-bucket-0', ['en-US'], () => date)[0];
  expect(entry('2026-10-26T11:22:33.987654+08:00')).toHaveProperty('lastmod', '2026-10-26T03:22:33Z');
  expect(entry('2026-10-26')).toHaveProperty('lastmod', '2026-10-26T00:00:00Z');
  expect(entry()).not.toHaveProperty('lastmod');
  expect(entry('not-a-date')).not.toHaveProperty('lastmod');
});
