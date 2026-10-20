import { describe, expect, it } from 'vitest';
import { fitsChip, isMacInstaller, selectAssets, shownChip } from '../src/linkAssets';
import type { LinkAssetInfo } from '../src/linkAssets';

const asset = (
  name: string,
  arch: LinkAssetInfo['arch'],
  format = name.split('.').pop() ?? 'other'
): LinkAssetInfo => ({
  name,
  arch,
  format: ['dmg', 'pkg', 'zip'].includes(format) ? format : 'other'
});

describe('isMacInstaller', () => {
  it('Takes disk images and packages, and ZIPs that say they are for the Mac', () => {
    expect(isMacInstaller(asset('Lumen-arm64.dmg', 'arm64'))).toBe(true);
    expect(isMacInstaller(asset('Lumen.pkg', null))).toBe(true);
    for (const name of ['Lumen-mac.zip', 'Lumen-macOS-arm64.zip', 'Lumen-darwin-x64.zip', 'Lumen_osx.zip'])
      expect(isMacInstaller(asset(name, null))).toBe(true);
  });

  it('Leaves out ZIPs that say nothing about the system, other formats and other systems', () => {
    expect(isMacInstaller(asset('Lumen-arm64.zip', 'arm64'))).toBe(false);
    expect(isMacInstaller(asset('latest-mac.yml', null))).toBe(false);
    expect(isMacInstaller(asset('ely-doc_0.1.9-1_amd64.deb', 'x86_64'))).toBe(false);
    expect(isMacInstaller(asset('ely-doc-0.1.9-1.x86_64.rpm', 'x86_64'))).toBe(false);
    expect(isMacInstaller(asset('ely-doc-0.1.9-1-x86_64.pkg.tar.zst', 'x86_64'))).toBe(false);
    expect(isMacInstaller(asset('Lumen-windows-mac.zip', null))).toBe(false);
    expect(isMacInstaller(asset('Lumen-Linux.dmg', null))).toBe(false);
    // "darwin" contains "win", but only a whole word names Windows.
    expect(isMacInstaller(asset('Lumen-darwin.zip', null))).toBe(true);
  });
});

describe('fitsChip', () => {
  it('Rules out only a file that names the other chip', () => {
    expect(fitsChip(asset('a.dmg', 'arm64'), 'arm64')).toBe(true);
    expect(fitsChip(asset('a.dmg', 'x86_64'), 'arm64')).toBe(false);
    expect(fitsChip(asset('a.dmg', 'universal'), 'x86_64')).toBe(true);
    expect(fitsChip(asset('a.dmg', null), 'x86_64')).toBe(true);
    expect(fitsChip(asset('a.dmg', 'x86_64'), null)).toBe(true);
  });
});

describe('shownChip', () => {
  it('Names a chip only for files that could be for a Mac', () => {
    expect(shownChip(asset('Lumen-arm64.dmg', 'arm64'))).toBe('arm64');
    expect(shownChip(asset('Lumen-x64.zip', 'x86_64'))).toBe('x86_64');
    expect(shownChip(asset('Lumen.dmg', null))).toBeNull();
    // A Linux package is not "Intel", and neither is a build whose name says Windows.
    expect(shownChip(asset('ely-doc_0.1.9-1_amd64.deb', 'x86_64'))).toBeNull();
    expect(shownChip(asset('Lumen-win-x64.zip', 'x86_64'))).toBeNull();
  });
});

describe('selectAssets', () => {
  const release = [
    asset('ElyOffice-0.1.9-arm64.dmg', 'arm64'),
    asset('ElyDoc-0.1.9-arm64.dmg', 'arm64'),
    asset('ElyDoc-0.1.9-x64.dmg', 'x86_64'),
    asset('ely-doc_0.1.9-1_amd64.deb', 'x86_64'),
    asset('ely-doc-0.1.9-1.x86_64.rpm', 'x86_64'),
    asset('latest-mac.yml', null)
  ];
  const names = (list: LinkAssetInfo[]) => list.map(item => item.name);

  it('Lists only the Mac installers for this chip; the rest wait, Mac files before other systems', () => {
    const picked = selectAssets(release, 'arm64');
    expect(picked.narrowed).toBe(true);
    expect(names(picked.main)).toEqual(['ElyDoc-0.1.9-arm64.dmg', 'ElyOffice-0.1.9-arm64.dmg']);
    expect(names(picked.rest)).toEqual([
      'ElyDoc-0.1.9-x64.dmg',
      'ely-doc_0.1.9-1_amd64.deb',
      'ely-doc-0.1.9-1.x86_64.rpm',
      'latest-mac.yml'
    ]);
    expect(names(selectAssets(release, 'x86_64').main)).toEqual(['ElyDoc-0.1.9-x64.dmg']);
  });

  it('With no chip known lists the Mac installers of both chips, universal first', () => {
    const picked = selectAssets([...release, asset('Ely-universal.dmg', 'universal'), asset('Ely.dmg', null)], null);
    expect(names(picked.main)).toEqual([
      'Ely-universal.dmg',
      'ElyDoc-0.1.9-arm64.dmg',
      'ElyOffice-0.1.9-arm64.dmg',
      'ElyDoc-0.1.9-x64.dmg',
      'Ely.dmg'
    ]);
    expect(names(picked.rest)).toEqual(['ely-doc_0.1.9-1_amd64.deb', 'ely-doc-0.1.9-1.x86_64.rpm', 'latest-mac.yml']);
  });

  it('Hides nothing when no file can be positively taken for a Mac installer that fits', () => {
    const odd = [asset('Lumen-arm64.zip', 'arm64'), asset('Lumen.tar.gz', null), asset('Lumen-x64.zip', 'x86_64')];
    const picked = selectAssets(odd, 'arm64');
    expect(picked.narrowed).toBe(false);
    expect(picked.rest).toEqual([]);
    expect(names(picked.main)).toEqual(['Lumen-arm64.zip', 'Lumen-x64.zip', 'Lumen.tar.gz']);
    // Only the other chip's installers exist: they are all there is.
    expect(selectAssets([asset('Lumen-x64.dmg', 'x86_64')], 'arm64')).toMatchObject({ narrowed: false });
    expect(selectAssets([], 'arm64')).toEqual({ main: [], rest: [], narrowed: false });
  });
});
