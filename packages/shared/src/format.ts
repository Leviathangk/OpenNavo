// Shared count/size/time/version formats (08 §12.3); all three clients must use these implementations.
// Site locales: the website formats in ten languages; the app locales are a subset.
import type { SiteLocale as Locale } from './types';

export interface FormatOptions {
  locale?: Locale;
}

export interface DecimalFormatOptions extends FormatOptions {
  digits?: number;
}

/** USD amounts: locale determines symbol position, separators, decimals. */
export function formatCurrency(value: number, { locale = 'en-US' }: FormatOptions = {}): string {
  return new Intl.NumberFormat(locale, { style: 'currency', currency: 'USD' }).format(value);
}

/** Percent input is a ratio (0.008 → 0.8%); at most digits decimals without trailing zeroes. */
export function formatPercent(value: number, { locale = 'en-US', digits = 0 }: DecimalFormatOptions = {}): string {
  return new Intl.NumberFormat(locale, {
    style: 'percent',
    maximumFractionDigits: digits,
    useGrouping: locale !== 'en-US' && locale !== 'zh-CN'
  }).format(value);
}

/** Fixed decimals: preserve toFixed for Chinese/English; other locales use local decimal/group separators. */
export function formatDecimal(value: number, { locale = 'en-US', digits = 2 }: DecimalFormatOptions = {}): string {
  if (locale === 'en-US' || locale === 'zh-CN') return value.toFixed(digits);
  return new Intl.NumberFormat(locale, { minimumFractionDigits: digits, maximumFractionDigits: digits }).format(value);
}

export interface DateFormatOptions extends FormatOptions {
  /** Current time for this-year/yesterday checks; defaults to DateDefaults.now, else new Date(). */
  now?: Date;
  /** IANA zone; defaults to DateDefaults.timeZone, else the runtime's local zone. */
  timeZone?: string;
}

/** Zone and reference time for date formatting calls that pass none. */
export interface DateDefaults {
  /**
   * IANA zone; unset formats in the runtime's local zone, as the desktop app does. Readers cannot assume
   * a default zone is theirs, so clock times formatted in it name it, e.g. 14:05 UTC.
   */
  timeZone?: string;
  /** Reference time for relative times and this-year checks; unset reads the clock. */
  now?: () => Date;
}

let dateDefaults: DateDefaults = {};

/**
 * Sets the defaults for every later call. The website formats in UTC against the time its server
 * rendered the page, so cached HTML and its hydration in any visitor's browser produce the same text.
 */
export function setDateDefaults(defaults: DateDefaults): void {
  dateDefaults = defaults;
}

const defaultNow = (): Date => dateDefaults.now?.() ?? new Date();

const integer = new Intl.NumberFormat('en-US', { maximumFractionDigits: 0 });

/** Full grouped count, e.g. 17,394. */
export function formatCount(value: number, { locale = 'en-US' }: FormatOptions = {}): string {
  const formatter =
    locale === 'en-US' || locale === 'zh-CN' ? integer : new Intl.NumberFormat(locale, { maximumFractionDigits: 0 });
  return formatter.format(Math.round(value));
}

// One decimal, omitting .0.
function oneDecimal(value: number): string {
  const rounded = Math.round(value * 10) / 10;
  return Number.isInteger(rounded) ? String(rounded) : rounded.toFixed(1);
}

function compactUnits(locale: Locale): Array<[number, string]> {
  return locale === 'zh-CN'
    ? [
        [1e8, '亿'],
        [1e4, '万']
      ]
    : [
        [1e9, 'B'],
        [1e6, 'M'],
        [1e3, 'K']
      ];
}

/**
 * Compact counts: Chinese units at 10,000 and 100,000,000; English K/M/B. One decimal without .0.
 * Below the smallest unit, return the full count.
 */
