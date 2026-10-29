import { IncomingMessage, ServerResponse } from 'node:http';
import { Socket } from 'node:net';
import { createEvent, getResponseHeader } from 'h3';
import { afterEach, describe, expect, it, vi } from 'vitest';
import markdownMiddleware from '../server/middleware/markdown-alternate';
import { edgeCacheHeader, LONG_EDGE_CACHE_VALUE, SHORT_EDGE_CACHE_VALUE } from '../server/utils/edgeCache';
import { GEO_SAMPLES } from './geoFixtures';

function eventFor(path: string, accept?: string) {
  const socket = new Socket();
  const request = new IncomingMessage(socket);
  request.url = path;
  request.method = 'GET';
  request.headers = accept ? { accept } : {};
  return { event: createEvent(request, new ServerResponse(request)), socket };
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('Accept-negotiated Markdown', () => {
  it('serves a Markdown home projection and marks it uncacheable', async () => {
    vi.stubGlobal('useRuntimeConfig', () => ({
      public: { markdownAlternates: true, siteUrl: 'https://opennavo.example' }
    }));
    const { event, socket } = eventFor('/', 'text/markdown');
    try {
      const body = await markdownMiddleware(event);
      expect(body).toMatch(/^# OpenNavo/);
      expect(getResponseHeader(event, 'content-type')).toBe('text/markdown; charset=utf-8');
      expect(getResponseHeader(event, 'vary')).toBe('Accept');
      expect(getResponseHeader(event, 'cache-control')).toBe('private, no-store');
      expect(getResponseHeader(event, 'cdn-cache-control')).toBe('no-store');
      expect(getResponseHeader(event, 'cloudflare-cdn-cache-control')).toBe('no-store');
    } finally {
      socket.destroy();
    }
  });

  it('serves an app Markdown projection at the negotiated overview URL and at .md', async () => {
    const detail = GEO_SAMPLES[0]!.detail;
    vi.stubGlobal('useRuntimeConfig', () => ({
      apiBaseInternal: 'https://api.example',
      public: {
        markdownAlternates: true,
        apiBase: 'https://api.example',
        siteUrl: 'https://opennavo.example'
      }
    }));
    vi.stubGlobal('$fetch', async () => ({ data: detail }));
    const log = vi.spyOn(console, 'info').mockImplementation(() => {});
    for (const path of [`/apps/${detail.token}`, `/apps/${detail.token}.md`]) {
      const { event, socket } = eventFor(path, 'text/markdown');
      try {
        const body = await markdownMiddleware(event);
        expect(body).toContain(`# ${detail.displayName}`);
        expect(getResponseHeader(event, 'content-type')).toBe('text/markdown; charset=utf-8');
        if (path.endsWith('.md')) {
          expect(getResponseHeader(event, 'cache-control')).toBe('s-maxage=21600, stale-while-revalidate');
          expect(getResponseHeader(event, 'cdn-cache-control')).toBeUndefined();
          expect(getResponseHeader(event, 'vary')).toBeUndefined();
          expect(getResponseHeader(event, 'cloudflare-cdn-cache-control')).toBe(
            'max-age=172800, stale-while-revalidate=604800, stale-if-error=604800'
          );
        } else {
          expect(getResponseHeader(event, 'cache-control')).toBe('private, no-store');
          expect(getResponseHeader(event, 'cdn-cache-control')).toBe('no-store');
          expect(getResponseHeader(event, 'cloudflare-cdn-cache-control')).toBe('no-store');
        }
      } finally {
        socket.destroy();
      }
    }
    expect(log).toHaveBeenCalledTimes(2);
  });

  it('renders the app named by each URL, so one Markdown request cannot borrow another page', async () => {
    const details = [GEO_SAMPLES[0]!.detail, GEO_SAMPLES[1]!.detail];
    vi.stubGlobal('useRuntimeConfig', () => ({
      apiBaseInternal: 'https://api.example',
      public: { markdownAlternates: true, apiBase: 'https://api.example', siteUrl: 'https://opennavo.example' }
    }));
    vi.stubGlobal('$fetch', async (url: string) => {
      const detail = details.find(item => url.includes(`/packages/cask/${encodeURIComponent(item.token)}`));
      return { data: detail };
    });
    for (const detail of details) {
      const { event, socket } = eventFor(`/apps/${detail.token}`, 'text/markdown');
      try {
        const body = await markdownMiddleware(event);
        expect(body).toContain(`# ${detail.displayName}`);
        expect(body).not.toContain(`# ${details.find(item => item !== detail)!.displayName}`);
      } finally {
        socket.destroy();
      }
    }
  });

  it.each(['text/html', '*/*'])('leaves an ordinary browser/crawler response as HTML: %s', async accept => {
    vi.stubGlobal('useRuntimeConfig', () => ({
      public: { markdownAlternates: true, siteUrl: 'https://opennavo.example' }
    }));
    const { event, socket } = eventFor('/apps/iterm2', accept);
    try {
      await expect(markdownMiddleware(event)).resolves.toBeUndefined();
      expect(getResponseHeader(event, 'content-type')).toBeUndefined();
      expect(getResponseHeader(event, 'vary')).toBe('Accept');
    } finally {
      socket.destroy();
    }
  });

  it('leaves the OpenNavo catalog token for its canonical /download redirect', async () => {
    vi.stubGlobal('useRuntimeConfig', () => ({
      public: { markdownAlternates: true, siteUrl: 'https://opennavo.example' }
    }));
    const { event, socket } = eventFor('/apps/opennavo', 'text/markdown');
    try {
      await expect(markdownMiddleware(event)).resolves.toBeUndefined();
      expect(getResponseHeader(event, 'content-type')).toBeUndefined();
    } finally {
      socket.destroy();
    }
  });

  it('keeps normal HTML and the explicit Markdown URL on the public app cache policy', () => {
    // These are the policies emitted by Nitro's / and /apps/** rules, then by edge-cache.ts on origin/main.
    expect(
      edgeCacheHeader({
        enabled: true,
        method: 'GET',
        path: '/',
        status: 200,
        contentType: 'text/html; charset=utf-8',
        cacheControl: 's-maxage=300, stale-while-revalidate',
        setCookie: false,
        authorization: false
      })
    ).toBe(SHORT_EDGE_CACHE_VALUE);
    expect(
      edgeCacheHeader({
        enabled: true,
        method: 'GET',
        path: '/apps/iterm2',
        status: 200,
        contentType: 'text/html; charset=utf-8',
        cacheControl: 's-maxage=21600, stale-while-revalidate',
        setCookie: false,
        authorization: false
      })
    ).toBe(LONG_EDGE_CACHE_VALUE);
  });
});
