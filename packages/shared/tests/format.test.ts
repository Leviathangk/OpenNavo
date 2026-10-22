import { describe, expect, it } from 'vitest';
import {
  formatBytes,
  formatCount,
  formatCountCompact,
  formatDate,
  formatDuration,
  formatList,
  formatMilliseconds,
  formatPercent,
  formatRelativeTime,
  formatSpeed,
  formatTime,
  formatReleaseVersion,
  releaseTitleDetail,
  formatVersion,
  formatVersionChange
} from '../src/format';

const timeZone = 'Asia/Shanghai';
// Wednesday 2026-09-30, 22:53 at UTC+8.
const now = new Date('2026-09-30T22:53:00+08:00');

describe('Count formatting', () => {
  it('Groups large numbers', () => {
    expect(formatCount(17394)).toBe('17,394');
    expect(formatCount(0)).toBe('0');
    expect(formatCount(1234.6)).toBe('1,235');
  });

  it('Uses Chinese ten-thousand and hundred-million units with one decimal and no trailing zero', () => {
    expect(formatCountCompact(9876, { locale: 'zh-CN' })).toBe('9,876');
    expect(formatCountCompact(91_000, { locale: 'zh-CN' })).toBe('9.1万');
    expect(formatCountCompact(478_000, { locale: 'zh-CN' })).toBe('47.8万');
    expect(formatCountCompact(20_000, { locale: 'zh-CN' })).toBe('2万');
    expect(formatCountCompact(120_000_000, { locale: 'zh-CN' })).toBe('1.2亿');
    expect(formatCountCompact(99_999_600, { locale: 'zh-CN' })).toBe('1亿');
  });

  it('Uses English K, M, and B units', () => {
    expect(formatCountCompact(999, { locale: 'en-US' })).toBe('999');
    expect(formatCountCompact(17_394, { locale: 'en-US' })).toBe('17.4K');
    expect(formatCountCompact(1_200_000, { locale: 'en-US' })).toBe('1.2M');
    expect(formatCountCompact(999_950, { locale: 'en-US' })).toBe('1M');
    expect(formatCountCompact(3_000_000_000, { locale: 'en-US' })).toBe('3B');
  });
});

describe('Size and speed formatting', () => {
  it('Uses decimal units at the specified thresholds', () => {
    expect(formatBytes(512)).toBe('512 B');
    expect(formatBytes(-1)).toBe('0 B');
    expect(formatBytes(6_100_000)).toBe('6.1 MB');
    expect(formatBytes(318_000_000)).toBe('318 MB');
    expect(formatBytes(1_400_000_000)).toBe('1.4 GB');
    expect(formatBytes(9_960_000)).toBe('10 MB');
    expect(formatBytes(999_600_000)).toBe('1.0 GB');
    expect(formatBytes(999.6)).toBe('1.0 KB');
    expect(formatBytes(5_000_000_000_000_000)).toBe('5000 TB');
  });

  it('Shows one decimal for speed', () => {
    expect(formatSpeed(18_400_000)).toBe('18.4 MB/s');
    expect(formatSpeed(500)).toBe('500.0 B/s');
    expect(formatSpeed(-5)).toBe('0.0 B/s');
  });
});

describe('Date and time formatting', () => {
  it('Omits the current year and optionally includes the weekday', () => {
    expect(formatDate('2026-09-30T10:00:00+08:00', { now, timeZone, locale: 'zh-CN' })).toBe('9月30日');
    expect(formatDate('2026-03-02T10:00:00+08:00', { now, timeZone, locale: 'zh-CN' })).toBe('3月2日');
    expect(formatDate('2025-03-02T10:00:00+08:00', { now, timeZone, locale: 'zh-CN' })).toBe('2025年3月2日');
    expect(formatDate('2026-09-30T10:00:00+08:00', { now, timeZone, weekday: true, locale: 'zh-CN' })).toBe(
      '9月30日 · 周三'
    );
  });

  it('Formats English dates', () => {
    expect(formatDate('2026-09-30T10:00:00+08:00', { now, timeZone, locale: 'en-US' })).toBe('Sep 30');
    expect(formatDate('2025-03-02T10:00:00+08:00', { now, timeZone, locale: 'en-US' })).toBe('Mar 2, 2025');
    expect(formatDate('2026-09-30T10:00:00+08:00', { now, timeZone, locale: 'en-US', weekday: true })).toBe(
      'Sep 30 · Wed'
    );
  });

  it('Defaults to the current time and local time zone', () => {
    expect(formatDate(new Date(), { locale: 'zh-CN' })).toMatch(/^\d{1,2}月\d{1,2}日$/);
  });

  it('Uses the 24-hour clock', () => {
    expect(formatTime('2026-09-30T22:53:00+08:00', { timeZone })).toBe('22:53');
    expect(formatTime('2026-09-30T08:05:00+08:00', { timeZone })).toBe('08:05');
    expect(formatTime(new Date())).toMatch(/^\d{2}:\d{2}$/);
  });
});

