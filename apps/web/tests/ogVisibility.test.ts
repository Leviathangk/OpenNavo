import { IncomingMessage, ServerResponse } from 'node:http';
import { Socket } from 'node:net';
import { createEvent } from 'h3';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { checkPackageOgVisibility, packageOgGuard } from '../server/utils/ogVisibility';

function event(path: string) {
  const socket = new Socket();
  const request = new IncomingMessage(socket);
  request.url = path;
  request.method = 'GET';
  return createEvent(request, new ServerResponse(request));
}

function encoded(value: string) {
  const base64 = Buffer.from(value, 'utf8')
    .toString('base64')
    .replace(/=+$/g, '')
    .replace(/\+/g, '-')
    .replace(/\//g, '~');
  return `~${base64}`;
}

function imagePath(kind: string, token: string, options: { locale?: string; visibility?: boolean } = {}) {
  const parts = ['c_OgPackageSatori', `locale_${options.locale ?? 'en-US'}`];
  if (options.visibility !== false) parts.push(`visibility_${encoded(`${kind}:${token}`)}`);
  parts.push('ch_0123456789abcdef', 's_test-signature');
  return `/_og/d/${parts.join(',')}.png`;
}

function stubApi(fetch: ReturnType<typeof vi.fn>) {
  vi.stubGlobal('useRuntimeConfig', () => ({
    apiBaseInternal: 'https://api.test',
    public: { apiBase: 'https://api.test', siteUrl: 'https://example.test' }
  }));
  vi.stubGlobal('$fetch', fetch);
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('package share-image visibility', () => {
  it('rejects a private package image even when the image key is cached', async () => {
    // The guard runs before nuxt-og-image's immutable buffer cache, so a cached PNG cannot be served.
    const fetch = vi.fn(async () => {
      throw { statusCode: 404 };
    });
    stubApi(fetch);
    const decision = await checkPackageOgVisibility(event(imagePath('cask', 'private-app')));
    expect(decision).toEqual({ status: 404 });
    expect(fetch).toHaveBeenCalled();
  });

  it('fails closed when the detail read returns an upstream error', async () => {
    const fetch = vi.fn(async () => {
      throw { statusCode: 503 };
    });
    stubApi(fetch);
    expect(await checkPackageOgVisibility(event(imagePath('cask', 'public-app')))).toEqual({ status: 503 });
    expect(fetch).toHaveBeenCalled();
  });

  it('serves a public package image', async () => {
    const fetch = vi.fn(async () => ({
      data: { kind: 'cask', token: 'public-app', displayName: 'Public App' }
    }));
    stubApi(fetch);
    expect(await checkPackageOgVisibility(event(imagePath('cask', 'public-app')))).toBeUndefined();
  });

  it('fails closed when the guard cannot be parsed', async () => {
    const fetch = vi.fn();
    stubApi(fetch);
    expect(packageOgGuard(imagePath('cask', 'private-app', { visibility: false }))).toBeNull();
    expect(await checkPackageOgVisibility(event(imagePath('cask', 'private-app', { visibility: false })))).toEqual({
      status: 404
    });
    expect(fetch).not.toHaveBeenCalled();
  });

  it('performs exactly one cached detail read per request', async () => {
    const fetch = vi.fn(async () => ({
      data: { kind: 'cask', token: 'public-app', displayName: 'Public App' }
    }));
    stubApi(fetch);
    await checkPackageOgVisibility(event(imagePath('cask', 'public-app', { locale: 'zh-CN' })));
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(fetch).toHaveBeenCalledWith('https://api.test/packages/cask/public-app', {
      query: { locale: 'zh-CN' },
      headers: { 'X-Client-Platform': 'web', 'X-OpenNavo-Caps': 'link' }
    });
  });
});
