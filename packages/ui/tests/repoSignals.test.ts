import { describe, expect, it, vi } from 'vitest';
import {
  formatRepoStars,
  repoAgeDays,
  repoMaintenanceStatus,
  repoSignalDays,
  toPackageRepoSignals
} from '../src/utils/repoSignals';

describe('repository signals', () => {
  const boundaries = [999, 1000, 12345, 999950, 1_049_999, 1_200_000, 1e9];
  const localized = [
    ['en-US', ['999', '1K', '12.3K', '1M', '1M', '1.2M', '1B']],
    ['zh-CN', ['999', '1,000', '1.2万', '100万', '105万', '120万', '10亿']],
    ['zh-Hant', ['999', '1,000', '1.2萬', '100萬', '105萬', '120萬', '10億']],
    ['ja-JP', ['999', '1,000', '1.2万', '100万', '105万', '120万', '10億']],
    ['es-ES', ['999', '1\u00a0mil', '12,3\u00a0mil', '1\u00a0M', '1\u00a0M', '1,2\u00a0M', '1 mil millones']],
    ['pt-BR', ['999', '1\u00a0mil', '12,3\u00a0mil', '1\u00a0mi', '1\u00a0mi', '1,2\u00a0mi', '1\u00a0bi']],
    ['ru-RU', ['999', '1\u00a0тыс.', '12,3\u00a0тыс.', '1\u00a0млн', '1\u00a0млн', '1,2\u00a0млн', '1\u00a0млрд']],
    ['de-DE', ['999', '1.000', '12.345', '1\u00a0Mio.', '1\u00a0Mio.', '1,2\u00a0Mio.', '1\u00a0Mrd.']],
    ['fr-FR', ['999', '1\u00a0k', '12,3\u00a0k', '1\u00a0M', '1\u00a0M', '1,2\u00a0M', '1\u00a0Md']],
    ['it-IT', ['999', '1K', '12,3K', '1\u00a0Mln', '1\u00a0Mln', '1,2\u00a0Mln', '1\u00a0Mld']]
  ] as const;
  for (const [locale, expected] of localized) {
    it.each(boundaries.map((stars, index) => [stars, expected[index]] as const))(
      `${locale} formats the %s star boundary as %s`,
      (stars, label) => {
        expect(formatRepoStars(stars, locale)).toBe(label);
        expect(formatRepoStars(stars, locale)).not.toMatch(/1000[.,]0\s*M|1050[.,]0\s*K/);
      }
    );
  }

  it('rounds ties consistently and carries into the next compact unit', () => {
    expect(formatRepoStars(12_350, 'en-US')).toBe('12.4K');
    expect(formatRepoStars(999_949, 'en-US')).toBe('999.9K');
    expect(formatRepoStars(999_950, 'en-US')).toBe('1M');
    expect(formatRepoStars(1_050_000, 'en-US')).toBe('1.1M');
  });

  it.each([
    ['zh-CN', 1_000, '1,000'],
    ['ja-JP', 1_000, '1,000'],
    ['de-DE', 1_000, '1.000'],
    ['zh-CN', 999_950, '100万'],
    ['ja-JP', 999_950, '100万'],
    ['de-DE', 999_950, '1\u00a0Mio.']
  ] as const)('rolls compact stars over in %s without a 1000.0 label', (locale, stars, expected) => {
    expect(formatRepoStars(stars, locale)).toBe(expected);
    expect(formatRepoStars(stars, locale)).not.toContain('1000.0');
    expect(formatRepoStars(stars, locale)).not.toContain('1000,0');
  });

  it.each(['invalid', '2026-13-40', '9999999999999999', '', ' ', null, undefined, 42, {}])(
    'omits invalid repository dates: %j',
    value => {
      const signals = toPackageRepoSignals({ repository: { stars: 12345, signalsCheckedAt: value, pushedAt: value } });
      expect(signals?.stars).toBe(12345);
      expect(signals?.checkedAt).toBeUndefined();
      expect(signals?.pushedAt).toBeUndefined();
      expect(toPackageRepoSignals({ repository: { signalsCheckedAt: value } })).toBeNull();
    }
  );

  it('uses only an injected clock for repository ages and maintenance status', () => {
    const clock = vi.spyOn(Date, 'now').mockImplementation(() => {
      throw new Error('render must use its injected clock');
    });
    try {
      const now = Date.parse('2026-10-30T00:00:00Z');
      expect(repoAgeDays('2025-10-30T00:00:00Z', now)).toBe(365);
      expect(repoSignalDays({ pushedAt: '2025-10-30T00:00:00Z' }, now)).toBe(365);
      expect(repoMaintenanceStatus({ pushedAt: '2025-10-30T00:00:00Z' }, now)).toBe('current');
      expect(repoSignalDays({ pushedAt: 'invalid' }, now)).toBeNull();
      expect(repoSignalDays({ pushedAt: '2027-01-01' }, now)).toBeNull();
      expect(clock).not.toHaveBeenCalled();
    } finally {
      clock.mockRestore();
    }
  });

  it('reads only the optional repository object and ignores legacy field spellings', () => {
    expect(
      toPackageRepoSignals({
        stargazers_count: 999,
        pushed_at: '2020-01-01T00:00:00Z',
        openSource: { archived: true, stars: 77, checkedAt: '2020-01-01T00:00:00Z' },
        repository: {
          url: 'https://github.com/example/app',
          stars: 12345,
          pushedAt: '2026-10-01T00:00:00Z',
          archived: false,
          signalsCheckedAt: '2026-10-02T00:00:00Z'
        }
      })
    ).toEqual({
      stars: 12345,
      pushedAt: '2026-10-01T00:00:00Z',
      checkedAt: '2026-10-02T00:00:00Z',
      archived: false,
      repoUrl: 'https://github.com/example/app'
    });
    expect(toPackageRepoSignals({})).toBeNull();
    expect(
      toPackageRepoSignals({ stargazers_count: 999, openSource: { stars: 77, checkedAt: '2020-01-01' } })
    ).toBeNull();
  });

  it('uses only open-source archived status as the archived fallback', () => {
    expect(
      toPackageRepoSignals({
        openSource: { url: 'https://github.com/example/app', archived: true, checkedAt: '2020-01-01' }
      })
    ).toEqual({
      stars: undefined,
      pushedAt: undefined,
      checkedAt: undefined,
      archived: true,
      repoUrl: undefined
    });
  });

  it('uses the strict greater-than 365 day threshold', () => {
    const now = Date.parse('2026-10-30T00:00:00Z');
    expect(repoSignalDays({ pushedAt: '2025-10-30T00:00:00Z' }, now)).toBe(365);
    expect(repoMaintenanceStatus({ pushedAt: '2025-10-30T00:00:00Z' }, now)).toBe('current');
    expect(repoMaintenanceStatus({ pushedAt: '2025-10-29T00:00:00Z' }, now)).toBe('unmaintained');
  });

  it('lets archived win over the unmaintained warning', () => {
    expect(
      repoMaintenanceStatus({ archived: true, pushedAt: '2020-01-01T00:00:00Z' }, Date.parse('2026-10-30T00:00:00Z'))
    ).toBe('archived');
  });
});
