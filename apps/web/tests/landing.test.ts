import { describe, expect, it } from 'vitest';
import type { PackageSummary } from '@opennavo/api';
import {
  SAMPLE_APPS,
  easeOutCubic,
  headingLead,
  installLog,
  marqueeRows,
  pickShowcaseApps,
  toLandingApp,
  withSampleApps
} from '../app/utils/landing';

function pkg(token: string, extra: Partial<PackageSummary> = {}): PackageSummary {
  return {
    kind: 'cask',
    token,
    name: token,
    displayName: token,
    version: '1.0.0',
    installs30d: 10,
    autoUpdates: false,
    deprecated: false,
    disabled: false,
    isFont: false,
    isLibrary: false,
    editorChoice: false,
    versionChangedAt: '2026-10-01T00:00:00Z',
    ...extra
  };
}

describe('Landing demo apps', () => {
  it('Merge deduplicates, preserves API order, and prioritizes icons', () => {
    const popular = [pkg('a'), pkg('b', { iconUrl: 'https://cdn/b.png' }), pkg('c')];
    const recent = [pkg('b', { iconUrl: 'https://cdn/b.png' }), pkg('d', { iconUrl: 'https://cdn/d.png' })];
    expect(pickShowcaseApps([popular, recent], 10).map(item => item.token)).toEqual(['b', 'd', 'a', 'c']);
    expect(pickShowcaseApps([popular, recent], 2).map(item => item.token)).toEqual(['b', 'd']);
  });

  it('Only installable Casks: exclude fonts, disabled, command-line tools; accept empty/missing lists', () => {
    const items = [
      pkg('font-inter', { isFont: true }),
      pkg('old-app', { disabled: true }),
      pkg('wget', { kind: 'formula' }),
      pkg('ghostty')
    ];
    expect(pickShowcaseApps([items, null, undefined, []], 5).map(item => item.token)).toEqual(['ghostty']);
    expect(pickShowcaseApps([items], 0)).toEqual([]);
  });

  it('Preserve real versions/times in demos and normalize absent values to null', () => {
    expect(toLandingApp(pkg('ghostty', { displayName: 'Ghostty', summary: null, version: '' }))).toEqual({
      token: 'ghostty',
      name: 'Ghostty',
      summary: null,
      iconUrl: null,
      iconVariants: undefined,
      accentColor: null,
      version: null,
      versionChangedAt: '2026-10-01T00:00:00Z'
    });
  });

  it('Preserves responsive icon renditions for every demo', () => {
    const iconVariants: PackageSummary['iconVariants'] = [
      { url: 'https://example.com/icon-64.webp', width: 64, height: 64, type: 'image/webp' }
    ];
    expect(toLandingApp(pkg('ghostty', { iconVariants })).iconVariants).toEqual(iconVariants);
  });

  it('Fallback sample apps fill unavailable data without duplicate tokens or invented versions', () => {
    const real = [toLandingApp(pkg('ghostty', { displayName: 'Ghostty' }))];
    const filled = withSampleApps(real, 3);
    expect(filled.map(item => item.token)).toEqual(['ghostty', 'visual-studio-code', 'google-chrome']);
    expect(withSampleApps([], 2).every(item => item.version === null && item.versionChangedAt === null)).toBe(true);
    expect(withSampleApps(real, 1)).toEqual(real);
    expect(withSampleApps([], 99)).toHaveLength(SAMPLE_APPS.length);
  });
});

describe('Marquee rows', () => {
  it('Distribute enough entries across rows and repeat to minimum count', () => {
    expect(marqueeRows([1, 2, 3, 4, 5, 6, 7, 8], 2, 6)).toEqual([
      [1, 3, 5, 7, 1, 3, 5, 7],
      [2, 4, 6, 8, 2, 4, 6, 8]
    ]);
  });

  it('Sparse rows each use all entries with staggered starts', () => {
    expect(marqueeRows(['a', 'b', 'c'], 2, 4)).toEqual([
      ['a', 'b', 'c', 'a', 'b', 'c'],
      ['b', 'c', 'a', 'b', 'c', 'a']
    ]);
  });

  it('Fewer items than rows create only populated rows; no data returns empty', () => {
    expect(marqueeRows(['a'], 2, 3)).toEqual([['a', 'a', 'a']]);
    expect(marqueeRows([], 2, 3)).toEqual([]);
    expect(marqueeRows(['a'], 0, 3)).toEqual([]);
  });
});

describe('Demo animation', () => {
  it('Easing runs zero to one and clamps out-of-range input', () => {
    expect(easeOutCubic(0)).toBe(0);
    expect(easeOutCubic(1)).toBe(1);
    expect(easeOutCubic(0.5)).toBeCloseTo(0.875);
    expect(easeOutCubic(-1)).toBe(0);
    expect(easeOutCubic(2)).toBe(1);
  });

  it('Install logs start with full --cask command and end with Homebrew success', () => {
    const lines = installLog('ghostty');
    expect(lines[0]).toBe('$ brew install --cask ghostty');
    expect(lines.at(-1)).toBe('🍺  ghostty was successfully installed!');
  });
});

describe('Two-line headings', () => {
  it('End the first line with a space except after CJK text', () => {
    expect(headingLead('Install apps, scan for malware,')).toBe('Install apps, scan for malware, ');
    expect(headingLead('装软件、查病毒，')).toBe('装软件、查病毒，');
    expect(headingLead('Macのアプリもウイルス対策も')).toBe('Macのアプリもウイルス対策も');
    expect(headingLead('Устанавливайте приложения —')).toBe('Устанавливайте приложения — ');
  });
});
