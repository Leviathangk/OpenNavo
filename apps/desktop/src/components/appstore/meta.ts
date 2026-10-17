import type { Locale } from '@opennavo/shared';

/** Storefront name in the interface language, e.g. us → 美国 / United States; falls back to the code. */
export function masRegionName(country: string, locale: Locale): string {
  try {
    return new Intl.DisplayNames([locale], { type: 'region' }).of(country.toUpperCase()) ?? country.toUpperCase();
  } catch {
    return country.toUpperCase();
  }
}

/** App Store product page in the browser, for copying or when the App Store app is unavailable. */
export const masWebUrl = (country: string, adamId: number) => `https://apps.apple.com/${country}/app/id${adamId}?mt=12`;