describe('Relative time formatting', () => {
  const ago = (seconds: number) => new Date(now.getTime() - seconds * 1000);
  const relative = (seconds: number, locale: 'zh-CN' | 'en-US' = 'zh-CN') =>
    formatRelativeTime(ago(seconds), { now, timeZone, locale });

  it('Formats Chinese relative time', () => {
    expect(relative(30)).toBe('刚刚');
    expect(relative(-120)).toBe('刚刚');
    expect(relative(5 * 60)).toBe('5 分钟前');
    expect(relative(3 * 3600)).toBe('3 小时前');
    expect(relative(30 * 3600)).toBe('昨天');
    expect(relative(3 * 86_400)).toBe('3 天前');
    expect(relative(10 * 86_400)).toBe('9月20日');
  });

  it('Formats English relative time', () => {
    expect(relative(30, 'en-US')).toBe('just now');
    expect(relative(5 * 60, 'en-US')).toBe('5 min ago');
    expect(relative(3600, 'en-US')).toBe('1 hour ago');
    expect(relative(3 * 3600, 'en-US')).toBe('3 hours ago');
    expect(relative(30 * 3600, 'en-US')).toBe('yesterday');
    expect(relative(3 * 86_400, 'en-US')).toBe('3 days ago');
    expect(relative(400 * 86_400, 'en-US')).toBe('Aug 26, 2025');
  });

  it('Defaults the reference time to now', () => {
    expect(formatRelativeTime(new Date())).toBe('just now');
  });
});

describe('Duration and version formatting', () => {
  it('Formats durations', () => {
    expect(formatDuration(41, { locale: 'zh-CN' })).toBe('41 秒');
    expect(formatDuration(-3, { locale: 'zh-CN' })).toBe('0 秒');
    expect(formatDuration(185, { locale: 'zh-CN' })).toBe('3 分 5 秒');
    expect(formatDuration(41, { locale: 'en-US' })).toBe('41 s');
    expect(formatDuration(185, { locale: 'en-US' })).toBe('3 min 5 s');
  });

  it('Uses the cask version before the comma', () => {
    expect(formatVersion('4.93.0,240920')).toBe('4.93.0');
    expect(formatVersion('1.140.0')).toBe('1.140.0');
    expect(formatVersion('')).toBe('');
    expect(formatVersionChange('1.139.1', '1.140.0,123')).toBe('1.139.1 → 1.140.0');
  });

  it('Shows release titles only when they add to the version', () => {
    expect(releaseTitleDetail(null, '2.0.3')).toBeNull();
    expect(releaseTitleDetail(undefined, '2.0.3')).toBeNull();
    expect(releaseTitleDetail('Rectangle 2.0.3', '2.0.3')).toBeNull();
    expect(releaseTitleDetail('v2.0.3', '2.0.3')).toBeNull();
    expect(releaseTitleDetail('Visual Studio Code 1.143.0 release notes', '1.143.0')).toBeNull();
    expect(releaseTitleDetail('Raycast 2.8.0.0 — Release notes', '2.8.0.0')).toBeNull();
    expect(releaseTitleDetail('Rectangle 2.0.3 版本说明', '2.0.3')).toBeNull();
    expect(releaseTitleDetail('Docker Desktop 4.96.0,243774', '4.96.0,243774')).toBeNull();
    expect(releaseTitleDetail('Docker Desktop 4.96.0,243774', '4.96.0')).toBeNull();
    expect(releaseTitleDetail('Stats 3.0.22 (Build 812)', '3.0.22')).toBeNull();
    expect(releaseTitleDetail('Release Notes', '9.1')).toBeNull();
    expect(releaseTitleDetail('Rectangle 2.0.4: more window shortcuts', '2.0.4')).toBe(
      'Rectangle 2.0.4: more window shortcuts'
    );
    expect(releaseTitleDetail('Raycast 2.7.0.0 — 2.7 feature release', '2.7.0.0')).toBe(
      'Raycast 2.7.0.0 — 2.7 feature release'
    );
    expect(releaseTitleDetail(' September 2026 ', '9.1')).toBe('September 2026');
  });

  it('Tags release entries that belong to one build', () => {
    expect(formatReleaseVersion({ version: '0.1' })).toBe('0.1');
    expect(formatReleaseVersion({ version: '0.1,49', build: '49' })).toBe('0.1 (49)');
    expect(formatReleaseVersion({ version: '160.0a1,2026-10-13-21-09-59', build: '2026-10-13-21-09-59' })).toBe(
      '160.0a1 (2026-10-13-21-09-59)'
    );
    expect(
      formatReleaseVersion({
        version: '1.142.0-insider,1791879736120,d5dfbe9aa2c84cc3cbc3d4926a5155983aceeef1',
        build: '1791879736120,d5dfbe9aa2c84cc3cbc3d4926a5155983aceeef1'
      })
    ).toBe('1.142.0-insider (d5dfbe9aa2c8)');
  });
});

