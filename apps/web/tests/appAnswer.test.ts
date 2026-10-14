import { readFileSync } from 'node:fs';
import type { PackageDetail } from '@opennavo/api';
import { describe, expect, it } from 'vitest';
import {
  downloadChoice,
  hasZap,
  installNote,
  joinSentences,
  leadSentences,
  minimumOnly,
  priceAmount,
  versionDates
} from '../app/utils/appAnswer';
import type { Translate } from '../app/utils/seoTitles';

const LOCALES = ['zh-CN', 'en-US', 'ja-JP', 'es-ES', 'pt-BR', 'ru-RU'] as const;

// Real messages with vue-i18n {name} interpolation, so every template is exercised in all six languages.
function translator(code: (typeof LOCALES)[number]): Translate {
  const messages = JSON.parse(readFileSync(new URL(`../i18n/locales/${code}.json`, import.meta.url), 'utf8'));
  return (key, named = {}) => {
    const template = key.split('.').reduce((node, part) => node?.[part], messages);
    if (typeof template !== 'string') throw new Error(`missing ${code} ${key}`);
    return template.replace(/\{(\w+)\}/g, (_, name: string) => {
      if (!(name in named)) throw new Error(`unfilled {${name}} in ${code} ${key}`);
      return String(named[name]);
    });
  };
}

const price = {
  type: 'subscription' as const,
  amount: 9.99,
  currency: 'USD',
  region: 'US',
  plan: 'Pro',
  period: 'month' as const,
  appVersion: '5.2',
  sourceUrl: 'https://vendor.example/pricing',
  method: 'vendor_site' as const,
  // A past year, so the formatted date always includes it.
  verifiedAt: '2025-12-01T08:00:00Z',
  stale: false,
  offer: true
};

function app(overrides: Partial<PackageDetail> = {}): PackageDetail {
  return {
    kind: 'cask',
    token: 'editor',
    name: 'Editor',
    displayName: 'Editor',
    summary: 'Edits text',
    version: '5.2',
    isFont: false,
    autoUpdates: false,
    primaryCategory: { slug: 'developer-tools', name: 'Developer Tools', icon: 'lucide:code' },
    artifacts: { apps: ['Editor.app'], binaries: [], pkgs: [], uninstallNotes: [], entries: [] },
    dependsOn: { macos: '>= 13', arch: [], formulae: [], casks: [], requirements: { macos: { '>=': ['13'] } } },
    downloadUrl: 'https://vendor.example/Editor.dmg',
    homepage: 'https://vendor.example/',
    platforms: [],
    ...overrides
  } as PackageDetail;
}

describe('WP9 lead', () => {
  it.each(LOCALES)('%s states what the app is and only verified, current facts', code => {
    const t = translator(code);
    const locale = code;
    const plain = joinSentences(leadSentences(app(), t), locale);
    expect(plain).toContain('Editor');
    expect(plain).toContain('13');
    const priced = leadSentences(app({ price }), t);
    expect(priced).toHaveLength(3);
    // Old price evidence stays out of the lead.
    expect(leadSentences(app({ price: { ...price, stale: true } }), t)).toHaveLength(2);
    expect(leadSentences(app({ isFont: true, artifacts: { ...app().artifacts, apps: [] } }), t)[0]).toContain('Editor');
    for (const variant of [
      app({ primaryCategory: null }),
      app({ artifacts: { ...app().artifacts, apps: [] } }),
      app({ primaryCategory: null, artifacts: { ...app().artifacts, apps: [] } })
    ])
      expect(leadSentences(variant, t)[0]).toContain('Editor');
    expect(priceAmount(price, t, locale)).toMatch(/9[.,]99/);
  });

  it('Only shortens Homebrew’s macOS constraint when it is a single minimum', () => {
    expect(minimumOnly(app())).toBe('13');
    for (const macos of [{ '>=': ['13'], '<=': ['15'] }, { '==': ['13'] }, { '>=': ['12', '13'] }, ['13'], undefined])
      expect(minimumOnly(app({ dependsOn: { ...app().dependsOn!, requirements: { macos } } }))).toBeNull();
    expect(minimumOnly(app({ dependsOn: null }))).toBeNull();
    const t = translator('en-US');
    expect(leadSentences(app({ dependsOn: null }), t)).toEqual(['Editor is a Developer Tools app for Mac.']);
    expect(joinSentences(['一。', '二。'], 'zh-CN')).toBe('一。二。');
    expect(joinSentences(['One.', 'Two.'], 'en-US')).toBe('One. Two.');
  });
});

