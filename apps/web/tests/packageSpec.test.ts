import { describe, expect, it } from 'vitest';
import { packageSpec } from '../app/utils/packageSpec';
import type { PackagePage } from '../app/utils/packageFacts';

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
    'package.repo.stars': '★',
    'package.repo.updated': `Updated ${named.days ?? 0} days ago`,
    'package.faq.openSourceChecked': `Repository data checked ${named.date}.`
  };
  return values[key] ?? key;
};

function page(overrides: Record<string, unknown> = {}): PackagePage {
  return {
    kind: 'cask',
    token: 'fixture-app',
    displayName: 'Fixture App',
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
      'en-US'
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
      'en-US'
    );
    const repoRow = result.rows.find(row => row.key === 'openSource')!;
    expect(repoRow.value).toBe('GitHub repository');
    expect(repoRow.stars).toMatchObject({ label: '★1.0K', checked: undefined });
  });

  it('hides the repository row when neither repository nor OSI data exists', () => {
    const result = packageSpec(page(), t, 'en-US');
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
    const fallback = packageSpec(page({ openSource: osi, repository: { stars: 999 } }), t, 'en-US');
    expect(fallback.rows.find(row => row.key === 'openSource')?.badge?.label).toBe('package.repo.archived');
    expect(fallback.repoSignals?.checkedAt).toBeUndefined();
    expect(fallback.checkedAt).toBeNull();
    const current = packageSpec(page({ openSource: osi, repository: { archived: false } }), t, 'en-US');
    expect(current.rows.find(row => row.key === 'openSource')?.badge).toBeUndefined();
  });
});
