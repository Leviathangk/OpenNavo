import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { OnPackageSpecCard } from '@opennavo/ui';
import { packageSpec } from '../app/utils/packageSpec';
import { SITE_LOCALES } from '@opennavo/shared';
import { siteMessage } from '../server/utils/siteCopy';
import type { PackagePage } from '../app/utils/packageFacts';

const SPEC_NOW = Date.parse('2026-10-30T00:00:00Z');

const identityOnlyFixture = {
  channels: [],
  openSource: 'yes',
  repositoryUrl: 'https://github.com/acme/identity-only',
  evidence: { status: 'verified', checkedAt: '2026-10-01T00:00:00Z' }
};

const t = (key: string, named: Record<string, unknown> = {}, plural?: number) => {
  const values: Record<string, string> = {
    'package.spec.price': 'Price',
    'package.spec.system': 'System',
    'package.spec.chip': 'Chip',
    'package.spec.autoUpdates': 'Auto-updates',
    'package.spec.openSource': 'Open source',
    'package.spec.repository': 'Repository',
    'package.spec.githubRepository': 'GitHub repository',
    'package.spec.developer': 'Developer',
    'package.spec.homepage': 'Homepage',
    'package.spec.bundleId': 'Bundle ID',
    'package.spec.token': 'Token',
    'package.spec.yes': 'Yes',
    'package.spec.no': 'No',
    'package.stats.latest': 'Latest version',
    'package.quick.source': 'Download source',
    'package.repo.stars': '★',
    'package.repo.updated': `Updated ${named.days ?? 0} ${plural === 1 ? 'day' : 'days'} ago`,
    'package.repo.checked': `Repository data checked ${named.date}.`
  };
  return values[key] ?? key;
};

function page(overrides: Record<string, unknown> = {}): PackagePage {
  return {
    kind: 'cask',
    token: 'fixture-app',
    displayName: 'Fixture App',
    names: ['Fixture App'],
    version: '1.0.0',
    supports: { status: 'known', arm64: true, x86_64: true },
    autoUpdates: false,
    developer: 'Example',
    homepage: 'https://example.com/',
    repoUrl: null,
    openSource: null,
    facts: {
      requirements: [],
      uninstall: [],
      overview: {
        version: '1.0.0',
        aliases: [],
        dependencyCount: 0,
        conflictCount: 0,
        artifacts: { apps: [], binaries: [], pkgs: [], entries: [] },
        downloadUrl: null,
        sha256: null,
        caveats: null
      },
      alternatives: [],
      prices: [],
      offers: []
    },
    ...overrides
  } as PackagePage;
}