export function formatCountCompact(value: number, { locale = 'en-US' }: FormatOptions = {}): string {
  if (locale !== 'zh-CN' && locale !== 'en-US') {
    const options: Intl.NumberFormatOptions = {
      notation: 'compact',
      maximumFractionDigits: 1,
      roundingMode: 'halfExpand'
    };
    const parts = new Intl.NumberFormat(locale, options).formatToParts(value);
    const compact = parts.some(part => part.type === 'compact');
    const integer = Number(
      parts
        .filter(part => part.type === 'integer')
        .map(part => part.value)
        .join('')
    );
    if (compact && integer < 1000) return parts.map(part => part.value).join('');
    if (compact && integer >= 1000)
      return new Intl.NumberFormat(locale, { ...options, compactDisplay: 'long' }).format(value);
    // German short form keeps four-digit numbers grouped until one million, so 999,950
    // would read 999.950. Add the half-expand carry so it rolls over to 1 Mio.
    if (Math.abs(value) >= 999_950) return new Intl.NumberFormat(locale, options).format(value + 50);
    return parts.map(part => part.value).join('');
  }
  const units = compactUnits(locale);
  const absolute = Math.abs(value);
  for (let index = 0; index < units.length; index += 1) {
    const [size, suffix] = units[index] as [number, string];
    if (absolute < size) continue;
    const scaled = Math.round((value / size) * 10) / 10;
    // Promote to the larger unit when rounding crosses its boundary, e.g. 99,999,600 to 100 million in Chinese notation.
    const larger = units[index - 1];
    if (larger && Math.abs(scaled * size) >= larger[0]) return `${oneDecimal(value / larger[0])}${larger[1]}`;
    return `${oneDecimal(value / size)}${suffix}`;
  }
  return formatCount(value, { locale });
}

const BYTE_UNITS = ['B', 'KB', 'MB', 'GB', 'TB'];
const INTL_BYTE_UNITS = ['byte', 'kilobyte', 'megabyte', 'gigabyte', 'terabyte'] as const;

/** Decimal sizes matching Finder: one decimal below ten, integers otherwise; e.g. 6.1 MB, 318 MB, 1.4 GB. */
export function formatBytes(bytes: number, { locale = 'en-US' }: FormatOptions = {}): string {
  let value = Math.max(0, bytes);
  let unit = 0;
  // Promote when rounding reaches 1000, avoiding 1000 MB.
  while (unit < BYTE_UNITS.length - 1 && Math.round(value) >= 1000) {
    value /= 1000;
    unit += 1;
  }
  if (locale !== 'en-US' && locale !== 'zh-CN') {
    const digits = unit > 0 && value < 9.95 ? 1 : 0;
    return new Intl.NumberFormat(locale, {
      style: 'unit',
      unit: INTL_BYTE_UNITS[unit],
      unitDisplay: 'short',
      minimumFractionDigits: digits,
      maximumFractionDigits: digits
    }).format(value);
  }
  if (unit === 0) return `${Math.round(value)} B`;
  // Values ≥9.95 round to ten; display as integers.
  const text = value < 9.95 ? value.toFixed(1) : String(Math.round(value));
  return `${text} ${BYTE_UNITS[unit]}`;
}

/** Speed with one decimal, e.g. 18.4 MB/s. */
export function formatSpeed(bytesPerSecond: number, { locale = 'en-US' }: FormatOptions = {}): string {
  let value = Math.max(0, bytesPerSecond);
  let unit = 0;
  while (unit < BYTE_UNITS.length - 1 && value >= 999.95) {
    value /= 1000;
    unit += 1;
  }
  if (locale !== 'en-US' && locale !== 'zh-CN') {
    return new Intl.NumberFormat(locale, {
      style: 'unit',
      unit: `${INTL_BYTE_UNITS[unit]}-per-second`,
      unitDisplay: 'short',
      minimumFractionDigits: 1,
      maximumFractionDigits: 1
    }).format(value);
  }
  return `${value.toFixed(1)} ${BYTE_UNITS[unit]}/s`;
}

interface DateParts {
  year: number;
  month: number;
  day: number;
  weekday: number;
  hour: number;
  minute: number;
}

const EN_WEEKDAYS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];

function dateParts(date: Date, timeZone?: string): DateParts {
  const parts = new Intl.DateTimeFormat('en-US', {
    timeZone,
    year: 'numeric',
    month: 'numeric',
    day: 'numeric',
    weekday: 'short',
    hour: 'numeric',
    minute: 'numeric',
    hourCycle: 'h23'
  }).formatToParts(date);
  const value: Partial<Record<Intl.DateTimeFormatPartTypes, string>> = Object.fromEntries(
    parts.map(part => [part.type, part.value])
  );
  return {
    year: Number(value.year),
    month: Number(value.month),
    day: Number(value.day),
    weekday: EN_WEEKDAYS.indexOf(String(value.weekday)),
    hour: Number(value.hour),
    minute: Number(value.minute)
  };
}

