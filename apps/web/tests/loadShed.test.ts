import { IncomingMessage, ServerResponse, createServer } from 'node:http';
import { Socket } from 'node:net';
import { once } from 'node:events';
import { createApp, createError, createEvent, defineEventHandler, setHeader, toNodeListener, type H3Event } from 'h3';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { agentLabel, countResponse, RequestCounter } from '../server/utils/crawlerMetrics';
import {
  LOAD_SHED_DEFAULTS,
  LoadShedState,
  loadShedConfig,
  loadShedLevel,
  loadShedPath,
  loadShedState,
  loadShedTier,
  shedRequest,
  shouldShed,
  type LoadShedTier
} from '../server/utils/loadShed';

afterEach(() => vi.unstubAllGlobals());

const tiers: Record<LoadShedTier, (string | undefined)[]> = {
  low: [
    'GPTBot',
    'ClaudeBot',
    'CCBot',
    'Bytespider',
    'Meta-ExternalAgent',
    'MistralAI-Training',
    'Amazonbot',
    'AhrefsBot',
    'ExampleBot',
    undefined
  ],
  medium: ['PerplexityBot', 'OAI-SearchBot', 'Claude-SearchBot', 'Meta-WebIndexer', 'MistralAI-Index', 'GoogleOther'],
  protected: [
    'Mozilla/5.0 Chrome/145',
    'Googlebot',
    'bingbot',
    'BingPreview',
    'Applebot',
    'DuckDuckBot',
    'YandexBot',
    'Baiduspider',
    'ChatGPT-User',
    'Claude-User',
    'Perplexity-User',
    'MistralAI-User',
    'Meta-ExternalFetcher',
    'DuckAssistBot',
    'facebookexternalhit',
    'Twitterbot',
    'Slackbot-LinkExpanding',
    'LinkedInBot',
    'Discordbot',
    'TelegramBot',
    'WhatsApp',
    'OpenNavo-Probe',
    'OpenNavo-Test'
  ]
};

