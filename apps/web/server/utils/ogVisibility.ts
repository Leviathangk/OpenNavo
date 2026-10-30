import type { H3Event } from 'h3';
import type { PackageKind, SiteLocale } from '@opennavo/shared';
import { isPackageKind, SITE_LOCALES } from '@opennavo/shared';
import { apiStatus, fetchPackageDetail, publicApi } from './publicApi';

const OG_PACKAGE_COMPONENT = 'OgPackageSatori';

/** A package share image's signed guard and the locale it renders in. */
export interface PackageOgGuard {
  kind: PackageKind;
  token: string;
  locale: SiteLocale;
}

/** The HTTP status a share-image request must answer with, or undefined when it may render. */
export interface OgVisibilityDecision {
  status: number;
}

function decodeBase64Url(value: string): string {
  const standard = value.replace(/-/g, '+').replace(/~/g, '/');
  const padded = standard + '='.repeat((4 - (standard.length % 4)) % 4);
  return Buffer.from(padded, 'base64').toString('utf8');
}

// Mirror nuxt-og-image's simple parameter encoding (shared/urlEncoding.js): a "~" value is base64,
// "~~" escapes a literal leading tilde, "__" is an escaped underscore and "+" is a space.
function decodeOgValue(raw: string): string {
  if (raw.startsWith('~~')) return decodeURIComponent(raw.slice(1).replace(/\+/g, '%20')).replace(/__/g, '_');
  if (raw.startsWith('~')) {
    try {
      return decodeBase64Url(raw.slice(1));
    } catch {
      return decodeURIComponent(raw.replace(/\+/g, '%20')).replace(/__/g, '_');
    }
  }
  return decodeURIComponent(raw.replace(/\+/g, '%20')).replace(/__/g, '_');
}

function parseOgParams(path: string): Map<string, string> | undefined {
  const pathname = path.split('?', 1)[0] ?? '';
  const marker = pathname.indexOf('/_og/');
  if (marker < 0) return undefined;
  const rest = pathname.slice(marker + '/_og/'.length);
  const slash = rest.indexOf('/');
  if (slash < 0) return undefined;
  let encoded = rest.slice(slash + 1);
  const extension = /\.(?:png|jpe?g|webp|svg|html|json)$/i.exec(encoded);
  if (extension) encoded = encoded.slice(0, -extension[0].length);
  encoded = encoded.replace(/,s_[^,]+$/, '');
  // Hash URLs only exist during prerendering; the runtime handler rejects them before rendering.
  if (/^o_[a-z0-9]+$/i.test(encoded)) return undefined;
  const params = new Map<string, string>();
  for (const part of encoded.split(',')) {
    const separator = part.indexOf('_');
    if (separator <= 0) continue;
    const alias = part.slice(0, separator);
    params.set(alias === 'c' ? 'component' : alias, decodeOgValue(part.slice(separator + 1)));
  }
  return params;
}

/**
 * The guard carried by a package share image. undefined means the request is not a package image,
 * null means it is one without a usable kind/token guard and must fail closed.
 */
export function packageOgGuard(path: string): PackageOgGuard | null | undefined {
  let params: Map<string, string> | undefined;
  try {
    params = parseOgParams(path);
  } catch (error) {
    // A malformed percent-escape (decodeURIComponent URIError) cannot carry a usable guard:
    // fail closed with 404 instead of letting the error surface as a 500.
    if (error instanceof URIError) return null;
    throw error;
  }
  if (!params || params.get('component') !== OG_PACKAGE_COMPONENT) return undefined;
  const visibility = params.get('visibility');
  if (!visibility) return null;
  const separator = visibility.indexOf(':');
  if (separator <= 0 || separator === visibility.length - 1) return null;
  const kind = visibility.slice(0, separator);
  const token = visibility.slice(separator + 1);
  if (!isPackageKind(kind) || !token) return null;
  const requested = params.get('locale');
  const locale = SITE_LOCALES.find(value => value === requested) ?? 'en-US';
  return { kind, token, locale };
}

async function lifecycleState(
  api: ReturnType<typeof publicApi>,
  kind: PackageKind,
  token: string,
  locale: SiteLocale
): Promise<string | undefined> {
  const kinds: PackageKind[] = kind === 'cask' ? ['cask', 'link'] : [kind];
  for (const candidate of kinds) {
    try {
      const lifecycle = await api.get<{ state?: string }>(
        `/packages/${candidate}/${encodeURIComponent(token)}/lifecycle`,
        { locale }
      );
      return lifecycle.state;
    } catch (error) {
      if (apiStatus(error) !== 404) throw error;
    }
  }
  return undefined;
}

/**
 * Reuses the public detail read (and its visibility-generation cache) once per share image. A 404
 * means private or unknown, so the image is rejected before nuxt-og-image can serve a cached PNG.
 * The lifecycle read only distinguishes a removed app (whose image still renders) from private;
 * it also excludes private apps, so private always wins.
 */
export async function checkPackageOgVisibility(event: H3Event): Promise<OgVisibilityDecision | undefined> {
  if (event.method !== 'GET' && event.method !== 'HEAD') return undefined;
  const guard = packageOgGuard(event.path ?? event.node.req.url ?? '');
  if (guard === undefined) return undefined;
  if (guard === null) return { status: 404 };
  const api = publicApi(event);
  try {
    const detail = await fetchPackageDetail(api, guard.token, guard.locale);
    if (detail) return undefined;
    if ((await lifecycleState(api, guard.kind, guard.token, guard.locale)) === 'removed') return undefined;
    return { status: 404 };
  } catch (error) {
    // A removed detail response (410) is not private; keep the existing image.
    if (apiStatus(error) === 410) return undefined;
    return { status: 503 };
  }
}
