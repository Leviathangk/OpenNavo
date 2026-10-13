import { describe, expect, it } from 'vitest';
import type { PackageDetail } from '@opennavo/api';
import { allowedHreflangs, latestVersionDate, packageRobots } from '../app/utils/indexing';
import { localizedEntries } from '../server/utils/sitemapEntries';

type Indexing = NonNullable<PackageDetail['indexing']>;
const indexing = (overrides: Partial<Indexing> = {}): Indexing => ({
  indexable: true,
  reason: null,
  locales: ['en-US', 'zh-CN'],
  enforced: true,
  notesCount: 0,
  ...overrides
});
const noRelations = { dependsOn: null, conflictsWith: { casks: [], formulae: [] } };

describe('WP11 page decisions', () => {
  it('Keeps today’s behaviour while the policy does not apply', () => {
    const pkg = {
      ...noRelations,
      indexing: indexing({ enforced: false, locales: ['en-US', 'zh-CN', 'ja-JP', 'es-ES', 'pt-BR', 'ru-RU'] })
    };
    for (const tab of ['overview', 'versions', 'dependencies', 'details'] as const)
      expect(packageRobots(pkg, tab, 'ja-JP'), tab).toBeUndefined();
    expect(packageRobots({ ...noRelations, indexing: undefined }, 'details', 'en-US')).toBeUndefined();
    expect(allowedHreflangs(pkg.indexing)).toBeNull();
  });

  it('Noindexes unpublished languages and sub-pages without their own value', () => {
    const pkg = { ...noRelations, indexing: indexing() };
    expect(packageRobots(pkg, 'overview', 'en-US')).toBeUndefined();
    expect(packageRobots(pkg, 'overview', 'ja-JP')).toBe('noindex, follow');
    expect(packageRobots(pkg, 'versions', 'en-US')).toBe('noindex, follow');
    expect(packageRobots({ ...pkg, indexing: indexing({ notesCount: 3 }) }, 'versions', 'en-US')).toBeUndefined();
    expect(packageRobots(pkg, 'dependencies', 'en-US')).toBe('noindex, follow');
    expect(
      packageRobots(
        { ...pkg, dependsOn: { formula: ['ripgrep'] } as PackageDetail['dependsOn'] },
        'dependencies',
        'en-US'
      )
    ).toBeUndefined();
    expect(packageRobots(pkg, 'details', 'en-US')).toBe('noindex, follow');
    expect(
      packageRobots(
        { ...pkg, indexing: indexing({ indexable: false, reason: 'thin', locales: [] }) },
        'overview',
        'en-US'
      )
    ).toBe('noindex, follow');
  });

  it('Limits hreflang to indexed languages, with x-default only alongside English', () => {
    expect(allowedHreflangs(indexing())?.sort()).toEqual(['en', 'x-default', 'zh', 'zh-Hans']);
    expect(allowedHreflangs(indexing({ locales: ['pt-BR'] }))?.sort()).toEqual(['pt', 'pt-BR']);
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
          { hreflang: 'x-default', href: '/apps/x' }
        ],
        _sitemap: 'apps'
      },
      {
        loc: '/zh/apps/x',
        alternatives: entries[0]!.alternatives,
        _sitemap: 'apps'
      }
    ]);
    expect(localizedEntries('/apps/y', 'apps', ['ja-JP'])[0]!.alternatives).toEqual([
      { hreflang: 'ja', href: '/ja/apps/y' }
    ]);
  });
});
