import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { packageSpec } from '../app/utils/packageSpec';
import type { PackagePage } from '../app/utils/packageFacts';

const SPEC_NOW = Date.parse('2026-10-30T00:00:00Z');

const t = (key: string, named: Record<string, unknown> = {}) => {
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
    'package.spec.token': 'Token',
    'package.spec.yes': 'Yes',
    'package.spec.no': 'No',
    'package.stats.latest': 'Latest version',
    'package.quick.source': 'Download source',
    'package.repo.stars': '★',
    'package.repo.updated': `Updated ${named.days ?? 0} days ago`,
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
    expect(repoRow.stars).toMatchObject({ label: '★1.0K', checked: undefined });
  });

  it('hides the repository row when neither repository nor OSI data exists', () => {
    const result = packageSpec(page(), t, 'en-US', SPEC_NOW);
    expect(result.rows.some(row => row.key === 'openSource')).toBe(false);
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
    expect(rows.find(row => row.key === 'bundle')?.value).toBe('org.example.ely-office');
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
  const locales = ['en-US', 'zh-CN', 'zh-Hant', 'ja-JP', 'es-ES', 'pt-BR', 'ru-RU', 'de-DE', 'fr-FR', 'it-IT'] as const;
  it.each(locales)('filters aliases in %s for all package kinds', locale => {
    for (const kind of ['cask', 'formula', 'link'] as const) {
      const pkg = page({
        kind,
        displayName: 'Visual Studio Code',
        names: ['Visual Studio Code', 'vscode', 'VS Code 编辑器']
      });
      const value = packageSpec(pkg, t, locale, SPEC_NOW).rows.find(row => row.key === 'names')?.value;
      expect(value).toBe(['zh-CN', 'zh-Hant', 'ja-JP'].includes(locale) ? 'vscode, VS Code 编辑器' : 'vscode');
      pkg.names = ['Visual Studio Code', 'VS Code 编辑器'];
      expect(packageSpec(pkg, t, locale, SPEC_NOW).rows.some(row => row.key === 'names')).toBe(
        ['zh-CN', 'zh-Hant', 'ja-JP'].includes(locale)
      );
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