describe('package specification repository rows', () => {
  it('renders OSI license plus repository stars', () => {
    const result = packageSpec(
      page({
        openSource: {
          repo: 'acme/osi',
          url: 'https://github.com/acme/osi',
          license: 'MIT',
          archived: false,
          checkedAt: '2026-10-01T00:00:00Z'
        },
        repository: {
          url: 'https://github.com/acme/osi',
          stars: 12345,
          pushedAt: '2026-10-01T00:00:00Z',
          archived: false,
          signalsCheckedAt: '2026-10-02T00:00:00Z'
        }
      }),
      t,
      'en-US',
      SPEC_NOW
    );
    const repoRow = result.rows.find(row => row.key === 'openSource')!;
    expect(repoRow.value).toContain('Open source · MIT · Updated');
    expect(repoRow.value).toContain('Updated 29 days ago');
    expect(repoRow.value).not.toContain('★');
    expect(repoRow.stars).toMatchObject({ label: '★12.3K', href: 'https://github.com/acme/osi' });
    expect(repoRow.stars?.checked).toMatch(/Repository data checked.*2026/);
    expect(result.checkedAt).toBeNull();
  });

  it('renders a public repository without claiming an OSI license', () => {
    const result = packageSpec(
      page({
        repository: { url: 'https://github.com/acme/public-only', stars: 1000, archived: false }
      }),
      t,
      'en-US',
      SPEC_NOW
    );
    const repoRow = result.rows.find(row => row.key === 'openSource')!;
    expect(repoRow.value).toBe('GitHub repository');
    expect(repoRow.stars).toMatchObject({ label: '★1K', checked: undefined });
  });

  it('uses the repository fallback URL for stars when the repository has no URL', () => {
    const result = packageSpec(
      page({
        openSource: {
          repo: 'acme/fallback',
          url: 'https://github.com/acme/fallback',
          license: 'MIT',
          archived: false,
          checkedAt: '2026-10-01T00:00:00Z'
        },
        repository: { stars: 1000, archived: false }
      }),
      t,
      'en-US',
      SPEC_NOW
    );
    const repoRow = result.rows.find(row => row.key === 'openSource')!;
    expect(repoRow.stars?.href).toBe('https://github.com/acme/fallback');
  });

  it('uses the singular plural form for a one-day repository age', () => {
    const result = packageSpec(
      page({ repository: { url: 'https://github.com/acme/one-day', stars: 5, pushedAt: '2026-10-29T00:00:00Z' } }),
      t,
      'en-US',
      SPEC_NOW
    );
    expect(result.rows.find(row => row.key === 'openSource')?.value).toContain('Updated 1 day ago');
  });

  it.each([
    ['en-US', 1, '2026-10-29T00:00:00Z', 'Updated 1 day ago'],
    ['en-US', 2, '2026-10-28T00:00:00Z', 'Updated 2 days ago'],
    ['de-DE', 1, '2026-10-29T00:00:00Z', 'Vor 1 Tag aktualisiert'],
    ['de-DE', 2, '2026-10-28T00:00:00Z', 'Vor 2 Tagen aktualisiert']
  ] as const)('selects the %s plural form for a %s-day repository age', (locale, _days, pushedAt, expected) => {
    const translate = (key: string, named?: Record<string, unknown>, plural?: number) =>
      siteMessage(locale, key, named as Record<string, string | number>, plural);
    const result = packageSpec(
      page({ repository: { url: 'https://github.com/acme/plural', stars: 5, pushedAt } }),
      translate,
      locale,
      SPEC_NOW
    );
    expect(result.rows.find(row => row.key === 'openSource')?.value).toContain(expected);
  });

  it('hides the repository row when neither repository nor OSI data exists', () => {
    const result = packageSpec(page(), t, 'en-US', SPEC_NOW);
    expect(result.rows.some(row => row.key === 'openSource')).toBe(false);
  });

  it('falls back to the first verified identity for identity-only open-source evidence', () => {
    const pkg = page({
      facts: {
        ...page().facts,
        security: {
          identities: [
            {
              channels: [],
              openSource: 'yes',
              repositoryUrl: 'https://github.com/acme/unverified',
              evidence: { status: 'stale', checkedAt: '2026-09-01T00:00:00Z' }
            },
            identityOnlyFixture
          ]
        }
      }
    });
    const result = packageSpec(pkg, t, 'en-US', SPEC_NOW);
    expect(result.rows.find(row => row.key === 'openSource')).toMatchObject({
      value: 'Open source',
      href: identityOnlyFixture.repositoryUrl
    });
    expect(result.checkedAt?.iso).toBe('2026-10-01');
  });

  it('renders no link for javascript: repository URLs', async () => {
    const pkg = page({
      repository: { url: 'javascript:alert(1)', stars: 42 },
      homepage: 'https://example.com/'
    });
    const rows = packageSpec(pkg, t, 'en-US', SPEC_NOW).rows;
    const html = await renderToString(createSSRApp({ render: () => h(OnPackageSpecCard, { rows }) }));
    expect(html).not.toContain('javascript:');
    expect(html).toContain('href="https://example.com/"');
    expect(html).toContain('GitHub repository');
  });

  it('drops unsafe homepage and stars hrefs before rendering', () => {
    const pkg = page({
      repository: { url: 'javascript:alert(1)', stars: 42 },
      homepage: 'javascript:alert(2)'
    });
    const rows = packageSpec(pkg, t, 'en-US', SPEC_NOW).rows;
    const repository = rows.find(row => row.key === 'openSource')!;
    expect(repository.href).toBeUndefined();
    expect(repository.stars?.href).toBeUndefined();
    expect(rows.some(row => row.key === 'homepage')).toBe(false);
  });

  it('falls back only to OSI archived and never to the OSI signal check date', () => {
    const osi = {
      repo: 'acme/osi',
      url: 'https://github.com/acme/osi',
      license: 'MIT',
      archived: true,
      checkedAt: '2020-01-01T00:00:00Z'
    };
    const fallback = packageSpec(page({ openSource: osi, repository: { stars: 999 } }), t, 'en-US', SPEC_NOW);
    expect(fallback.rows.find(row => row.key === 'openSource')?.badge?.label).toBe('package.repo.archived');
    expect(fallback.repoSignals?.checkedAt).toBeUndefined();
    expect(fallback.checkedAt).toBeNull();
    const current = packageSpec(page({ openSource: osi, repository: { archived: false } }), t, 'en-US', SPEC_NOW);
    expect(current.rows.find(row => row.key === 'openSource')?.badge).toBeUndefined();
  });
});