describe('priority load shedding', () => {
  it('exhaustively protects browsers, search engines, user-triggered AI fetchers and previews until extreme load', () => {
    for (const [tier, uas] of Object.entries(tiers)) {
      for (const ua of uas) {
        expect(loadShedTier(agentLabel(ua)), ua).toBe(tier);
        expect(shouldShed(loadShedTier(agentLabel(ua)), 'normal')).toBe(false);
        expect(shouldShed(loadShedTier(agentLabel(ua)), 'mild')).toBe(tier === 'low');
        expect(shouldShed(loadShedTier(agentLabel(ua)), 'heavy')).toBe(tier !== 'protected');
        expect(shouldShed(loadShedTier(agentLabel(ua)), 'extreme')).toBe(true);
      }
    }
  });

  it('sheds on either concurrent work or recent event-loop delay, at each configured boundary', () => {
    for (const [inFlight, lagMs, level] of [
      [7, 49, 'normal'],
      [8, 0, 'mild'],
      [0, 50, 'mild'],
      [16, 0, 'heavy'],
      [0, 200, 'heavy'],
      [64, 0, 'extreme'],
      [0, 1000, 'extreme']
    ] as const)
      expect(loadShedLevel(inFlight, lagMs, LOAD_SHED_DEFAULTS)).toBe(level);
  });

  it('defaults on, supports optional environment values and keeps thresholds ordered', async () => {
    expect(loadShedConfig()).toEqual(LOAD_SHED_DEFAULTS);
    expect(loadShedConfig({ enabled: 'false' }).enabled).toBe(false);
    expect(loadShedConfig({ enabled: false }).enabled).toBe(false);
    expect(loadShedConfig({ lowInFlight: '12', mediumInFlight: '3', extremeInFlight: 1, lowLagMs: NaN })).toMatchObject(
      { lowInFlight: 12, mediumInFlight: 12, extremeInFlight: 12, lowLagMs: 50 }
    );
    expect(loadShedConfig({ lowInFlight: -1, mediumLagMs: 'invalid' })).toEqual(LOAD_SHED_DEFAULTS);
    vi.stubGlobal('defineNuxtConfig', (value: unknown) => value);
    const { default: config } = await import('../nuxt.config');
    expect(config.runtimeConfig?.loadShed).toEqual(LOAD_SHED_DEFAULTS);
  });

  it('always exempts discovery, health, APIs and static assets, including HTML accept requests', () => {
    for (const path of [
      '/_nuxt/abc.js',
      '/_nuxt/test/_payload.json',
      '/robots.txt',
      '/llms.txt',
      '/sitemap.xml',
      '/sitemap_index.xml',
      '/sitemap-apps.xml.gz',
      '/__sitemap__/zh-apps-bucket-0.xml',
      '/_sitemap/index.xml',
      '/health',
      '/healthz',
      '/readyz',
      '/livez',
      '/metrics',
      '/api/consent-region',
      '/fonts/og.woff',
      '/icon-512.png'
    ])
      expect(loadShedPath(`${path}?anything=1`), path).toBe(false);
    for (const path of [
      '/',
      '/zh/apps/vlc',
      '/apps/example.app',
      '/background-items/com.example.agent',
      '/apps/vlc/_payload.json',
      '/__nuxt_island/Component_hash.json',
      '/_og/image.png',
      '/articles/feed.xml',
      '/pt/apps/vlc/feed.xml',
      '/missing-page',
      '/missing.html'
    ])
      expect(loadShedPath(path), path).toBe(true);
  });

  it('reclaims admitted requests exactly once on finish, errors, aborts, close and localFetch completion', () => {
    for (const signal of ['finish', 'close', 'error', 'aborted', 'request-error', 'afterResponse', 'already-aborted']) {
      const state = new LoadShedState();
      const socket = new Socket();
      const req = new IncomingMessage(socket);
      const res = new ServerResponse(req);
      const event = createEvent(req, res);
      if (signal === 'already-aborted') req.aborted = true;
      state.track(event);
      if (signal === 'aborted') req.emit('aborted');
      else if (signal === 'request-error') req.emit('error', new Error('request failed'));
      else if (signal === 'afterResponse') event.context.releaseLoadShed();
      else if (signal !== 'already-aborted') res.emit(signal, new Error('response failed'));
      expect(state.inFlight, signal).toBe(0);
      res.emit('close');
      event.context.releaseLoadShed();
      expect(state.inFlight).toBe(0);
      for (const name of ['finish', 'close', 'error']) expect(res.listenerCount(name)).toBe(0);
      expect(req.listenerCount('aborted')).toBe(0);
      expect(req.listenerCount('error')).toBe(0);
      socket.destroy();
    }
  });

  it('captures trusted crawler verification even when the earliest middleware rejects', async () => {
    vi.stubGlobal('useRuntimeConfig', () => ({
      loadShed: LOAD_SHED_DEFAULTS,
      trustedProxies: '192.0.2.10',
      cfVerifiedBotEnabled: true,
      verifiedBotHeader: 'X-OpenNavo-Verified-Bot'
    }));
    const socket = new Socket();
    Object.defineProperty(socket, 'remoteAddress', { value: '192.0.2.10' });
    const request = new IncomingMessage(socket);
    request.url = '/apps/vlc';
    request.headers = { 'user-agent': 'Googlebot' };
    request.rawHeaders = ['X-OpenNavo-Verified-Bot', 'true'];
    const event = createEvent(request, new ServerResponse(request));
    loadShedState.lagMs = 1000;
    try {
      const { default: middleware } = await import('../server/middleware/00-load-shed');
      await middleware(event);
      expect(event.context.crawlerIdentity).toMatchObject({ agent: 'googlebot', verification: 'verified' });
      const counter = new RequestCounter();
      countResponse(counter, event);
      expect(counter.render()).toContain('status="503",verification="verified",verification_basis="cf_client_bot"} 1');
    } finally {
      loadShedState.stop();
      socket.destroy();
    }
  });

  it('sends uncached 503 immediately through real H3, retains retry jitter and counts island/OG rejections', async () => {
    const state = new LoadShedState();
    const counter = new RequestCounter();
    const beforeHooks: ((event: H3Event) => void)[] = [];
    vi.stubGlobal('setHeader', setHeader);
    vi.stubGlobal('defineNitroPlugin', (plugin: (nitro: unknown) => void) =>
      plugin({ hooks: { hook: (_name: string, handler: (event: H3Event) => void) => beforeHooks.push(handler) } })
    );
    // The existing error plugin must not shorten the overload Retry-After to five seconds.
    await import('../server/plugins/error-cache-control');
    const config = { ...LOAD_SHED_DEFAULTS };
    const downstream = vi.fn(() => 'page');
    const app = createApp({
      onBeforeResponse: event => {
        for (const hook of beforeHooks) hook(event);
      },
      onAfterResponse: event => {
        event.context.releaseLoadShed?.();
        countResponse(counter, event);
      }
    });
    app.use(defineEventHandler(event => shedRequest(event, config, state)));
    app.use(defineEventHandler(downstream));
    const server = createServer(toNodeListener(app));
    server.listen(0, '127.0.0.1');
    await once(server, 'listening');
    const address = server.address() as { port: number };
    const request = (path: string, ua = 'GPTBot', extra = {}) =>
      fetch(`http://127.0.0.1:${address.port}${path}`, { headers: { 'user-agent': ua, ...extra } });
    try {
      state.inFlight = 8;
      for (const path of ['/apps/vlc?private=no-label', '/__nuxt_island/test.json', '/_og/image.png']) {
        const response = await request(path, 'GPTBot', { 'x-nuxt-og-image': '1' });
        expect(response.status).toBe(503);
        expect(Number(response.headers.get('retry-after'))).toBeGreaterThanOrEqual(60);
        expect(Number(response.headers.get('retry-after'))).toBeLessThanOrEqual(180);
        for (const header of ['cache-control', 'cdn-cache-control', 'cloudflare-cdn-cache-control'])
          expect(response.headers.get(header)).toBe('no-store');
        expect(response.headers.get('content-type')).toBe('text/plain; charset=utf-8');
        expect(response.headers.get('x-robots-tag')).toBeNull();
        expect(await response.text()).toContain('temporarily unavailable');
      }
      expect(downstream).not.toHaveBeenCalled();
      const rejected = [...counter.render().matchAll(/status="503"[^\n]+\} (\d+)/g)];
      expect(rejected.reduce((total, match) => total + Number(match[1]), 0)).toBe(3);
      expect(state.render()).toContain('agent="gptbot",tier="low",level="mild"} 3');
      expect(state.render()).toContain('opennavo_web_in_flight 8');
      expect(state.render() + counter.render()).not.toMatch(/private|vlc|image\.png/);
      expect((await request('/apps/vlc', 'Googlebot')).status).toBe(200);
      expect((await request('/apps/vlc', 'PerplexityBot')).status).toBe(200);
      state.inFlight = 16;
      expect((await request('/apps/vlc', 'PerplexityBot')).status).toBe(503);
      expect((await request('/apps/vlc', 'ChatGPT-User')).status).toBe(200);
      state.inFlight = 64;
      expect((await request('/apps/vlc', 'Googlebot')).status).toBe(503);
      for (const path of ['/robots.txt', '/sitemap_index.xml', '/_nuxt/page.js'])
        expect((await request(path)).status).toBe(200);
      config.enabled = false;
      expect((await request('/apps/vlc')).status).toBe(200);
    } finally {
      state.stop();
      await new Promise<void>(resolve => server.close(() => resolve()));
    }
  });

  it('releases actual HTTP handler exceptions and disconnected clients', async () => {
    const state = new LoadShedState();
    let entered!: () => void;
    let finish!: () => void;
    let closed!: () => void;
    const started = new Promise<void>(resolve => {
      entered = resolve;
    });
    const held = new Promise<void>(resolve => {
      finish = resolve;
    });
    const disconnected = new Promise<void>(resolve => {
      closed = resolve;
    });
    const app = createApp({
      onAfterResponse: event => {
        event.context.releaseLoadShed?.();
      }
    });
    app.use(defineEventHandler(event => shedRequest(event, LOAD_SHED_DEFAULTS, state)));
    app.use(
      defineEventHandler(async event => {
        if (event.path === '/error') throw createError({ message: 'test failure', statusCode: 500 });
        event.node.res.once('close', closed);
        entered();
        await held;
        return 'done';
      })
    );
    const server = createServer(toNodeListener(app));
    server.listen(0, '127.0.0.1');
    await once(server, 'listening');
    const origin = `http://127.0.0.1:${(server.address() as { port: number }).port}`;
    try {
      expect((await fetch(`${origin}/error`)).status).toBe(500);
      expect(state.inFlight).toBe(0);
      const controller = new AbortController();
      const pending = fetch(`${origin}/held`, { signal: controller.signal }).catch(() => undefined);
      await started;
      expect(state.inFlight).toBe(1);
      controller.abort();
      await disconnected;
      expect(state.inFlight).toBe(0);
      finish();
      await pending;
    } finally {
      finish();
      state.stop();
      server.closeAllConnections();
      await new Promise<void>(resolve => server.close(() => resolve()));
    }
  });
});
