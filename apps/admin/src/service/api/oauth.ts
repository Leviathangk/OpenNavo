import { request } from '@/service/request';
import { getAuthorization, handleExpiredRequest } from '@/service/request/shared';

export interface OAuthConsent {
  clientName: string;
  clientId: string;
  redirectUri: string;
  requestedScopes: string[];
  allowedScopes: string[];
}
export interface OAuthGrant {
  id: number;
  name: string;
  scopes: string;
  allowDelete: boolean;
  expiresAt: string;
  revokedAt: string | null;
}

export interface OAuthGrantPage {
  items: OAuthGrant[];
  nextCursor: string | null;
}

/** OAuth uses protocol JSON rather than the admin API envelope. Never put credentials in URLs. */
export async function oauthRequest<T>(path: string, init: RequestInit = {}, retry = true): Promise<T> {
  const authorization = getAuthorization();
  const response = await fetch(`/oauth/${path}`, {
    ...init,
    credentials: 'omit',
    cache: 'no-store',
    headers: {
      ...(init.body ? { 'Content-Type': 'application/json' } : {}),
      ...(authorization ? { Authorization: authorization } : {})
    }
  });
  if (response.status === 401 && retry && (await handleExpiredRequest(request.state))) {
    return oauthRequest<T>(path, init, false);
  }
  if (!response.ok) throw new Error('oauth_request_failed');
  return response.json() as Promise<T>;
}

const authorizationKeys = [
  'client_id',
  'redirect_uri',
  'response_type',
  'scope',
  'state',
  'code_challenge',
  'code_challenge_method',
  'resource'
] as const;

export function authorizationParams(query: string): Record<string, string> {
  const params = new URLSearchParams(query);
  const result: Record<string, string> = {};
  for (const key of authorizationKeys) {
    if (params.getAll(key).length > 1) throw new Error('invalid_request');
    result[key] = params.get(key) || '';
  }
  return result;
}