describe('quick-info field parity with main', () => {
  it('restores Ely Office link version and download source', () => {
    const pkg = page({
      kind: 'link',
      token: 'ely-office',
      version: '0.1.12',
      facts: { ...page().facts, overview: { ...page().facts.overview, version: '0.1.12' } },
      link: { github: { repo: 'ELY-Open-Source/ElyOffice' }, pricing: 'unknown' },
      bundleIds: ['org.example.ely-office']
    });
    const rows = packageSpec(pkg, t, 'en-US', SPEC_NOW).rows;
    expect(rows.find(row => row.key === 'version')).toMatchObject({ label: 'Latest version', value: '0.1.12' });
    expect(rows.find(row => row.key === 'source')).toMatchObject({
      label: 'Download source',
      value: 'github.com/ELY-Open-Source/ElyOffice'
    });
    expect(rows.find(row => row.key === 'openSource')?.href).toBe('https://github.com/ELY-Open-Source/ElyOffice');
    expect(rows.find(row => row.key === 'bundle')).toMatchObject({
      label: 'Bundle ID',
      value: 'org.example.ely-office'
    });
    expect(rows.some(row => ['auto', 'homebrew', 'install'].includes(row.key))).toBe(false);
  });

  it.each(['cask', 'formula', 'link'] as const)(
    'preserves verified developer, signature, license and alternate names for %s',
    kind => {
      const pkg = page({
        kind,
        license: 'Apache-2.0',
        names: ['Fixture App', 'Former App'],
        facts: {
          ...page().facts,
          security: {
            identities: [
              {
                developer: 'Verified Developer',
                teamId: 'EXAMPLE123',
                evidence: { status: 'verified', checkedAt: '2026-10-01T00:00:00Z' }
              }
            ]
          }
        }
      });
      const rows = packageSpec(pkg, t, 'en-US', SPEC_NOW).rows;
      expect(rows.find(row => row.key === 'developer')?.value).toBe('Verified Developer');
      expect(rows.find(row => row.key === 'teamId')?.value).toBe('EXAMPLE123');
      expect(rows.find(row => row.key === 'license')?.value).toBe('Apache-2.0');
      expect(rows.find(row => row.key === 'names')?.value).toBe('Former App');
      expect(rows.some(row => row.key === 'homebrew')).toBe(kind === 'cask');
      expect(rows.some(row => row.key === 'install')).toBe(false);
    }
  );

  it('preserves font files, formats and the declared install location', () => {
    const pkg = page({ isFont: true });
    pkg.facts.overview.artifacts.entries = [
      {
        type: 'font',
        phase: 'install',
        sources: ['Example.otf', 'Example.ttf', 'Example.otf'],
        target: null,
        declaration: { font: ['Example.otf', 'Example.ttf'] }
      }
    ];
    const rows = packageSpec(pkg, t, 'en-US', SPEC_NOW).rows;
    expect(rows.find(row => row.key === 'files')?.value).toBe('2 · OTF, TTF');
    expect(rows.find(row => row.key === 'location')).toMatchObject({ value: '~/Library/Fonts', mono: true });
    expect(rows.some(row => ['chip', 'macos'].includes(row.key))).toBe(false);
  });

  it('includes a release date only for the current link version and omits unknown versions', () => {
    const pkg = page({ kind: 'link', latestRelease: { version: '1.0.0', publishedAt: '2025-10-01T00:00:00Z' } });
    expect(packageSpec(pkg, t, 'en-US', SPEC_NOW).rows.find(row => row.key === 'version')?.value).toContain('2025');
    pkg.latestRelease!.version = '0.9.0';
    expect(packageSpec(pkg, t, 'en-US', SPEC_NOW).rows.find(row => row.key === 'version')?.value).toBe('1.0.0');
    pkg.facts.overview.version = 'latest';
    expect(packageSpec(pkg, t, 'en-US', SPEC_NOW).rows.some(row => row.key === 'version')).toBe(false);
  });
});

