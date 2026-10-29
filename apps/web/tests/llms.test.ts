import { readFileSync } from 'node:fs';
import { IncomingMessage, ServerResponse } from 'node:http';
import { Socket } from 'node:net';
import { createEvent, getResponseHeader, getResponseStatus } from 'h3';
import { describe, expect, it, vi } from 'vitest';
import handler from '../server/routes/llms.txt.get';
import { captureCrawler, countResponse, RequestCounter, routeLabel } from '../server/utils/crawlerMetrics';
import { renderLlms } from '../server/utils/llms';
import { siteMessage } from '../server/utils/siteCopy';
import { BRAND_FACTS } from '../shared/utils/brandFacts';
import { cacheQuery, canonicalQuery } from '../shared/utils/routeQuery';

describe('G6 short English llms', () => {
  it('uses the shared definition and license with only stable canonical HTML links', () => {
    const origin = 'https://opennavo.example';
    const text = renderLlms(`${origin}/`);
    expect(text.startsWith(`# ${BRAND_FACTS.name}\n`)).toBe(true);
    expect(text).toContain(`\n> ${siteMessage('en-US', BRAND_FACTS.definitionKey)}\n`);
    expect(text).toContain('\n## Main pages\n');
    expect(text).toContain(BRAND_FACTS.license);
    expect(text.length).toBeLessThan(1800);
    const urls = [...text.matchAll(/\]\(([^)]+)\)/g)].map(match => new URL(match[1]!));
    expect(urls.map(url => url.pathname)).toEqual(['/', '/download', '/apps', '/security', '/about']);
    for (const url of urls) {
      expect(url.origin).toBe(origin);
      expect(url.search).toBe('');
      expect(url.hash).toBe('');
      expect(url.pathname).not.toMatch(/\.md|admin|candidate|\/zh|\/de/);
    }
  });

  it.each([true, 'true', false, 'false', undefined, '1', 'TRUE'])(
    'serves the runtime switch %s with text headers, no cache and final-status metrics',
    async enabled => {
      vi.stubGlobal('useRuntimeConfig', () => ({
        llmsEnabled: enabled,
        public: { siteUrl: 'https://opennavo.example' }
      }));
      const socket = new Socket();
      try {
        const request = new IncomingMessage(socket);
        request.url = '/llms.txt?locale=de&token=private';
        request.headers = { 'user-agent': 'OpenNavo-Probe/1.0' };
        const event = createEvent(request, new ServerResponse(request));
        captureCrawler(event, { trustedProxies: '' });
        const body = await handler(event);
        const isEnabled = enabled === true || enabled === 'true';
        const status = isEnabled ? 200 : 404;
        expect(getResponseStatus(event)).toBe(status);
        expect(getResponseHeader(event, 'content-type')).toBe('text/plain; charset=utf-8');
        expect(getResponseHeader(event, 'cache-control')).toBe('no-store');
        expect(getResponseHeader(event, 'x-robots-tag')).toBe('noindex, follow');
        if (isEnabled) expect(body).toBe(renderLlms('https://opennavo.example'));
        else expect(body).not.toContain(siteMessage('en-US', BRAND_FACTS.definitionKey));
        const counter = new RequestCounter();
        countResponse(counter, event);
        expect(counter.render()).toContain(`agent="self_test",route="llms",status="${status}"`);
        expect(counter.render()).not.toMatch(/private|locale|token=/);
      } finally {
        socket.destroy();
        vi.unstubAllGlobals();
      }
    }
  );

  it('keeps query parameters out of content selection and uses a fixed metric label', () => {
    expect(routeLabel('/llms.txt?anything=1')).toBe('llms');
    expect(routeLabel('/apps/llms.txt')).not.toBe('llms');
    expect(cacheQuery('/llms.txt', 'locale=de&token=private')).toBe('');
    expect(canonicalQuery('/llms.txt', new URLSearchParams('page=2&locale=de'))).toBe('');
  });

  it('defaults on independently of Markdown, stays outside sitemaps and adds no discovery link', async () => {
    vi.stubGlobal('defineNuxtConfig', (config: unknown) => config);
    try {
      const { default: config } = await import('../nuxt.config');
      expect(config.runtimeConfig?.llmsEnabled).toBe(true);
      expect(config.runtimeConfig?.public?.markdownAlternates).toBe(true);
      expect(config.routeRules?.['/llms.txt']).toEqual({ cache: false, headers: { 'cache-control': 'no-store' } });
      expect(config.sitemap?.exclude).toContain('/llms.txt');
      // No discovery header is emitted when disabled, and no robots entry can dangle.
      const robots = readFileSync(new URL('../server/routes/robots.txt.ts', import.meta.url), 'utf8');
      expect(robots).not.toContain('llms');
      const route = readFileSync(new URL('../server/routes/llms.txt.get.ts', import.meta.url), 'utf8');
      expect(route).not.toMatch(/IndexNow|indexnow|announce|fetch\(/);
    } finally {
      vi.unstubAllGlobals();
    }
  });
});
