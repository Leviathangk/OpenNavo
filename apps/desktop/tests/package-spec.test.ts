import { describe, expect, it } from 'vitest';
import type { PackageDetail } from '@opennavo/api';
import type { LocalItem } from '@/ipc/client';
import { packageSpec } from '@/utils/packageSpec';

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
const rows = (pkg: PackageDetail) => packageSpec(pkg, null, null, t, 'en-US').rows;

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
    expect(spec.find(row => row.key === 'bundle')?.value).toBe('org.example.ely-office');
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
      'en-US'
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
    const spec = packageSpec(null, local, null, t, 'en-US');
    expect(spec.rows.find(row => row.key === 'token')?.value).toBe('example');
    expect(spec.rows.find(row => row.key === 'homepage')?.href).toBe('https://example.com/');
    expect(spec.rows.find(row => row.key === (kind === 'link' ? 'bundle' : 'binaries'))?.value).toBe(
      kind === 'link' ? 'org.example.app' : 'example'
    );
    expect(spec.checkedAt).toBeNull();
  });
});
