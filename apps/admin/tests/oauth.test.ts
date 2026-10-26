import { beforeEach, describe, expect, it, vi } from 'vitest';
import { authorizationParams, oauthRequest } from '../src/service/api/oauth';

const refresh = vi.hoisted(() => vi.fn());
vi.mock('@/service/request', () => ({ request: { state: {} } }));
vi.mock('@/service/request/shared', () => ({
  getAuthorization: () => 'Bearer isolated-test-token',
  handleExpiredRequest: refresh
}));

beforeEach(() => {
  vi.restoreAllMocks();
  refresh.mockReset();
});
describe('MCP OAuth client', () => {
  it('preserves the authorization binding and rejects duplicate parameters', () => {
    expect(authorizationParams('state=a%2Bb&client_id=client&resource=https%3A%2F%2Fexample.test%2Fmcp')).toMatchObject(
      { state: 'a+b', client_id: 'client', resource: 'https://example.test/mcp' }
    );
    expect(() => authorizationParams('client_id=a&client_id=b')).toThrow('invalid_request');
  });
  it('refreshes an expired admin session once without leaking credentials into URLs', async () => {
    const fetcher = vi
      .spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(new Response('{}', { status: 401 }))
      .mockResolvedValueOnce(new Response('{"clientName":"Example"}', { status: 200 }));
    refresh.mockResolvedValue(true);
    expect(await oauthRequest('consent?client_id=example')).toEqual({ clientName: 'Example' });
    expect(refresh).toHaveBeenCalledTimes(1);
    expect(fetcher.mock.calls[0][0]).toBe('/oauth/consent?client_id=example');
    expect(fetcher.mock.calls[0][1]).toMatchObject({
      credentials: 'omit',
      cache: 'no-store',
      headers: { Authorization: 'Bearer isolated-test-token' }
    });
  });
  it('does not loop when refreshed credentials are rejected', async () => {
    const fetcher = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('{}', { status: 401 }));
    refresh.mockResolvedValue(true);
    await expect(oauthRequest('grants')).rejects.toThrow('oauth_request_failed');
    expect(fetcher).toHaveBeenCalledTimes(2);
  });
});
