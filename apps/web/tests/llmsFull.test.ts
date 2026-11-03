import { IncomingMessage, ServerResponse } from 'node:http';
import { Socket } from 'node:net';
import { createEvent, getResponseHeader, getResponseStatus } from 'h3';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import handler, { resetLlmsFullCache } from '../server/routes/llms-full.txt.get';
import { renderLlmsFull, type LlmsFullData } from '../server/utils/llmsFull';
import { loadShedPath } from '../server/utils/loadShed';
import { routeLabel } from '../server/utils/crawlerMetrics';
import { siteMessage } from '../server/utils/siteCopy';
import { BRAND_FACTS } from '../shared/utils/brandFacts';

function eventFor(url: string, headers: Record<string, string> = { 'user-agent': 'OpenNavo-Probe/1.0' }) {
  const socket = new Socket();
  try {
    const request = new IncomingMessage(socket);
    request.url = url;
    request.headers = headers;
    return { event: createEvent(request, new ServerResponse(request)), socket };
  } catch (error) {
    socket.destroy();
    throw error;
  }
}

function fixture(): LlmsFullData {
  const apps = (tokenPrefix: string, label: string, count: number) =>
    Array.from({ length: count }, (_, index) => ({
      kind: 'cask',
      token: `${tokenPrefix}-${index}`,
      displayName: `${label} App ${index}`,
      summary:
        'A small Mac utility that edits documents, compares folders and keeps a searchable history without sending files anywhere. '.repeat(
          2
        ),
      version: `1.${index}.0`,
      installs30d: 1000 + index,
      rank30d: index + 1,
      primaryCategory: { name: 'Utilities', slug: 'utilities' }
    }));
  return {
    total: 5193,
    linkTotal: 33,
    categories: [
      {
        slug: 'developer-tools',
        name: 'Developer Tools',
        description: 'Edit code, debug applications and add extensions.',
        packageCount: 393
      }
    ],
    collections: [{ slug: 'new-mac', title: 'A better workspace', subtitle: 'Apps for everyday work', itemCount: 15 }],
    articles: [
      {
        slug: 'example-guide',
        title: 'Example guide',
        summary: 'How to choose an app.',
        publishedAt: '2026-11-03T03:14:03.060093Z',
        type: 'roundup'
      }
    ],
    apps: apps('popular', 'Popular', 100),
    generatedAt: '2026-11-03T00:00:00.000Z'
  };
}

