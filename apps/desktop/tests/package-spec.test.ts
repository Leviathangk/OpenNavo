import { describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';
import { OnPackageSpecCard } from '@opennavo/ui';
import type { PackageDetail } from '@opennavo/api';
import type { LocalItem } from '@/ipc/client';
import { packageSpec } from '@/utils/packageSpec';
import enUS from '@/i18n/en-US';
import zhCN from '@/i18n/zh-CN';
import jaJP from '@/i18n/ja-JP.json';
import esES from '@/i18n/es-ES.json';
import ptBR from '@/i18n/pt-BR.json';
import ruRU from '@/i18n/ru-RU.json';
import deDE from '@/i18n/de-DE.json';
import frFR from '@/i18n/fr-FR.json';
import itIT from '@/i18n/it-IT.json';
import zhHant from '@/i18n/zh-Hant.json';

const SPEC_NOW = Date.parse('2026-10-30T00:00:00Z');

const t = (key: string) => key;
const detail = (overrides: Partial<PackageDetail> = {}) =>
  ({
    kind: 'cask',
    token: 'example',
    displayName: 'Example',
    names: ['Example'],
    version: '1.0.0',
    homepage: 'https://example.com/',
    autoUpdates: true,
    supports: { status: 'known', arm64: true, x86_64: true },
    artifacts: { apps: [], binaries: [], pkgs: [], entries: [] },
    ...overrides
  }) as PackageDetail;
const rows = (pkg: PackageDetail) => packageSpec(pkg, null, null, t, 'en-US', SPEC_NOW).rows;

describe('desktop quick-info field parity', () => {
  it('restores Ely Office link version, source, GitHub, signature and Bundle IDs', () => {
    const spec = rows(
      detail({
        kind: 'link',
        token: 'ely-office',
        version: '0.1.12',
        link: {
          homepage: 'https://ely.example/',
          github: { repo: 'ELY-Open-Source/ElyOffice' },
          pricing: 'open_source',
          teamId: 'EXAMPLE123'
        },
        bundleIds: ['org.example.ely-office']
      } as Partial<PackageDetail>)
    );
    expect(spec.find(row => row.key === 'version')?.value).toBe('0.1.12');
    expect(spec.find(row => row.key === 'source')?.value).toBe('github.com/ELY-Open-Source/ElyOffice');
    expect(spec.find(row => row.key === 'openSource')?.href).toBe('https://github.com/ELY-Open-Source/ElyOffice');
    expect(spec.find(row => row.key === 'teamId')?.value).toBe('EXAMPLE123');
    expect(spec.find(row => row.key === 'bundle')).toMatchObject({
      label: 'package.spec.bundleId',
      value: 'org.example.ely-office'
    });
    expect(spec.find(row => row.key === 'homepage')?.href).toBe('https://ely.example/');
    expect(spec.some(row => ['auto', 'binaries', 'install'].includes(row.key))).toBe(false);
  });

  it.each(['cask', 'formula'] as const)(
    'preserves %s commands, uninstall notes, Rosetta, legacy versions and license',
    kind => {
      const spec = rows(
        detail({
          kind,
          license: 'MIT',
          names: ['Example', 'Former Example'],
          supports: { status: 'known', arm64: true, x86_64: true, requiresRosetta: true },
          artifacts: {
            apps: [],
            binaries: ['example', 'example-helper'],
            pkgs: [],
            entries: [],
            uninstallNotes: ['Stops the helper']
          },
          platforms: [{ tag: 'intel', version: '0.9.0' } as NonNullable<PackageDetail['platforms']>[number]]
        } as Partial<PackageDetail>)
      );
      expect(spec.find(row => row.key === 'binaries')).toMatchObject({ value: 'example example-helper', mono: true });
      expect(spec.find(row => row.key === 'uninstall')?.value).toBe('Stops the helper');
      expect(spec.find(row => row.key === 'rosetta')?.value).toBe('Requires Rosetta');
      expect(spec.find(row => row.key === 'platformVersions')?.value).toBe(
        'Older versions available on some platforms'
      );
      expect(spec.find(row => row.key === 'license')?.value).toBe('MIT');
      expect(spec.find(row => row.key === 'names')?.value).toBe('Former Example');
      expect(spec.some(row => row.key === 'auto')).toBe(kind === 'cask');
    }
  );

  it('uses the repository fallback URL for stars when the repository has no URL', () => {
    const pkg = detail({
      openSource: {
        repo: 'acme/fallback',
        url: 'https://github.com/acme/fallback',
        license: 'MIT',
        archived: false,
        checkedAt: '2026-10-01T00:00:00Z'
      } as PackageDetail['openSource'],
      repository: { stars: 1000, archived: false } as PackageDetail['repository']
    });
    expect(rows(pkg).find(row => row.key === 'openSource')?.stars?.href).toBe('https://github.com/acme/fallback');
  });

  it('passes the repository age to the i18n plural selector', () => {
    const pkg = detail({
      repository: {
        url: 'https://github.com/acme/one-day',
        stars: 5,
        pushedAt: '2026-10-29T00:00:00Z'
      } as PackageDetail['repository']
    });
    const translate = (key: string, named: Record<string, unknown> = {}, plural?: number) =>
      key === 'package.repo.updated' ? `Updated ${named.days} ${plural === 1 ? 'day' : 'days'} ago` : key;
    expect(
      packageSpec(pkg, null, null, translate as never, 'en-US', SPEC_NOW).rows.find(row => row.key === 'openSource')
        ?.value
    ).toContain('Updated 1 day ago');
  });

  it.each([
    [{ status: 'unknown', arm64: false, x86_64: false }, 'Not confirmed'],
    [{ status: 'known', arm64: false, x86_64: false }, 'No declared macOS support']
  ] as const)('keeps the architecture explanation for %j', (supports, expected) => {
    expect(rows(detail({ supports })).find(row => row.key === 'chip')?.value).toBe(expected);
  });

  it('keeps font metadata and installed version/location as distinct facts', () => {
    const pkg = detail({ isFont: true });
    pkg.artifacts.entries = [
      {
        type: 'font',
        phase: 'install',
        sources: ['Example.otf', 'Example.otf'],
        target: null,
        declaration: { font: ['Example.otf'] }
      }
    ];
    const spec = packageSpec(
      pkg,
      null,
      { actualVersion: '0.9.0', appPaths: ['~/Library/Fonts/Example.otf'] },
      t,
      'en-US',
      SPEC_NOW
    ).rows;
    expect(spec.find(row => row.key === 'files')?.value).toBe('1 · OTF');
    expect(spec.find(row => row.key === 'fontLocation')?.value).toBe('~/Library/Fonts');
    expect(spec.find(row => row.key === 'installedVersion')?.value).toBe('0.9.0');
    expect(spec.find(row => row.key === 'location')?.value).toBe('~/Library/Fonts/Example.otf');
  });

  it.each(['cask', 'formula', 'link'] as const)('preserves local %s info while offline', kind => {
    const local = {
      kind,
      token: 'example',
      name: 'Example',
      names: [],
      version: '1.0.0',
      homepage: 'https://example.com/',
      autoUpdates: true,
      binaries: ['example'],
      bundleIds: ['org.example.app'],
      link: kind === 'link' ? { pricing: 'free', teamId: 'EXAMPLE123', github: { repo: 'acme/example' } } : null
    } as unknown as LocalItem;
    const spec = packageSpec(null, local, null, t, 'en-US', SPEC_NOW);
    expect(spec.rows.find(row => row.key === 'token')?.value).toBe('example');
    expect(spec.rows.find(row => row.key === 'homepage')?.href).toBe('https://example.com/');
    expect(spec.rows.find(row => row.key === (kind === 'link' ? 'bundle' : 'binaries'))?.value).toBe(
      kind === 'link' ? 'org.example.app' : 'example'
    );
    expect(spec.checkedAt).toBeNull();
  });
});

describe('desktop alternate names', () => {
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
    ['es-ES', latinAliases],
    ['pt-BR', latinAliases],
    ['ru-RU', latinAliases],
    ['de-DE', latinAliases],
    ['fr-FR', latinAliases],
    ['it-IT', latinAliases],
    ['ja-JP', japaneseAliases],
    ['zh-CN', allAliases],
    ['zh-Hant', allAliases]
  ] as const)('filters aliases by page locale in %s for all package kinds', (locale, expected) => {
    for (const kind of ['cask', 'formula', 'link'] as const) {
      const pkg = detail({ kind, displayName: 'Visual Studio Code', names });
      const local = { ...pkg, name: pkg.displayName } as unknown as LocalItem;
      for (const spec of [
        packageSpec(pkg, null, null, t, locale, SPEC_NOW),
        packageSpec(null, local, null, t, locale, SPEC_NOW)
      ])
        expect(spec.rows.find(row => row.key === 'names')?.value).toBe(expected.join(', '));
    }
  });

  it.each(['C++', 'Notepad++', 'Ｃ＋＋', '$', '®™'])('keeps the symbol alias %s on en, de and ja pages', alias => {
    for (const locale of ['en-US', 'de-DE', 'ja-JP'] as const) {
      const pkg = detail({ kind: 'cask', displayName: 'Editor', names: ['Editor', alias] });
      expect(packageSpec(pkg, null, null, t, locale, SPEC_NOW).rows.find(row => row.key === 'names')?.value).toBe(
        alias
      );
    }
  });

  it('keeps fullwidth Latin on en, de and ja pages', () => {
    for (const locale of ['en-US', 'de-DE', 'ja-JP'] as const) {
      const pkg = detail({ kind: 'cask', displayName: 'Editor', names: ['Editor', 'ＶＳ Ｃｏｄｅ'] });
      expect(packageSpec(pkg, null, null, t, locale, SPEC_NOW).rows.find(row => row.key === 'names')?.value).toBe(
        'ＶＳ Ｃｏｄｅ'
      );
    }
  });

  it('shows halfwidth katakana on Japanese pages but hides it elsewhere', () => {
    const pkg = detail({ kind: 'cask', displayName: 'Editor', names: ['Editor', 'ｺｰﾄﾞ'] });
    expect(packageSpec(pkg, null, null, t, 'ja-JP', SPEC_NOW).rows.find(row => row.key === 'names')?.value).toBe('ｺｰﾄﾞ');
    expect(packageSpec(pkg, null, null, t, 'en-US', SPEC_NOW).rows.some(row => row.key === 'names')).toBe(false);
    expect(packageSpec(pkg, null, null, t, 'de-DE', SPEC_NOW).rows.some(row => row.key === 'names')).toBe(false);
  });

  it('omits the names row when every alias is filtered out', () => {
    for (const kind of ['cask', 'formula', 'link'] as const) {
      for (const locale of ['en-US', 'ja-JP'] as const) {
        const pkg = detail({ kind, displayName: 'Editor', names: ['Editor', '編集', 'Редактор'] });
        expect(packageSpec(pkg, null, null, t, locale, SPEC_NOW).rows.some(row => row.key === 'names')).toBe(false);
      }
    }
  });

  it('renders the filtered aliases in the spec card', () => {
    const english = packageSpec(detail({ displayName: 'Visual Studio Code', names }), null, null, t, 'en-US', SPEC_NOW);
    const enCard = mount(OnPackageSpecCard, { props: { rows: english.rows } });
    try {
      expect(enCard.text()).toContain('vscode');
      expect(enCard.text()).not.toContain('VS Code 编辑器');
      expect(enCard.text()).not.toContain('VS Code エディター');
    } finally {
      enCard.unmount();
    }

    const japanese = packageSpec(
      detail({ displayName: 'Visual Studio Code', names }),
      null,
      null,
      t,
      'ja-JP',
      SPEC_NOW
    );
    const jaCard = mount(OnPackageSpecCard, { props: { rows: japanese.rows } });
    try {
      expect(jaCard.text()).toContain('VS Code エディター');
      expect(jaCard.text()).toContain('コード');
      expect(jaCard.text()).not.toContain('編集');
    } finally {
      jaCard.unmount();
    }
  });
});

