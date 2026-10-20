import { describe, expect, it } from 'vitest';
import type { PackageDetail } from '@opennavo/api';
import { latestVersionDate } from '../app/utils/indexing';
import { hreflangsFor, hreflangValues } from '../shared/utils/hreflang';
import { localizedEntries, openLocaleEntries, pathLocale } from '../server/utils/sitemapEntries';

// Page index decisions themselves come from the API (S5) and are tested in Go; the web only maps the
// decision's languages to hreflang values.
describe('hreflang values (S5, L1)', () => {
  it('Maps published languages to their values, catch-alls and aliases, with x-default only alongside English', () => {
    expect(hreflangsFor(['en-US', 'zh-CN']).sort()).toEqual(['en', 'x-default', 'zh', 'zh-Hans']);
    expect(hreflangsFor(['pt-BR']).sort()).toEqual(['pt', 'pt-BR']);
    expect(hreflangsFor(['zh-Hant']).sort()).toEqual(['zh-HK', 'zh-Hant', 'zh-TW']);
    expect(hreflangsFor(['de-DE', 'fr-FR', 'it-IT']).sort()).toEqual(['de', 'fr', 'it']);
    expect(hreflangValues('zh-CN')).toEqual(['zh-Hans', 'zh']);
  });

  it('Shows only reliable release dates for the latest version', () => {
    const base = { displayName: 'App', version: '2.0', versionCommittedAt: '2025-03-18T10:00:00Z' };
    expect(latestVersionDate({ ...base, latestRelease: null })).toBe('2025-03-18T10:00:00Z');
    expect(
      latestVersionDate({
        ...base,
        latestRelease: { version: '2.0', publishedAt: '2025-03-17T00:00:00Z' } as PackageDetail['latestRelease']
      })
    ).toBe('2025-03-17T00:00:00Z');
    expect(latestVersionDate({ ...base, versionCommittedAt: null, latestRelease: null })).toBeUndefined();
  });
});

describe('sitemap entries', () => {
  it('Lists only the given languages and omits unknown lastmod', () => {
    const entries = localizedEntries('/apps/x', 'apps', ['en-US', 'zh-CN'], locale =>
      locale === 'en-US' ? '2025-03-18T10:00:00Z' : null
    );
    expect(entries).toEqual([
      {
        loc: '/apps/x',
        lastmod: '2025-03-18T10:00:00Z',
        alternatives: [
          { hreflang: 'en', href: '/apps/x' },
          { hreflang: 'zh-Hans', href: '/zh/apps/x' },
          { hreflang: 'zh', href: '/zh/apps/x' },
          { hreflang: 'x-default', href: '/apps/x' }
        ],
        _encoded: true,
        _sitemap: 'apps'
      },
      {
        loc: '/zh/apps/x',
        alternatives: entries[0]!.alternatives,
        _encoded: true,
        _sitemap: 'apps'
      }
    ]);
    expect(localizedEntries('/apps/y', 'apps', ['ja-JP'])[0]!.alternatives).toEqual([
      { hreflang: 'ja', href: '/ja/apps/y' }
    ]);
  });
});

describe('open languages in the static sitemap (L1)', () => {
  it('Reads the language from the path prefix', () => {
    expect(pathLocale('https://opennavo.com/zh-hant/download')).toBe('zh-Hant');
    expect(pathLocale('/zh/download')).toBe('zh-CN');
    expect(pathLocale('https://opennavo.com/download')).toBe('en-US');
    expect(pathLocale('/de')).toBe('de-DE');
  });

  it('Keeps only open languages, with their alternates and zh-Hant aliases', () => {
    const alternatives = [
      { hreflang: 'en', href: 'https://opennavo.com/download' },
      { hreflang: 'de', href: 'https://opennavo.com/de/download' },
      { hreflang: 'zh-Hant', href: 'https://opennavo.com/zh-hant/download' },
      { hreflang: 'x-default', href: 'https://opennavo.com/download' }
    ];
    const entries = [
      { loc: 'https://opennavo.com/download', alternatives },
      { loc: 'https://opennavo.com/de/download', alternatives },
      { loc: 'https://opennavo.com/zh-hant/download', alternatives }
    ];
    const closed = openLocaleEntries(entries, ['en-US']);
    expect(closed.map(entry => entry.loc)).toEqual(['https://opennavo.com/download']);
    expect(closed[0]!.alternatives.map(item => item.hreflang)).toEqual(['en', 'x-default']);
    const open = openLocaleEntries(entries, ['en-US', 'de-DE', 'zh-Hant']);
    expect(open).toHaveLength(3);
    expect(open[0]!.alternatives.map(item => item.hreflang).sort()).toEqual(
      ['de', 'en', 'x-default', 'zh-HK', 'zh-Hant', 'zh-TW'].sort()
    );
  });
});
