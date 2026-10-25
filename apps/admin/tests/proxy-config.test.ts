import { describe, expect, it } from 'vitest';
import { createViteProxy } from '../build/config/proxy';
import { getServiceBaseURL } from '../src/utils/service';

function env(baseURL: string): Env.ImportMeta {
  return {
    VITE_SERVICE_BASE_URL: baseURL,
    VITE_OTHER_SERVICE_BASE_URL: '{}',
    VITE_HTTP_PROXY: 'Y',
    VITE_PROXY_LOG: 'N'
  } as Env.ImportMeta;
}

describe('admin development proxy configuration', () => {
  it('keeps a relative production API path on the same origin', () => {
    const config = env('/admin-api');
    expect(createViteProxy(config, true)).toEqual({});
    expect(getServiceBaseURL(config, true).baseURL).toBe('/admin-api');
  });
  it('routes OAuth to the origin of an absolute backend, without its admin-api path', () => {
    const config = env('http://localhost:8080/admin-api');
    const proxy = createViteProxy(config, true);
    expect(proxy?.['/oauth/']).toMatchObject({ target: 'http://localhost:8080', changeOrigin: true });
    expect(proxy?.['/proxy-default']).toMatchObject({ target: 'http://localhost:8080/admin-api' });
    expect(getServiceBaseURL(config, true).baseURL).toBe('/proxy-default');
  });
  it('does not create proxies for builds or when proxying is disabled', () => {
    expect(createViteProxy(env('/admin-api'), false)).toBeUndefined();
    expect(createViteProxy({ ...env('/admin-api'), VITE_HTTP_PROXY: 'N' }, true)).toBeUndefined();
  });
});