const ZH_WEEKDAYS = ['周日', '周一', '周二', '周三', '周四', '周五', '周六'];
const EN_MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

/**
 * Dates omit the current year and optionally include weekday.
 * Chinese uses month/day and optional year/weekday; English examples: Sep 30, Mar 2, 2026, Sep 30 · Wed.
 */
export function formatDate(
  input: Date | string | number,
  {
    locale = 'en-US',
    now = defaultNow(),
    timeZone = dateDefaults.timeZone,
    weekday = false
  }: DateFormatOptions & { weekday?: boolean } = {}
): string {
  if (locale !== 'zh-CN' && locale !== 'en-US') {
    const year = dateParts(new Date(input), timeZone).year === dateParts(now, timeZone).year ? undefined : 'numeric';
    return new Intl.DateTimeFormat(locale, {
      timeZone,
      month: 'short',
      day: 'numeric',
      year,
      weekday: weekday ? 'short' : undefined
    }).format(new Date(input));
  }
  const date = dateParts(new Date(input), timeZone);
  const sameYear = date.year === dateParts(now, timeZone).year;
  if (locale === 'zh-CN') {
    const text = `${sameYear ? '' : `${date.year}年`}${date.month}月${date.day}日`;
    return weekday ? `${text} · ${ZH_WEEKDAYS[date.weekday]}` : text;
  }
  const text = `${EN_MONTHS[date.month - 1]} ${date.day}${sameYear ? '' : `, ${date.year}`}`;
  return weekday ? `${text} · ${EN_WEEKDAYS[date.weekday]}` : text;
}

/**
 * 24-hour time, e.g. 22:53. In the default zone from setDateDefaults it names that zone, e.g. 14:05 UTC;
 * a zone the caller passes, or the runtime's local zone, stays unnamed.
 */
export function formatTime(
  input: Date | string | number,
  { timeZone }: Pick<DateFormatOptions, 'timeZone'> = {}
): string {
  const zone = timeZone ?? dateDefaults.timeZone;
  const { hour, minute } = dateParts(new Date(input), zone);
  const time = `${String(hour).padStart(2, '0')}:${String(minute).padStart(2, '0')}`;
  return timeZone === undefined && zone ? `${time} ${zone}` : time;
}

// Calendar-day difference, unaffected by DST.
function calendarDays(from: DateParts, to: DateParts): number {
  return Math.round(
    (Date.UTC(to.year, to.month - 1, to.day) - Date.UTC(from.year, from.month - 1, from.day)) / 86_400_000
  );
}

/**
 * Relative time: under one minute just now, under one hour minutes ago, under 24 hours hours ago;
 * previous calendar day yesterday, under seven days days ago, otherwise date. Future times count as just now.
 */
export function formatRelativeTime(
  input: Date | string | number,
  { locale = 'en-US', now = defaultNow(), timeZone = dateDefaults.timeZone }: DateFormatOptions = {}
): string {
  const date = new Date(input);
  const seconds = Math.floor((now.getTime() - date.getTime()) / 1000);
  if (locale !== 'zh-CN' && locale !== 'en-US') {
    const relative = new Intl.RelativeTimeFormat(locale, { numeric: 'auto' });
    if (seconds < 60) return relative.format(0, 'second');
    if (seconds < 3600) return relative.format(-Math.floor(seconds / 60), 'minute');
    if (seconds < 86_400) return relative.format(-Math.floor(seconds / 3600), 'hour');
    const days = calendarDays(dateParts(date, timeZone), dateParts(now, timeZone));
    if (days < 7) return relative.format(-days, 'day');
    return formatDate(date, { locale, now, timeZone });
  }
  const zh = locale === 'zh-CN';
  if (seconds < 60) return zh ? '刚刚' : 'just now';
  if (seconds < 3600) {
    const minutes = Math.floor(seconds / 60);
    return zh ? `${minutes} 分钟前` : `${minutes} min ago`;
  }
  if (seconds < 86_400) {
    const hours = Math.floor(seconds / 3600);
    return zh ? `${hours} 小时前` : `${hours} ${hours === 1 ? 'hour' : 'hours'} ago`;
  }
  const days = calendarDays(dateParts(date, timeZone), dateParts(now, timeZone));
  if (days <= 1) return zh ? '昨天' : 'yesterday';
  if (days < 7) return zh ? `${days} 天前` : `${days} days ago`;
  return formatDate(date, { locale, now, timeZone });
}