describe('llms-full', () => {
  beforeEach(() => resetLlmsFullCache());

  it('renders catalog, categories, apps, articles and interfaces without private or query URLs', () => {
    const text = renderLlmsFull('https://opennavo.example/', 'https://api.opennavo.example/api/v1', fixture());
    expect(text.startsWith(`# ${BRAND_FACTS.name}\n`)).toBe(true);
    expect(text).toContain(`\n> ${siteMessage('en-US', BRAND_FACTS.definitionKey)}\n`);
    expect(text).toContain('License: GPL-3.0-only');
    expect(text).toContain('Site: https://opennavo.example');
    expect(text).toContain('Generated: 2026-11-03 (UTC)');
    expect(text).toContain('Source: OpenNavo catalog database + public API');
    expect(text).toContain('Short orientation file: <https://opennavo.example/llms.txt>');
    expect(text).toContain('## URL conventions');
    expect(text).toContain('5,160 Homebrew apps and 33 link-only entries');
    expect(text).toContain('## Categories');
    expect(text).toContain('[Developer Tools](https://opennavo.example/categories/developer-tools)');
    expect(text).toContain('## App index (top 100 by 30-day installs)');
    expect(text).toContain('this is not the full catalog');
    expect(text).toContain('[Popular App 0](https://opennavo.example/apps/popular-0)');
    expect(text).toContain('category Utilities; rank #1; 1,000 installs in 30 days; version 1.0.0');
    expect(text).not.toContain('## Recently updated apps');
    expect(text).toContain('[A better workspace](https://opennavo.example/collections/new-mac)');
    expect(text).toContain('[Example guide](https://opennavo.example/articles/example-guide)');
    expect(text).toContain('(roundup, 2026-11-03)');
    expect(text).toContain('Sitemap index: <https://opennavo.example/sitemap_index.xml>');
    expect(text).not.toContain('<https://opennavo.example/sitemap.xml>');
    expect(text).toContain('## Developer interfaces');
    expect(text).toContain('OpenAPI: <https://api.opennavo.com/api/v1/openapi.yaml>');
    for (const match of text.matchAll(/\]\(([^)]+)\)/g)) {
      const url = new URL(match[1]!);
      expect(url.search).toBe('');
      expect(url.hash).toBe('');
      expect(url.pathname).not.toMatch(/\/search|\/brewfile|_payload|\/admin/);
    }
    expect(text.length).toBeGreaterThanOrEqual(30_000);
    expect(text.length).toBeLessThanOrEqual(60_000);
  });

  it('serves the runtime switch with text headers, an ETag and an internally cached snapshot', async () => {
    const fetch = vi.fn(async (url: string, options: { query?: Record<string, string | number> }) => {
      const path = new URL(url).pathname;
      if (path.endsWith('/categories')) return { data: fixture().categories };
      if (path.endsWith('/collections')) return { data: { total: 1, records: fixture().collections } };
      if (path.endsWith('/articles')) return { data: { total: 1, records: fixture().articles } };
      const query = options.query ?? {};
      if (query.kind === 'cask') return { data: { total: 5_160, records: [] } };
      if (query.kind === 'link') return { data: { total: 33, records: [] } };
      if (query.sort === 'popular') return { data: { total: 5193, records: fixture().apps } };
      return { data: { total: 0, records: [] } };
    });
    vi.stubGlobal('$fetch', fetch);
    vi.stubGlobal('useRuntimeConfig', () => ({
      llmsEnabled: true,
      public: { siteUrl: 'https://opennavo.example', apiBase: 'https://api.opennavo.example/api/v1' }
    }));
    const first = eventFor('/llms-full.txt?locale=de&token=private');
    try {
      const body = await handler(first.event);
      expect(body).toContain('## App index');
      expect(getResponseStatus(first.event)).toBe(200);
      expect(getResponseHeader(first.event, 'content-type')).toBe('text/plain; charset=utf-8');
      expect(getResponseHeader(first.event, 'cache-control')).toBe('public, max-age=300, s-maxage=3600');
      expect(getResponseHeader(first.event, 'etag')).toMatch(/^W\/"[A-Za-z0-9_-]+"$/);
      expect(getResponseHeader(first.event, 'last-modified')).toBeTruthy();
      expect(getResponseHeader(first.event, 'x-robots-tag')).toBe('noindex, follow');
      expect(fetch).toHaveBeenCalledTimes(6);
      await handler(first.event);
      expect(fetch).toHaveBeenCalledTimes(6);
    } finally {
      first.socket.destroy();
      vi.unstubAllGlobals();
    }
  });

  it('returns 404 when disabled and keeps the stable sections when the API fails', async () => {
    vi.stubGlobal('$fetch', vi.fn());
    vi.stubGlobal('useRuntimeConfig', () => ({
      llmsEnabled: false,
      public: { siteUrl: 'https://opennavo.example', apiBase: 'https://api.opennavo.example/api/v1' }
    }));
    const disabled = eventFor('/llms-full.txt');
    try {
      await handler(disabled.event);
      expect(getResponseStatus(disabled.event)).toBe(404);
      expect(getResponseHeader(disabled.event, 'cache-control')).toBe('no-store');
    } finally {
      disabled.socket.destroy();
      vi.unstubAllGlobals();
    }

    resetLlmsFullCache();
    vi.stubGlobal(
      '$fetch',
      vi.fn(async () => {
        throw new Error('catalog unavailable');
      })
    );
    vi.stubGlobal('useRuntimeConfig', () => ({
      llmsEnabled: 'true',
      public: { siteUrl: 'https://opennavo.example', apiBase: 'https://api.opennavo.example/api/v1' }
    }));
    const fallback = eventFor('/llms-full.txt');
    try {
      const body = await handler(fallback.event);
      expect(getResponseStatus(fallback.event)).toBe(200);
      expect(body).toContain('## Key pages');
      expect(body).toContain('## Developer interfaces');
      expect(body).toContain('Catalog data is temporarily unavailable');
    } finally {
      fallback.socket.destroy();
      vi.unstubAllGlobals();
    }
  });

  it('stays out of the search paths and the sitemap, and is exempt from load shedding', async () => {
    expect(routeLabel('/llms-full.txt?locale=de')).toBe('llms');
    expect(loadShedPath('/llms-full.txt')).toBe(false);
    vi.stubGlobal('defineNuxtConfig', (config: unknown) => config);
    try {
      const { default: config } = await import('../nuxt.config');
      expect(config.routeRules?.['/llms-full.txt']).toEqual({ cache: false });
      expect(config.sitemap?.exclude).toContain('/llms-full.txt');
    } finally {
      vi.unstubAllGlobals();
    }
  });
});