describe('Supports additional locales through Intl', () => {
  const intlNow = new Date('2026-10-06T12:00:00Z');
  const locale = 'ja-JP' as const;
  it('Localizes counts, dates, and durations', () => {
    expect(formatCountCompact(91000, { locale })).toBe(
      new Intl.NumberFormat(locale, { notation: 'compact', maximumFractionDigits: 1 }).format(91000)
    );
    for (const input of ['2026-03-02T00:00:00Z', '2025-03-02T00:00:00Z']) {
      for (const weekday of [false, true]) {
        expect(formatDate(input, { locale, now: intlNow, weekday, timeZone: 'UTC' })).toBe(
          new Intl.DateTimeFormat(locale, {
            timeZone: 'UTC',
            month: 'short',
            day: 'numeric',
            year: input.startsWith('2026') ? undefined : 'numeric',
            weekday: weekday ? 'short' : undefined
          }).format(new Date(input))
        );
      }
    }
    expect(formatDuration(5, { locale })).toContain('5');
    expect(formatDuration(65, { locale })).toContain('1');
  });
  it('Covers relative-time branches', () => {
    for (const seconds of [0, 120, 7200, 86400, 3 * 86400, 10 * 86400]) {
      expect(
        formatRelativeTime(new Date(intlNow.getTime() - seconds * 1000), { locale, now: intlNow, timeZone: 'UTC' })
      ).not.toMatch(/ago|yesterday|just now/);
    }
  });
});

describe('Formats numbers and lists in all six languages', () => {
  it.each(['en-US', 'zh-CN', 'ja-JP', 'es-ES', 'pt-BR', 'ru-RU'] as const)('%s', locale => {
    const output = formatCount(17394, { locale });
    expect(output).toBe(new Intl.NumberFormat(locale, { maximumFractionDigits: 0 }).format(17394));
    expect(formatList(['Alpha', 'Beta', 'Gamma'], { locale })).toBe(
      new Intl.ListFormat(locale, { style: 'long', type: 'conjunction' }).format(['Alpha', 'Beta', 'Gamma'])
    );
    expect(formatList([], { locale })).toBe('');
    expect(formatMilliseconds(123, { locale })).toBe(
      new Intl.NumberFormat(locale, {
        style: 'unit',
        unit: 'millisecond',
        unitDisplay: 'short',
        maximumFractionDigits: 0
      }).format(123)
    );
    if (locale === 'en-US' || locale === 'zh-CN') {
      expect(formatBytes(6100000, { locale })).toBe('6.1 MB');
      expect(formatSpeed(18400000, { locale })).toBe('18.4 MB/s');
    } else {
      for (const [input, unit, value, digits] of [
        [512, 'byte', 512, 0],
        [6100000, 'megabyte', 6.1, 1],
        [318000000, 'megabyte', 318, 0]
      ] as const) {
        expect(formatBytes(input, { locale })).toBe(
          new Intl.NumberFormat(locale, {
            style: 'unit',
            unit,
            unitDisplay: 'short',
            minimumFractionDigits: digits,
            maximumFractionDigits: digits
          }).format(value)
        );
      }
      expect(formatSpeed(18400000, { locale })).toBe(
        new Intl.NumberFormat(locale, {
          style: 'unit',
          unit: 'megabyte-per-second',
          unitDisplay: 'short',
          minimumFractionDigits: 1,
          maximumFractionDigits: 1
        }).format(18.4)
      );
    }
  });
  it('Defaults to English', () => {
    expect(formatList(['Alpha', 'Beta'])).toBe('Alpha and Beta');
    expect(formatMilliseconds(123)).toBe('123 ms');
  });
});

describe('Percentage formatting', () => {
  it.each([
    ['en-US', '0.8%', '1.4%', '50%'],
    ['zh-CN', '0.8%', '1.4%', '50%'],
    ['ja-JP', '0.8%', '1.4%', '50%'],
    ['es-ES', '0,8 %', '1,4 %', '50 %'],
    ['pt-BR', '0,8%', '1,4%', '50%'],
    ['ru-RU', '0,8 %', '1,4 %', '50 %']
  ] as const)('%s 的小数和百分号间距', (locale, coverage, budget, progress) => {
    expect(formatPercent(0.008, { locale, digits: 1 })).toBe(coverage);
    expect(formatPercent(0.014, { locale, digits: 1 })).toBe(budget);
    expect(formatPercent(0.5, { locale })).toBe(progress);
    expect(formatPercent(0.5, { locale, digits: 1 })).toBe(progress);
  });

  it('Preserves rounding, zero, full values, and ungrouped English and Chinese output', () => {
    expect(formatPercent(0)).toBe('0%');
    expect(formatPercent(1)).toBe('100%');
    expect(formatPercent(0.126)).toBe('13%');
    expect(formatPercent(0.126, { digits: 1 })).toBe('12.6%');
    for (const locale of ['en-US', 'zh-CN'] as const) {
      expect(formatPercent(10, { locale })).toBe('1000%');
    }
  });
});

describe('English defaults without explicit locale', () => {
  it('formats compact counts, dates, relative times, and durations in English', () => {
    expect(formatCountCompact(91_000)).toBe('91K');
    expect(formatDate('2026-09-30T10:00:00+08:00', { now, timeZone })).toBe('Sep 30');
    expect(formatRelativeTime(now, { now, timeZone })).toBe('just now');
    expect(formatDuration(185)).toBe('3 min 5 s');
  });
});