/** Duration: seconds below 60, otherwise minutes and seconds. */
export function formatDuration(totalSeconds: number, { locale = 'en-US' }: FormatOptions = {}): string {
  if (locale !== 'zh-CN' && locale !== 'en-US') {
    const seconds = Math.max(0, Math.round(totalSeconds));
    const unit = (value: number, name: 'minute' | 'second') =>
      new Intl.NumberFormat(locale, { style: 'unit', unit: name, unitDisplay: 'short' }).format(value);
    return seconds < 60
      ? unit(seconds, 'second')
      : `${unit(Math.floor(seconds / 60), 'minute')} ${unit(seconds % 60, 'second')}`;
  }
  const seconds = Math.max(0, Math.round(totalSeconds));
  const zh = locale === 'zh-CN';
  if (seconds < 60) return zh ? `${seconds} 秒` : `${seconds} s`;
  const minutes = Math.floor(seconds / 60);
  const rest = seconds % 60;
  return zh ? `${minutes} 分 ${rest} 秒` : `${minutes} min ${rest} s`;
}

/** Cask versions display only the part before the build comma; keep full versions in tooltips/install details. */
export function formatVersion(version: string): string {
  const [display = ''] = version.split(',');
  return display.trim();
}

/**
 * Release-timeline label. Entries for one build with notes of its own (casks that keep 0.1 and ship
 * every build with new notes) add a short build tag, e.g. 0.1 (49); long hashes keep 12 characters.
 */
export function formatReleaseVersion(entry: { version: string; build?: string | null }): string {
  const display = formatVersion(entry.version);
  const tag = entry.build?.split(',').at(-1)?.trim() ?? '';
  if (!tag) return display;
  return `${display} (${tag.length > 20 ? tag.slice(0, 12) : tag})`;
}

// What is left of a release title after the version only restates "release notes".
const GENERIC_RELEASE_TITLE =
  /^(?:release notes?|release|notes|change ?log|update|what'?s new|build\s*\d+|版本说明|更新说明|发布说明|更新日志|リリースノート|notas de la versi[oó]n|notas de vers[aã]o|примечания к выпуску|versionshinweise|notes de version|note di rilascio)$/i;

/**
 * A release title worth showing under its version heading, or null when it only restates the app
 * name and version ("Rectangle 2.0.3", "VS Code 1.143.0 release notes"). Titles without the version,
 * such as "September 2026", are kept unless they are generic.
 */
export function releaseTitleDetail(title: string | null | undefined, version: string): string | null {
  const text = title?.trim() ?? '';
  const base = version.replace(/,.*$/, '');
  const index = text.indexOf(base);
  const rest = index < 0 ? text : text.slice(index + base.length).replace(/^,[\w.-]*/, '');
  const detail = rest.replace(/^[\s:：—–\-·|()（）]+|[\s)）]+$/g, '');
  return detail && !GENERIC_RELEASE_TITLE.test(detail) ? text : null;
}

/** Version change with spaced arrow, e.g. 1.139.1 → 1.140.0. */
export function formatVersionChange(from: string, to: string): string {
  return `${formatVersion(from)} → ${formatVersion(to)}`;
}

/** Mirror latency uses localized units. */
export function formatMilliseconds(value: number, { locale = 'en-US' }: FormatOptions = {}): string {
  return new Intl.NumberFormat(locale, {
    style: 'unit',
    unit: 'millisecond',
    unitDisplay: 'short',
    maximumFractionDigits: 0
  }).format(value);
}

/** Natural-language lists instead of fixed punctuation. */
export function formatList(values: readonly string[], { locale = 'en-US' }: FormatOptions = {}): string {
  return new Intl.ListFormat(locale, { style: 'long', type: 'conjunction' }).format(values);
}
