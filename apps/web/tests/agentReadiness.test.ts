import { IncomingMessage, ServerResponse } from 'node:http';
import { Socket } from 'node:net';
import { createEvent, getResponseHeader } from 'h3';
import { afterEach, describe, expect, it, vi } from 'vitest';
import markdownMiddleware from '../server/middleware/markdown-alternate';
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
      expect(getResponseHeader(event, 'cache-control')).toBe('no-store');
      expect(getResponseHeader(event, 'cloudflare-cdn-cache-control')).toBe('no-store');
    } finally {
      socket.destroy();
    }
  });

  it('serves an app Markdown projection at the negotiated overview URL and at .md', async () => {
    const detail = GEO_SAMPLES[0]!.detail;
    vi.stubGlobal('useRuntimeConfig', () => ({
      apiBaseInternal: 'https://api.example',
      public: { markdownAlternates: true, apiBase: 'https://api.example', siteUrl: 'https://opennavo.example' }
    }));
    vi.stubGlobal('$fetch', async () => ({ data: detail }));
    const log = vi.spyOn(console, 'info').mockImplementation(() => {});
    for (const path of [`/apps/${detail.token}`, `/apps/${detail.token}.md`]) {
      const { event, socket } = eventFor(path, 'text/markdown');
      try {
        const body = await markdownMiddleware(event);
        expect(body).toContain(`# ${detail.displayName}`);
        expect(getResponseHeader(event, 'content-type')).toBe('text/markdown; charset=utf-8');
        expect(getResponseHeader(event, 'cache-control')).toBe('no-store');
      } finally {
        socket.destroy();
      }
    }
    expect(log).toHaveBeenCalledTimes(2);
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
});
