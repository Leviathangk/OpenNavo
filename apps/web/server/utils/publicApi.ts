import type { PublicComponents } from '@opennavo/api';
import type { H3Event } from 'h3';
import type { SiteLocale } from '@opennavo/shared';

type PackageDetail = PublicComponents['schemas']['PackageDetail'];

// Public API reads for server routes (F6 feeds, X4 Markdown): the internal API address, the headers the
// web client sends (link apps included) and the visitor's address for the API's rate limit. Responses are
// unwrapped from {code, msg, data}; API errors keep their HTTP status.
export function publicApi(event: H3Event) {
  const config = useRuntimeConfig(event);
  const base = (config.apiBaseInternal || config.public.apiBase).replace(/\/$/, '');
  const headers: Record<string, string> = { 'X-Client-Platform': 'web', 'X-OpenNavo-Caps': 'link' };
  const clientIP = event.context.publicApiClientIP;
  if (typeof clientIP === 'string') headers['X-Forwarded-For'] = clientIP;
  return {
    siteUrl: config.public.siteUrl.replace(/\/$/, ''),
    get: <T>(path: string, query: Record<string, string | number>) =>
      $fetch<{ data: T }>(`${base}${path}`, { query, headers }).then(response => response.data)
  };
}

/** HTTP status of a failed API read. */
export function apiStatus(error: unknown): number | undefined {
  const value = error as { statusCode?: number; response?: { status?: number } };
  return value.statusCode ?? value.response?.status;
}

/** An app's detail as its page finds it: casks first, then link apps (usePackage); undefined when neither exists. */
export async function fetchPackageDetail(
  api: ReturnType<typeof publicApi>,
  token: string,
  locale: SiteLocale
): Promise<PackageDetail | undefined> {
  for (const kind of ['cask', 'link'] as const) {
    try {
      return await api.get<PackageDetail>(`/packages/${kind}/${encodeURIComponent(token)}`, { locale });
    } catch (error) {
      if (apiStatus(error) !== 404) throw error;
    }
  }
  return undefined;
}
