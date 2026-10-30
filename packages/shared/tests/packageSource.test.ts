import { describe, expect, it } from 'vitest';
import { toPackageSource } from '../src/packageSource';

type SourcePackage = Parameters<typeof toPackageSource>[0];
const sourceUrl = 'https://github.com/Homebrew/homebrew-cask/blob/HEAD/Casks/e/example.rb';
const hash = 'a'.repeat(64);
const fixture = (): SourcePackage => ({
  kind: 'cask',
  version: '1.0',
  sourceUrl,
  facts: {
    overview: { version: '1.0', downloadUrl: 'https://vendor.example/app.dmg', sha256: hash },
    requirements: [
      {
        architectures: ['arm64'],
        versions: ['1.0'],
        downloadUrl: 'https://vendor.example/app.dmg',
        sha256: hash,
        evidence: { sourceUrl, status: 'verified', checkedAt: '2026-10-30T00:00:00Z' }
      }
    ]
  }
});

describe('package source evidence', () => {
  it('verifies all current architectures and reports the oldest complete check', () => {
    const pkg = fixture();
    const first = pkg.facts!.requirements[0]!;
    pkg.facts!.requirements = [
      first,
      {
        ...first,
        downloadUrl: 'https://intel.vendor.example/app.dmg',
        sha256: 'b'.repeat(64),
        evidence: { ...first.evidence, checkedAt: '2026-10-29T00:00:00Z' }
      }
    ];
    expect(toPackageSource(pkg)).toEqual({
      verified: true,
      checkedAt: '2026-10-29T00:00:00Z',
      entries: [
        { host: 'vendor.example', sha256: hash },
        { host: 'intel.vendor.example', sha256: 'b'.repeat(64) }
      ]
    });
  });

  it('merges identical download URLs and compares SHA-256 without regard to letter case', () => {
    const pkg = fixture();
    const row = pkg.facts!.requirements[0]!;
    pkg.facts!.requirements = [row, { ...row, sha256: hash.toUpperCase() }];
    expect(toPackageSource(pkg)).toMatchObject({ verified: true, entries: [{ sha256: hash }] });
  });

  it.each([null, '', 'not-a-checksum', 'b'.repeat(64)])(
    'does not verify missing, invalid or conflicting SHA-256: %s',
    sha256 => {
      const pkg = fixture();
      const row = pkg.facts!.requirements[0]!;
      pkg.facts!.requirements = [row, { ...row, sha256 }];
      expect(toPackageSource(pkg)).toMatchObject({ verified: false, entries: [{ sha256: null }] });
    }
  );

  it.each([
    { status: 'verified', checkedAt: null },
    { status: 'verified', checkedAt: 'invalid' },
    { status: 'pending', checkedAt: '2026-10-29T00:00:00Z' },
    { status: 'stale', checkedAt: '2026-10-29T00:00:00Z' }
  ])('does not hide incomplete or stale evidence behind a verified row: %j', evidence => {
    const pkg = fixture();
    const first = pkg.facts!.requirements[0]!;
    pkg.facts!.requirements = [
      first,
      {
        ...first,
        downloadUrl: 'https://intel.vendor.example/app.dmg',
        evidence: { sourceUrl, ...evidence }
      }
    ];
    const source = toPackageSource(pkg)!;
    expect(source.verified).toBe(false);
    expect(source.entries).toHaveLength(2);
    expect(source.checkedAt).toBe(evidence.status === 'stale' ? evidence.checkedAt : undefined);
  });

  it('uses the package version when the facts overview has no version', () => {
    const pkg = fixture();
    pkg.facts!.overview.version = '';
    expect(toPackageSource(pkg)?.verified).toBe(true);
  });

  it.each(['version', 'architecture', 'source', 'missing source'])(
    'excludes declarations that do not describe this Homebrew package: %s',
    reason => {
      const pkg = fixture();
      const row = pkg.facts!.requirements[0]!;
      if (reason === 'version') row.versions = ['0.9'];
      if (reason === 'architecture') row.architectures = [];
      if (reason === 'source') row.evidence.sourceUrl = 'https://other.example/requirements';
      if (reason === 'missing source') pkg.sourceUrl = null;
      expect(toPackageSource(pkg)).toEqual({
        verified: false,
        checkedAt: undefined,
        entries: [{ host: 'vendor.example', sha256: hash }]
      });
    }
  );

  it.each(['http://vendor.example/app.dmg', 'not-a-url', null])(
    'does not verify an unsafe or absent download address: %s',
    downloadUrl => {
      const pkg = fixture();
      pkg.facts!.requirements[0]!.downloadUrl = downloadUrl;
      expect(toPackageSource(pkg)).toMatchObject({ verified: false, entries: [{ host: 'vendor.example' }] });
      pkg.facts!.overview.downloadUrl = null;
      expect(toPackageSource(pkg)).toBeNull();
    }
  );

  it('does not verify a complete architecture while another has an unsafe download URL', () => {
    const pkg = fixture();
    const row = pkg.facts!.requirements[0]!;
    pkg.facts!.requirements = [row, { ...row, downloadUrl: 'http://intel.vendor.example/app.dmg' }];
    expect(toPackageSource(pkg)?.verified).toBe(false);
  });

  it('shows an older API download and hash as unverified, without inventing a check date', () => {
    expect(
      toPackageSource({ kind: 'cask', downloadUrl: 'https://vendor.example/app.dmg', downloadSha256: hash })
    ).toEqual({
      verified: false,
      checkedAt: undefined,
      entries: [{ host: 'vendor.example', sha256: hash }]
    });
    expect(
      toPackageSource({
        kind: 'cask',
        facts: null,
        downloadUrl: 'https://vendor.example/app.dmg',
        downloadSha256: 'no_check'
      })
    ).toMatchObject({
      verified: false,
      entries: [{ sha256: null }]
    });
    expect(toPackageSource({ kind: 'cask', downloadUrl: 'https://vendor.example/app.dmg' })).toMatchObject({
      verified: false,
      entries: [{ sha256: null }]
    });
  });

  it('does not create a badge without a usable download source or for other package kinds', () => {
    expect(toPackageSource({ kind: 'cask' })).toBeNull();
    expect(toPackageSource({ kind: 'formula' })).toBeNull();
    expect(toPackageSource({ ...fixture(), kind: 'link' })).toBeNull();
    const pkg = fixture();
    pkg.facts!.requirements = [];
    pkg.facts!.overview.sha256 = null;
    expect(toPackageSource(pkg)).toMatchObject({ verified: false, entries: [{ sha256: null }] });
  });
});