describe('spec card alternate names', () => {
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
      const pkg = page({ kind, displayName: 'Visual Studio Code', names });
      expect(packageSpec(pkg, t, locale, SPEC_NOW).rows.find(row => row.key === 'names')?.value).toBe(
        expected.join(', ')
      );
    }
  });
  it('hides a kanji-only alias on Japanese pages and shows a kana alias', () => {
    const pkg = page({ kind: 'cask', displayName: 'Visual Studio Code', names });
    const value = packageSpec(pkg, t, 'ja-JP', SPEC_NOW).rows.find(row => row.key === 'names')?.value;
    expect(value).toContain('コード');
    expect(value).not.toContain('編集');
  });
  it.each(['C++', 'Notepad++', 'Ｃ＋＋', '$', '®™'])('keeps the symbol alias %s on en, de and ja pages', alias => {
    for (const locale of ['en-US', 'de-DE', 'ja-JP'] as const) {
      const pkg = page({ kind: 'cask', displayName: 'Editor', names: ['Editor', alias] });
      expect(packageSpec(pkg, t, locale, SPEC_NOW).rows.find(row => row.key === 'names')?.value).toBe(alias);
    }
  });
  it('keeps fullwidth Latin on en, de and ja pages', () => {
    for (const locale of ['en-US', 'de-DE', 'ja-JP'] as const) {
      const pkg = page({ kind: 'cask', displayName: 'Editor', names: ['Editor', 'ＶＳ Ｃｏｄｅ'] });
      expect(packageSpec(pkg, t, locale, SPEC_NOW).rows.find(row => row.key === 'names')?.value).toBe('ＶＳ Ｃｏｄｅ');
    }
  });
  it('shows halfwidth katakana on Japanese pages but hides it elsewhere', () => {
    const pkg = page({ kind: 'cask', displayName: 'Editor', names: ['Editor', 'ｺｰﾄﾞ'] });
    expect(packageSpec(pkg, t, 'ja-JP', SPEC_NOW).rows.find(row => row.key === 'names')?.value).toBe('ｺｰﾄﾞ');
    expect(packageSpec(pkg, t, 'en-US', SPEC_NOW).rows.some(row => row.key === 'names')).toBe(false);
    expect(packageSpec(pkg, t, 'de-DE', SPEC_NOW).rows.some(row => row.key === 'names')).toBe(false);
  });
  it('omits the names row when every alias is filtered out', () => {
    for (const kind of ['cask', 'formula', 'link'] as const) {
      for (const locale of ['en-US', 'ja-JP'] as const) {
        const pkg = page({ kind, displayName: 'Editor', names: ['Editor', '編集', 'Редактор'] });
        expect(packageSpec(pkg, t, locale, SPEC_NOW).rows.some(row => row.key === 'names')).toBe(false);
      }
    }
  });
});

describe('QA spec card regressions', () => {
  it.each(['en-US', 'zh-CN', 'zh-Hant', 'ja-JP', 'es-ES', 'pt-BR', 'ru-RU', 'de-DE', 'fr-FR', 'it-IT'] as const)(
    'renders repository checked-date text from real messages in %s',
    locale => {
      const messages = JSON.parse(readFileSync(new URL(`../i18n/locales/${locale}.json`, import.meta.url), 'utf8'));
      const translate = (key: string, named: Record<string, unknown> = {}) => {
        const message = key.split('.').reduce((node, part) => node?.[part], messages);
        expect(typeof message, key).toBe('string');
        return message.replace(/\{(\w+)\}/g, (_: string, name: string) => String(named[name]));
      };
      const pkg = page({
        repository: { url: 'https://github.com/acme/example', stars: 42, signalsCheckedAt: '2026-10-02T00:00:00Z' }
      });
      const checked = packageSpec(pkg, translate, locale, SPEC_NOW).rows.find(row => row.key === 'openSource')?.stars
        ?.checked;
      expect(checked).toBeTruthy();
      expect(checked).not.toMatch(/package\.repo\.checked|\{date\}|undefined/);
      expect(checked).toContain('2026');
    }
  );
  it.each(['cask', 'formula', 'link'] as const)(
    'keeps a standalone top-level license without OSI data for %s',
    kind => {
      const pkg = page({ kind, license: 'Apache-2.0', openSource: null });
      const spec = packageSpec(pkg, t, 'en-US', SPEC_NOW);
      expect(spec.rows.find(row => row.key === 'license')?.value).toBe('Apache-2.0');
      expect(spec.rows.some(row => row.key === 'openSource')).toBe(false);
    }
  );

  it('keeps stars but omits an invalid checked-date tooltip without throwing', () => {
    const pkg = page({
      repository: { url: 'https://github.com/acme/example', stars: 12345, signalsCheckedAt: 'not-a-date' }
    });
    expect(() => packageSpec(pkg, t, 'en-US', SPEC_NOW)).not.toThrow();
    const spec = packageSpec(pkg, t, 'en-US', SPEC_NOW);
    expect(spec.rows.find(row => row.key === 'openSource')?.stars).toMatchObject({
      label: '★12.3K',
      checked: undefined
    });
  });

  it('localizes repository stars with the interface locale', () => {
    const pkg = page({ repository: { url: 'https://github.com/acme/example', stars: 12345 } });
    expect(packageSpec(pkg, t, 'fr-FR', SPEC_NOW).rows.find(row => row.key === 'openSource')?.stars?.label).toBe(
      '★12,3\u00a0k'
    );
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
  ] as const)('%s formats %s stars on the web detail page', (locale, stars, expected) => {
    const spec = packageSpec(
      page({ repository: { url: 'https://github.com/acme/stars', stars, archived: false } }),
      t,
      locale,
      SPEC_NOW
    );
    const label = spec.rows.find(row => row.key === 'openSource')?.stars?.label;
    expect(label).toBe(`★${expected}`);
    expect(label).not.toMatch(/1000[.,]0/);
  });
});

