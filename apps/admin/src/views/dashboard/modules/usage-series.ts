export type UsagePlatform = 'desktop' | 'web';

/**
 * Categorical slots 1 (blue) and 2 (orange) of the validated chart palette, one step per mode. The pair passes the
 * lightness, chroma, colour-blind separation and 3:1 contrast checks on the admin card surfaces (#ffffff, #1a1a1a).
 */
export const USAGE_SERIES_COLORS: Record<'light' | 'dark', Record<UsagePlatform, string>> = {
  light: { desktop: '#2a78d6', web: '#eb6834' },
  dark: { desktop: '#3987e5', web: '#d95926' }
};