describe('QA spec card regressions', () => {
  it('falls back to the first verified identity for identity-only open-source evidence', () => {
    const pkg = detail({
      facts: {
        overview: { artifacts: { entries: [] } },
        security: {
          identities: [
            {
              channels: [],
              openSource: 'yes',
              repositoryUrl: 'https://github.com/acme/unverified',
              evidence: { status: 'stale', checkedAt: '2026-09-01T00:00:00Z' }
            },
            {
              channels: [],
              openSource: 'yes',
              repositoryUrl: 'https://github.com/acme/identity-only',
              evidence: { status: 'verified', checkedAt: '2026-10-01T00:00:00Z' }
            }
          ]
        }
      }
    } as unknown as Partial<PackageDetail>);
    const spec = packageSpec(pkg, null, null, t, 'en-US', SPEC_NOW);
    expect(spec.rows.find(row => row.key === 'openSource')).toMatchObject({
      value: 'package.spec.openSource',
      href: 'https://github.com/acme/identity-only'
    });
  });

  it('renders no link for javascript: repository URLs', () => {
    const pkg = Object.assign(detail(), {
      repository: { url: 'javascript:alert(1)', stars: 42 },
      homepage: 'https://example.com/'
    });
    const spec = packageSpec(pkg, null, null, t, 'en-US', SPEC_NOW);
    const wrapper = mount(OnPackageSpecCard, { props: { rows: spec.rows } });
    try {
      expect(wrapper.html()).not.toContain('javascript:');
      expect(wrapper.findAll('a').map(link => link.attributes('href'))).toEqual(['https://example.com/']);
    } finally {
      wrapper.unmount();
    }
  });

  it('drops unsafe homepage and stars hrefs before rendering', () => {
    const pkg = Object.assign(detail(), {
      repository: { url: 'javascript:alert(1)', stars: 42 },
      homepage: 'javascript:alert(2)'
    });
    const spec = packageSpec(pkg, null, null, t, 'en-US', SPEC_NOW);
    expect(spec.rows.find(row => row.key === 'openSource')?.href).toBeUndefined();
    expect(spec.rows.find(row => row.key === 'openSource')?.stars?.href).toBeUndefined();
    expect(spec.rows.some(row => row.key === 'homepage')).toBe(false);
  });

  it.each(['cask', 'formula', 'link'] as const)(
    'keeps a standalone top-level license without OSI data for %s',
    kind => {
      const pkg = detail({ kind, license: 'Apache-2.0', openSource: null });
      const spec = packageSpec(pkg, null, null, t, 'en-US', SPEC_NOW);
      expect(spec.rows.find(row => row.key === 'license')?.value).toBe('Apache-2.0');
      expect(spec.rows.some(row => row.key === 'openSource')).toBe(false);
    }
  );

  it('keeps stars but omits an invalid checked-date tooltip without throwing', () => {
    const pkg = Object.assign(detail(), {
      repository: { url: 'https://github.com/acme/example', stars: 12345, signalsCheckedAt: 'not-a-date' }
    });
    expect(() => packageSpec(pkg, null, null, t, 'en-US', SPEC_NOW)).not.toThrow();
    const spec = packageSpec(pkg, null, null, t, 'en-US', SPEC_NOW);
    expect(spec.rows.find(row => row.key === 'openSource')?.stars).toMatchObject({
      label: '★12.3K',
      checked: undefined
    });
  });

  it('localizes repository stars with the interface locale', () => {
    const pkg = Object.assign(detail(), { repository: { url: 'https://github.com/acme/example', stars: 12345 } });
    expect(
      packageSpec(pkg, null, null, t, 'fr-FR', SPEC_NOW).rows.find(row => row.key === 'openSource')?.stars?.label
    ).toBe('★12,3\u00a0k');
  });
});