describe('WP9 install guide', () => {
  const platform = (arch: string, url: string | null, version = '5.2') =>
    ({ tag: `${arch}_sonoma`, macos: '14', arch, version, downloadUrl: url }) as NonNullable<
      PackageDetail['platforms']
    >[number];

  it('Links the file Homebrew uses only when the match is reliable', () => {
    expect(downloadChoice(app())).toEqual({ kind: 'direct', url: 'https://vendor.example/Editor.dmg' });
    expect(downloadChoice(app({ downloadUrl: null }))).toEqual({
      kind: 'official',
      url: 'https://vendor.example/',
      varies: false
    });
    expect(downloadChoice(app({ downloadUrl: null, homepage: null }))).toBeNull();
    const universal = [
      platform('arm64', 'https://vendor.example/u.dmg'),
      platform('x86_64', 'https://vendor.example/u.dmg')
    ];
    expect(downloadChoice(app({ platforms: universal }))).toEqual({
      kind: 'direct',
      url: 'https://vendor.example/u.dmg'
    });
    const split = [
      platform('x86_64', 'https://vendor.example/intel.dmg'),
      platform('arm64', 'https://vendor.example/arm.dmg')
    ];
    expect(downloadChoice(app({ platforms: split }))).toEqual({
      kind: 'arch',
      items: [
        { arch: 'arm64', url: 'https://vendor.example/arm.dmg' },
        { arch: 'x86_64', url: 'https://vendor.example/intel.dmg' }
      ]
    });
    // Different files per macOS version, or a platform without a URL, send people to the vendor.
    const varies = [
      platform('arm64', 'https://vendor.example/a.dmg'),
      platform('arm64', 'https://vendor.example/b.dmg')
    ];
    expect(downloadChoice(app({ platforms: varies }))).toEqual({
      kind: 'official',
      url: 'https://vendor.example/',
      varies: true
    });
    expect(downloadChoice(app({ platforms: [platform('arm64', null)] }))).toMatchObject({
      kind: 'official',
      varies: true
    });
    // Legacy builds for older macOS releases are not the current version.
    expect(
      downloadChoice(app({ platforms: [platform('x86_64', 'https://vendor.example/old.dmg', '4.0')] }))
    ).toMatchObject({
      kind: 'direct',
      url: 'https://vendor.example/Editor.dmg'
    });
  });

  it('Describes the install by kind and offers --zap only when files are listed', () => {
    expect(installNote(app())).toEqual({ key: 'installApp' });
    expect(installNote(app({ isFont: true }))).toEqual({ key: 'installFont' });
    expect(installNote(app({ artifacts: { ...app().artifacts, apps: [], pkgs: ['Driver.pkg'] } }))).toEqual({
      key: 'installPkg',
      pkg: 'Driver.pkg'
    });
    expect(installNote(app({ artifacts: { ...app().artifacts, apps: [] } }))).toBeNull();
    expect(hasZap(app())).toBe(false);
    expect(
      hasZap(
        app({
          artifacts: {
            ...app().artifacts,
            entries: [{ type: 'zap', phase: 'cleanup', sources: [], target: null, declaration: {} }]
          }
        })
      )
    ).toBe(true);
  });
});

describe('WP9 questions and facts', () => {
  const t = translator('en-US');

  it('Formats amounts and keeps the two version dates apart', () => {
    expect(priceAmount(price, t, 'en-US')).toBe('$9.99 per month');
    expect(priceAmount({ ...price, period: 'year' }, t, 'en-US')).toBe('$9.99 per year');
    expect(priceAmount({ ...price, period: 'one_time', currency: 'EUR' }, t, 'en-US')).toBe('€9.99');
    expect(priceAmount({ ...price, currency: 'XX' }, t, 'en-US')).toBe('9.99 XX per month');
    expect(priceAmount({ ...price, amount: null }, t, 'en-US')).toBe('');
    const release = { version: '5.2', publishedAt: '2026-09-29T00:00:00Z' } as PackageDetail['latestRelease'];
    expect(versionDates(app({ versionCommittedAt: '2026-09-30T00:00:00Z', latestRelease: release }))).toEqual({
      recorded: '2026-09-30T00:00:00Z',
      released: '2026-09-29T00:00:00Z'
    });
    expect(versionDates(app({ latestRelease: { ...release!, version: '5.1' } }))).toEqual({
      recorded: undefined,
      released: undefined
    });
    expect(versionDates(app({ version: 'latest', versionCommittedAt: '2026-09-30T00:00:00Z' }))).toEqual({});
  });
});
