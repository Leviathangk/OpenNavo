// Public API client (04 §7.1): openapi-fetch and common headers; unwrap() decodes {code, msg, data}.
import createClient from 'openapi-fetch';
import type { Client, Middleware } from 'openapi-fetch';
import { ErrorCode } from '@opennavo/shared';
import type { Locale } from '@opennavo/shared';
import { ApiError, NETWORK_ERROR, toApiError } from './errors';
import type { paths } from './public';

export type PublicClient = Client<paths>;

export interface PublicClientOptions {
  /** For example https://api.opennavo.example/api/v1, or http://localhost:4010 for Prism. */
  baseUrl: string;
  /** Request language; a function is reevaluated for every request to follow UI changes. */
  locale?: Locale | (() => Locale);
  /** Client identifier (04 §6.2), used only for server statistics/compatibility. */
  platform?: 'web' | 'desktop';
  version?: string;
  /**
   * Response shapes this client understands, sent as X-OpenNavo-Caps. `link`: lists may include link apps
   * (kind `link`, official-site or GitHub downloads; store/security plan §4). Released clients send nothing and keep
   * getting Homebrew apps only.
   */
  capabilities?: readonly 'link'[];
  fetch?: typeof globalThis.fetch;
}

function clientHeaders({ locale, platform, version, capabilities }: PublicClientOptions): Middleware {
  return {
    onRequest({ request }) {
      const current = typeof locale === 'function' ? locale() : locale;
      if (current) request.headers.set('Accept-Language', current);
      if (platform) request.headers.set('X-Client-Platform', platform);
      if (version) request.headers.set('X-Client-Version', version);
      if (capabilities?.length) request.headers.set('X-OpenNavo-Caps', capabilities.join(','));
      return request;
    },
    onError({ error }) {
      // No response: normalize to ApiError for offline/retry UI.
      return new ApiError({
        code: NETWORK_ERROR,
        msg: error instanceof Error ? error.message : String(error),
        status: 0,
        requestId: null,
        cause: error
      });
    }
  };
}

export function createPublicClient(options: PublicClientOptions): PublicClient {
  const client = createClient<paths>({ baseUrl: options.baseUrl, fetch: options.fetch });
  client.use(clientHeaders(options));
  return client;
}

interface Envelope<T> {
  code: string;
  msg: string;
  data: T;
}

type FetchResult<T> =
  | { data: Envelope<T>; error?: never; response: Response }
  | { data?: never; error: unknown; response: Response };

/**
 * Unwrap the common envelope: return data on success, otherwise throw ApiError.
 *
 * @example
 *   const pkg = await unwrap(api.GET('/packages/{kind}/{token}', { params: { path: { kind, token } } }));
 */
export async function unwrap<T>(pending: Promise<FetchResult<T>>): Promise<T> {
  const { data, error, response } = await pending;
  if (data === undefined) throw toApiError(error, response);
  if (data.code !== ErrorCode.OK) {
    throw new ApiError({
      code: data.code,
      msg: data.msg,
      status: response.status,
      requestId: response.headers.get('X-Request-Id')
    });
  }
  return data.data;
}