describe('product star formatting', () => {
  it.each([
    ['en-US', 1_000, '1K'],
    ['en-US', 12_345, '12.3K'],
    ['en-US', 184_400, '184.4K'],
    ['en-US', 999_949, '999.9K'],
    ['en-US', 999_950, '1M'],
    ['en-US', 1_200_000, '1.2M'],
    ['zh-CN', 1_000, '1,000'],
    ['zh-CN', 12_345, '1.2万'],
    ['zh-CN', 999_950, '100万'],
    ['ja-JP', 1_000, '1,000'],
    ['ja-JP', 12_345, '1.2万'],
    ['ja-JP', 999_950, '100万'],
    ['de-DE', 1_000, '1.000'],
    ['de-DE', 12_345, '12.345'],
    ['de-DE', 999_950, '1\u00a0Mio.']
  ] as const)('%s formats %s stars on the desktop detail page', (locale, stars, expected) => {
    const spec = packageSpec(
      detail({ repository: { url: 'https://github.com/acme/stars', stars, archived: false } }),
      null,
      null,
      t,
      locale,
      SPEC_NOW
    );
    const label = spec.rows.find(row => row.key === 'openSource')?.stars?.label;
    expect(label).toBe(`★${expected}`);
    expect(label).not.toMatch(/1000[.,]0/);
  });
});

