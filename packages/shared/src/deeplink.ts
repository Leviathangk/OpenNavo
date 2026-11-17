// Deep links (06 §10): web constructs, client parses; Rust uses identical rules, with this parser for web/tests.
// Links only open pages or request confirmed installation; never execute writes directly.
import { BRAND } from './brand';
import { isValidToken } from './commands';
import { isPackageKind } from './types';
import type { PackageKind } from './types';

export const SLUG_PATTERN = /^[a-z0-9][a-z0-9-]{0,63}$/;
export const SEARCH_QUERY_MAX_LENGTH = 64;

export type DeepLinkTarget =
  | { type: 'package'; kind: PackageKind; token: string; action?: 'install' }
  | { type: 'collection'; slug: string; action?: 'install' }
  | { type: 'search'; q: string }
  | { type: 'updates' }
  | { type: 'route'; route: ClientRoute };

/** Desktop pages a web link may open. Navigation only: no query and no action. */
export const CLIENT_ROUTES = ['installed', 'updates', 'cleanup', 'monitor', 'security', 'firewall'] as const;
export type ClientRoute = (typeof CLIENT_ROUTES)[number];

function truncate(text: string, length: number): string {
  return Array.from(text).slice(0, length).join('');
}

/** Build links, throwing on invalid arguments; web callers use validated API values. */
export function buildDeepLink(target: DeepLinkTarget): string {
  const base = `${BRAND.scheme}://`;
  const action = 'action' in target && target.action === 'install' ? '?action=install' : '';
  switch (target.type) {
    case 'package':
      if (!isPackageKind(target.kind) || !isValidToken(target.token))
        throw new Error('Invalid package deep-link arguments');
      return `${base}package/${target.kind}/${encodeURIComponent(target.token)}${action}`;
    case 'collection':
      if (!SLUG_PATTERN.test(target.slug)) throw new Error('Invalid collection deep-link arguments');
      return `${base}collection/${target.slug}${action}`;
    case 'search':
      return `${base}search?q=${encodeURIComponent(truncate(target.q, SEARCH_QUERY_MAX_LENGTH))}`;
    case 'updates':
      return `${base}updates`;
    case 'route':
      if (!CLIENT_ROUTES.includes(target.route)) throw new Error('Invalid client route');
      return `${base}${target.route}`;
  }
}

function decode(segment: string): string | null {
  try {
    return decodeURIComponent(segment);
  } catch {
    return null;
  }
}

/** Parse links; invalid scheme/path/arguments return null, causing the client only to open its main window. */
export function parseDeepLink(input: string): DeepLinkTarget | null {
  let url: URL;
  try {
    url = new URL(input.trim());
  } catch {
    return null;
  }
  if (url.protocol.toLowerCase() !== `${BRAND.scheme}:`) return null;
  // Keep the same authority and path shape Rust accepts: no credentials, port or fragment, and no
  // trailing slash that would be a different link there.
  if (url.username || url.password || url.port || url.href.includes('#') || url.pathname.endsWith('/')) return null;

  const route = url.hostname.toLowerCase();
  const segments = url.pathname.split('/').filter(Boolean).map(decode);
  if (segments.includes(null)) return null;
  const [first, second] = segments as string[];
  const action = url.searchParams.get('action') === 'install' ? { action: 'install' as const } : {};

  switch (route) {
    case 'package':
      if (segments.length !== 2 || !isPackageKind(first) || second === undefined || !isValidToken(second)) return null;
      return { type: 'package', kind: first, token: second, ...action };
    case 'collection':
      if (segments.length !== 1 || first === undefined || !SLUG_PATTERN.test(first)) return null;
      return { type: 'collection', slug: first, ...action };
    case 'search': {
      const q = (url.searchParams.get('q') ?? '').trim();
      if (segments.length !== 0 || !q) return null;
      return { type: 'search', q: truncate(q, SEARCH_QUERY_MAX_LENGTH) };
    }
    case 'updates':
      return segments.length === 0 ? { type: 'updates' } : null;
    case 'installed':
    case 'cleanup':
    case 'monitor':
    case 'security':
    case 'firewall':
      return segments.length === 0 && !url.searchParams.toString() ? { type: 'route', route } : null;
    default:
      return null;
  }
}
