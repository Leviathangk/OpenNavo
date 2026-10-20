import { isValidToken, LOCALE_METADATA, SITE_LOCALES } from '@opennavo/shared';
import type { SiteLocale } from '@opennavo/shared';

// Markdown alternates (X4, experiment, off by default): each app overview also answers as Markdown at
// <prefix>/apps/{token}.md, and the overview links it with rel=alternate type=text/markdown. The switch is
// runtimeConfig.public.markdownAlternates (NUXT_PUBLIC_MARKDOWN_ALTERNATES); the server logs every fetch by
// user agent so the data decides whether the experiment stays.

/** The runtime switch: on only for true (or the string "true" from an environment variable). */
export function markdownAlternatesEnabled(value: unknown): boolean {
  return value === true || value === 'true';
}

const MARKDOWN_PATH = /^(?:\/([a-z-]+))?\/apps\/([^/]+)\.md$/;

/** The app and language a Markdown request names, or undefined for any other path. */
export function markdownTarget(pathname: string): { locale: SiteLocale; token: string } | undefined {
  const match = MARKDOWN_PATH.exec(pathname);
  if (!match) return undefined;
  const [, prefix, token = ''] = match;
  const locale = SITE_LOCALES.find(code => LOCALE_METADATA[code].prefix === (prefix ? `/${prefix}` : ''));
  return locale && isValidToken(token) ? { locale, token } : undefined;
}

/** Whether a page path (language prefix removed) is an app overview, the pages that link a Markdown alternate. */
export function isOverviewPath(path: string): boolean {
  return /^\/apps\/[^/]+$/.test(path);
}