describe('desktop link price types', () => {
  it.each(['free_with_iap', 'trial'] as const)('localises a %s link price', pricing => {
    const pkg = detail({
      kind: 'link',
      link: { pricing, homepage: 'https://example.com/' }
    } as unknown as Partial<PackageDetail>);
    expect(rows(pkg).find(row => row.key === 'price')?.value).toBe(`package.link.pricing.${pricing}`);
  });

  it.each(['free_with_iap', 'trial'] as const)('localises a verified %s detail price', type => {
    const pkg = detail({ price: { type } } as unknown as Partial<PackageDetail>);
    expect(rows(pkg).find(row => row.key === 'price')?.value).toBe(`package.link.pricing.${type}`);
  });

  it.each([
    ['en-US', enUS],
    ['zh-CN', zhCN],
    ['zh-Hant', zhHant],
    ['ja-JP', jaJP],
    ['es-ES', esES],
    ['pt-BR', ptBR],
    ['ru-RU', ruRU],
    ['de-DE', deDE],
    ['fr-FR', frFR],
    ['it-IT', itIT]
  ] as const)('has both link price labels in %s', (_locale, messages) => {
    const pricing = messages.package.link.pricing as Record<string, string>;
    for (const key of ['free_with_iap', 'trial']) expect(pricing[key]).toBeTruthy();
  });

  // The label names a field, so it is translated; the identifier under it is the app's own and stays raw.
  it.each([
    ['en-US', enUS],
    ['zh-CN', zhCN],
    ['zh-Hant', zhHant],
    ['ja-JP', jaJP],
    ['es-ES', esES],
    ['pt-BR', ptBR],
    ['ru-RU', ruRU],
    ['de-DE', deDE],
    ['fr-FR', frFR],
    ['it-IT', itIT]
  ] as const)('localises the Bundle ID labels in %s', (locale, messages) => {
    const catalog = messages as unknown as {
      package: { spec: { bundleId: string; token: string } };
      appStore: { info: { bundleId: string } };
      appInfo: { fields: { bundleId: string } };
    };
    const labels = [catalog.package.spec.bundleId, catalog.appStore.info.bundleId, catalog.appInfo.fields.bundleId];
    for (const label of labels) expect(label.trim()).toBeTruthy();
    for (const label of labels) {
      // Every locale but English must actually translate it: the identical English string was the #87 defect.
      if (locale !== 'en-US') expect(label).not.toBe('Bundle ID');
      // "Bundle ID" is the Xcode term; a machine-translated "identifier" sentence is not a label.
      expect(label.length).toBeLessThanOrEqual(20);
    }
    // The package token is a different row in the same card; one language must not name both the same way.
    expect(catalog.package.spec.bundleId).not.toBe(catalog.package.spec.token);
    // All three surfaces read the same term in one language, so the app never names the field two ways.
    expect(new Set(labels).size).toBe(1);
  });

  it('keeps the Bundle ID value raw while the label is translated', () => {
    const spec = packageSpec(
      detail({ kind: 'link', bundleIds: ['org.example.ely-office'] } as Partial<PackageDetail>),
      null,
      null,
      (key: string) => (key === 'package.spec.bundleId' ? '应用包标识符' : key),
      'zh-CN',
      SPEC_NOW
    ).rows;
    expect(spec.find(row => row.key === 'bundle')).toMatchObject({
      label: '应用包标识符',
      value: 'org.example.ely-office',
      mono: true
    });
  });
});
