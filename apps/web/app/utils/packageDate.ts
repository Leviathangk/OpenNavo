import type { SiteLocale } from '@opennavo/shared';

/** Stable factual dates: include the year and use UTC on both server and client. */
export function formatDate(input: Date | string | number, { locale = 'en-US' }: { locale?: SiteLocale } = {}): string {
  const date = new Date(input);
  if (!Number.isFinite(date.getTime())) return '';
  return new Intl.DateTimeFormat(locale, { year: 'numeric', month: 'short', day: 'numeric', timeZone: 'UTC' }).format(
    date
  );
}
