import { IncomingMessage, ServerResponse } from 'node:http';
import { Socket } from 'node:net';
import { createEvent, getResponseHeader, setHeader } from 'h3';
import { LOCALE_METADATA, SITE_LOCALES } from '@opennavo/shared';
import { afterEach, describe, expect, it, vi } from 'vitest';
import markdownMiddleware from '../server/middleware/markdown-alternate';
import { edgeCacheHeader, LONG_EDGE_CACHE_VALUE, SHORT_EDGE_CACHE_VALUE } from '../server/utils/edgeCache';
import { GEO_SAMPLES } from './geoFixtures';
import { DEFAULT_SITE_SETTINGS } from '../server/utils/siteSettings';

const OPEN_LOCALES = ['en-US', 'zh-CN', 'ja-JP', 'es-ES', 'ru-RU'] as const;

function eventFor(path: string, accept?: string, edgeCacheEnabled = true) {
  const socket = new Socket();
  const request = new IncomingMessage(socket);
  request.url = path;
  request.method = 'GET';
  request.headers = accept ? { accept } : {};
  const event = createEvent(request, new ServerResponse(request));
  event.context.siteSettings = {
    ...DEFAULT_SITE_SETTINGS,
    edgeCache: { enabled: edgeCacheEnabled },
    pages: { ...DEFAULT_SITE_SETTINGS.pages, openLocales: [...OPEN_LOCALES] }
  };
  return { event, socket };
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
      expect(getResponseHeader(event, 'x-robots-tag')).toBe('noindex');
      expect(getResponseHeader(event, 'link')).toBe('<https://opennavo.example/>; rel="canonical"');
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
        expect(getResponseHeader(event, 'x-robots-tag')).toBe('noindex');
        expect(getResponseHeader(event, 'link')).toBe(
          `<https://opennavo.example/apps/${detail.token}>; rel="canonical"`
        );
        if (path.endsWith('.md')) {
          expect(getResponseHeader(event, 'cache-control')).toBe('s-maxage=21600, stale-while-revalidate');
          expect(getResponseHeader(event, 'cdn-cache-control')).toBeUndefined();
          expect(getResponseHeader(event, 'vary')).toBeUndefined();
          expect(getResponseHeader(event, 'cloudflare-cdn-cache-control')).toBe(LONG_EDGE_CACHE_VALUE);
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

  it.each(SITE_LOCALES)('serves Markdown only when the locale is open: %s', async locale => {
    const detail = GEO_SAMPLES[0]!.detail;
    vi.stubGlobal('useRuntimeConfig', () => ({
      apiBaseInternal: 'https://api.example',
      public: { markdownAlternates: true, apiBase: 'https://api.example', siteUrl: 'https://opennavo.example/' }
    }));
    const fetch = vi.fn(async () => ({ data: detail }));
    vi.stubGlobal('$fetch', fetch);
    vi.spyOn(console, 'info').mockImplementation(() => {});
    const prefix = LOCALE_METADATA[locale].prefix;
    const open = OPEN_LOCALES.some(code => code === locale);
    for (const method of ['GET', 'HEAD'])
      for (const path of [prefix || '/', `${prefix}/apps/${detail.token}`, `${prefix}/apps/${detail.token}.md`]) {
        const explicit = path.endsWith('.md');
        const { event, socket } = eventFor(`${path}?tracking=fixture`, explicit ? 'text/html' : 'text/markdown');
        event.node.req.method = method;
        const previousLink = '</font.woff>; rel="preload"';
        setHeader(event, 'Link', previousLink);
        try {
          if (!open) {
            if (explicit) await expect(markdownMiddleware(event)).rejects.toMatchObject({ statusCode: 404 });
            else await expect(markdownMiddleware(event)).resolves.toBeUndefined();
            expect(getResponseHeader(event, 'content-type')).toBeUndefined();
            expect(getResponseHeader(event, 'link')).toBe(previousLink);
            continue;
          }
          expect(await markdownMiddleware(event)).toBeTypeOf('string');
          expect(getResponseHeader(event, 'content-type')).toBe('text/markdown; charset=utf-8');
          expect(getResponseHeader(event, 'x-robots-tag')).toBe('noindex');
          const htmlPath = explicit ? path.slice(0, -3) : path;
          expect(getResponseHeader(event, 'link')).toBe(
            `${previousLink}, <https://opennavo.example${htmlPath}>; rel="canonical"`
          );
        } finally {
          socket.destroy();
        }
      }
    if (!open) expect(fetch).not.toHaveBeenCalled();
  });

  it('follows the HTML edge-cache switch for explicit Markdown URLs', async () => {
    const detail = GEO_SAMPLES[0]!.detail;
    vi.stubGlobal('useRuntimeConfig', () => ({
      apiBaseInternal: 'https://api.example',
      public: { markdownAlternates: true, apiBase: 'https://api.example', siteUrl: 'https://opennavo.example' }
    }));
    vi.stubGlobal('$fetch', async () => ({ data: detail }));
    for (const enabled of [true, false]) {
      const { event, socket } = eventFor(`/apps/${detail.token}.md`, undefined, enabled);
      try {
        await markdownMiddleware(event);
        expect(getResponseHeader(event, 'cache-control')).toBe('s-maxage=21600, stale-while-revalidate');
        expect(getResponseHeader(event, 'cloudflare-cdn-cache-control')).toBe(
          enabled ? LONG_EDGE_CACHE_VALUE : 'no-store'
        );
      } finally {
        socket.destroy();
      }
    }
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
      // The HTML beforeResponse hook adds Vary only after a successful render.
      expect(getResponseHeader(event, 'vary')).toBeUndefined();
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

  it.each(['', '/zh'])('canonicalizes the explicit OpenNavo Markdown projection to %s/download', async prefix => {
    const detail = { ...GEO_SAMPLES[0]!.detail, token: 'opennavo' };
    vi.stubGlobal('useRuntimeConfig', () => ({
      apiBaseInternal: 'https://api.example',
      public: { markdownAlternates: true, apiBase: 'https://api.example', siteUrl: 'https://opennavo.example' }
    }));
    vi.stubGlobal('$fetch', async () => ({ data: detail }));
    vi.spyOn(console, 'info').mockImplementation(() => {});
    const { event, socket } = eventFor(`${prefix}/apps/opennavo.md`);
    try {
      await markdownMiddleware(event);
      expect(getResponseHeader(event, 'link')).toBe(`<https://opennavo.example${prefix}/download>; rel="canonical"`);
      expect(getResponseHeader(event, 'x-robots-tag')).toBe('noindex');
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