describe('identity-only source evidence', () => {
  const identity = (openSource: 'yes' | 'no' | 'unknown', repositoryUrl?: string) => ({
    channels: [],
    openSource,
    ...(repositoryUrl ? { repositoryUrl } : {}),
    evidence: { status: 'verified', checkedAt: '2026-10-01T00:00:00Z' }
  });

  it('leaves a closed source to the security facts block instead of the repository answer', () => {
    // Product: a closed identity is a neutral fact in PackageSafety; this card answers where a repository is.
    const pkg = page({ facts: { ...page().facts, security: { identities: [identity('no')] } } });
    expect(packageSpec(pkg, t, 'en-US', SPEC_NOW).rows.some(row => row.key === 'openSource')).toBe(false);
  });

  it('keeps a closed identity from hiding a repository the catalog already states', () => {
    const pkg = page({
      repository: { url: 'https://github.com/acme/public', stars: 12 },
      facts: { ...page().facts, security: { identities: [identity('no')] } }
    });
    const row = packageSpec(pkg, t, 'en-US', SPEC_NOW).rows.find(item => item.key === 'openSource')!;
    expect(row.value).toBe('GitHub repository');
    expect(row.href).toBe('https://github.com/acme/public');
  });

  it('still prefers an open identity over a closed one in the same record', () => {
    const pkg = page({
      facts: {
        ...page().facts,
        security: { identities: [identity('no'), identity('yes', 'https://github.com/acme/open')] }
      }
    });
    const row = packageSpec(pkg, t, 'en-US', SPEC_NOW).rows.find(item => item.key === 'openSource')!;
    expect(row).toMatchObject({ value: 'Open source', href: 'https://github.com/acme/open' });
  });

  it('renders the repository an identity gives without an open-source claim', () => {
    // The guard is "an open identity or a repository url": a record that merely links a repository still
    // answers this card's question, and a closed record beside it does not turn that answer into "open".
    const pkg = page({
      facts: {
        ...page().facts,
        security: { identities: [identity('unknown', 'https://github.com/acme/identity-repo')] }
      }
    });
    const row = packageSpec(pkg, t, 'en-US', SPEC_NOW).rows.find(item => item.key === 'openSource')!;
    expect(row).toMatchObject({ value: 'GitHub repository', href: 'https://github.com/acme/identity-repo' });
  });

  it('renders the open-source answer an identity states without a repository', () => {
    const pkg = page({ facts: { ...page().facts, security: { identities: [identity('yes')] } } });
    const row = packageSpec(pkg, t, 'en-US', SPEC_NOW).rows.find(item => item.key === 'openSource')!;
    expect(row.value).toBe('Open source');
    expect(row.href).toBeUndefined();
  });
});

describe('Bundle ID label', () => {
  it('is written in each language instead of the English "Bundle ID"', () => {
    for (const code of SITE_LOCALES) {
      const messages = JSON.parse(readFileSync(`i18n/locales/${code}.json`, 'utf8'));
      const label = messages.package.spec.bundleId;
      expect(label, code).toBeTruthy();
      if (code !== 'en-US') expect(label, code).not.toBe('Bundle ID');
      // The value under the label is a raw identifier, so the label stays short and free of placeholders.
      expect(label, code).not.toMatch(/[{}]/);
    }
  });

  it('does not reuse the package token label, so the two identifier rows stay apart', () => {
    // Both rows say "identifier" and Traditional Chinese could otherwise render them with one name.
    for (const code of SITE_LOCALES) {
      const messages = JSON.parse(readFileSync(`i18n/locales/${code}.json`, 'utf8'));
      expect(messages.package.spec.bundleId, code).not.toBe(messages.package.spec.token);
    }
  });
});
