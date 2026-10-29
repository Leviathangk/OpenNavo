import { isValidToken, LOCALE_METADATA, SITE_LOCALES } from '@opennavo/shared';
import type { SiteLocale } from '@opennavo/shared';

// Markdown alternates (X4): each app overview also answers as Markdown at <prefix>/apps/{token}.md, and
// the overview links it with rel=alternate type=text/markdown. The switch remains runtime-configurable so
// an operator can turn the projection off without a rebuild; production enables it by default.

/** The runtime switch: on only for true (or the string "true" from an environment variable). */
export function markdownAlternatesEnabled(value: unknown): boolean {
  return value === true || value === 'true';
}

const MARKDOWN_PATH = /^(?:\/([a-z-]+))?\/apps\/([^/]+)\.md$/;
const APP_PATH = /^(?:\/([a-z-]+))?\/apps\/([^/]+)$/;

export type MarkdownPageTarget =
  | { kind: 'home'; locale: SiteLocale }
  | { kind: 'app'; locale: SiteLocale; token: string };

function localeForPrefix(prefix: string | undefined): SiteLocale | undefined {
  return SITE_LOCALES.find(code => LOCALE_METADATA[code].prefix === (prefix ? `/${prefix}` : ''));
}

/** Whether an Accept header explicitly prefers Markdown over HTML. */
export function prefersMarkdown(accept: string | undefined): boolean {
  if (!accept) return false;
  let markdown: { quality: number; order: number } | undefined;
  let html: { quality: number; order: number } | undefined;
  let textWildcard: { quality: number; order: number } | undefined;
  let anyWildcard: { quality: number; order: number } | undefined;
  accept.split(',').forEach((part, order) => {
    const [rawMediaType, ...parameters] = part.trim().toLowerCase().split(';');
    const mediaType = rawMediaType?.trim();
    if (!mediaType) return;
    const qualityParameter = parameters.find(parameter => parameter.trim().startsWith('q='));
    const parsedQuality = qualityParameter ? Number(qualityParameter.trim().slice(2)) : 1;
    const quality = Number.isFinite(parsedQuality) ? Math.max(0, Math.min(1, parsedQuality)) : 0;
    if (mediaType === 'text/markdown') markdown = { quality, order };
    if (mediaType === 'text/html') html = { quality, order };
    if (mediaType === 'text/*') textWildcard = { quality, order };
    if (mediaType === '*/*') anyWildcard = { quality, order };
  });
  if (!markdown || markdown.quality <= 0) return false;
  const htmlMatch = html ?? textWildcard ?? anyWildcard;
  if (!htmlMatch || htmlMatch.quality <= 0) return true;
  return (
    markdown.quality > htmlMatch.quality || (markdown.quality === htmlMatch.quality && markdown.order < htmlMatch.order)
  );
}

/** The page represented by a negotiable Markdown request, or undefined for other routes. */
export function markdownPageTarget(pathname: string): MarkdownPageTarget | undefined {
  const clean = pathname.split('?', 1)[0] ?? '';
  if (clean === '/' || SITE_LOCALES.some(locale => LOCALE_METADATA[locale].prefix === clean && clean !== '')) {
    const prefix = clean === '/' ? undefined : clean.slice(1);
    const locale = localeForPrefix(prefix);
    return locale ? { kind: 'home', locale } : undefined;
  }
  const match = APP_PATH.exec(clean);
  if (!match) return undefined;
  const [, prefix, token = ''] = match;
  const locale = localeForPrefix(prefix);
  return locale && isValidToken(token) ? { kind: 'app', locale, token } : undefined;
}

/** The app and language a Markdown request names, or undefined for any other path. */
export function markdownTarget(pathname: string): { locale: SiteLocale; token: string } | undefined {
  const match = MARKDOWN_PATH.exec(pathname.split('?', 1)[0] ?? '');
  if (!match) return undefined;
  const [, prefix, token = ''] = match;
  const locale = localeForPrefix(prefix);
  return locale && isValidToken(token) ? { locale, token } : undefined;
}

/** Whether a page path (language prefix removed) is an app overview, the pages that link a Markdown alternate. */
export function isOverviewPath(path: string): boolean {
  return markdownPageTarget(path)?.kind === 'app';
}

/** Whether an HTML route has a Markdown representation selected by Accept negotiation. */
export function supportsMarkdownNegotiation(path: string): boolean {
  return markdownTarget(path) === undefined && markdownPageTarget(path) !== undefined;
}
