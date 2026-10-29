import { describe, expect, it } from 'vitest';
import { formatRepoStars, repoMaintenanceStatus, repoSignalDays, toPackageRepoSignals } from '../src/utils/repoSignals';

describe('repository signals', () => {
  it.each([
    [999, '999'],
    [1000, '1.0K'],
    [12345, '12.3K'],
    [1_200_000, '1.2M'],
    [1_049_999, '1.0M']
  ])('formats %s stars as %s', (stars, expected) => {
    expect(formatRepoStars(stars)).toBe(expected);
  });

  it('reads only the optional repository object and ignores legacy field spellings', () => {
    expect(
      toPackageRepoSignals(
        {
          stargazers_count: 999,
          pushed_at: '2020-01-01T00:00:00Z',
          repository: {
            url: 'https://github.com/example/app',
            stars: 12345,
            pushedAt: '2026-10-01T00:00:00Z',
            archived: false,
            signalsCheckedAt: '2026-10-02T00:00:00Z'
          }
        },
        { archived: true }
      )
    ).toEqual({
      stars: 12345,
      pushedAt: '2026-10-01T00:00:00Z',
      checkedAt: '2026-10-02T00:00:00Z',
      archived: false,
      repoUrl: 'https://github.com/example/app'
    });
    expect(toPackageRepoSignals({}, null)).toBeNull();
  });

  it('uses open-source archived status only as the explicit archived fallback', () => {
    expect(toPackageRepoSignals({}, { repoUrl: 'https://github.com/example/app', archived: true })).toEqual({
      stars: undefined,
      pushedAt: undefined,
      checkedAt: undefined,
      archived: true,
      repoUrl: 'https://github.com/example/app'
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
