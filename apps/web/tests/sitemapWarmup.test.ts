import { IncomingMessage, ServerResponse } from 'node:http';
import { Socket } from 'node:net';
import { createEvent } from 'h3';
import { LOAD_SHED_DEFAULTS, LoadShedState, shedRequest } from '../server/utils/loadShed';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { SITEMAP_WARMUP_PATHS, warmSitemaps } from '../server/utils/sitemapWarmup';

beforeEach(() => {
  vi.stubEnv('OPENNAVO_SITEMAP_WARMUP', 'true');
  vi.stubGlobal('useRuntimeConfig', () => ({ loadShed: LOAD_SHED_DEFAULTS, sitemapFonts: true }));
});

afterEach(() => {
  vi.unstubAllEnvs();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  vi.resetModules();
});

describe('sitemap startup warmup', () => {
  it('defers work until plugin registration finishes and warms index plus all 24 children', async () => {
    vi.useFakeTimers();
    const localFetch = vi.fn(async () => ({ status: 200 }));
    const hook = vi.fn();
    vi.stubGlobal('defineNitroPlugin', (plugin: (app: unknown) => void) => plugin({ localFetch, hooks: { hook } }));
    await import('../server/plugins/sitemap-warmup');
    expect(localFetch).not.toHaveBeenCalled();
    await vi.runAllTimersAsync();
    expect(localFetch.mock.calls.map(call => (call as unknown[])[0])).toEqual(SITEMAP_WARMUP_PATHS);
    expect(localFetch).toHaveBeenCalledTimes(25);
    expect(hook).toHaveBeenCalledWith('close', expect.any(Function));
  });

  it('skips all font shards when the font experiment is disabled', async () => {
    vi.useFakeTimers();
    vi.stubGlobal('useRuntimeConfig', () => ({ loadShed: LOAD_SHED_DEFAULTS, sitemapFonts: false }));
    const localFetch = vi.fn(async (_path: string) => ({ status: 200 }));
    vi.stubGlobal('defineNitroPlugin', (plugin: (app: unknown) => void) =>
      plugin({ localFetch, hooks: { hook: vi.fn() } })
    );
    await import('../server/plugins/sitemap-warmup');
    await vi.runAllTimersAsync();
    expect(localFetch.mock.calls.map(([path]) => path)).toEqual(
      SITEMAP_WARMUP_PATHS.filter(path => !path.includes('/fonts-bucket-'))
    );
    expect(localFetch).toHaveBeenCalledTimes(17);
  });

  it('makes no requests and schedules no work when explicitly disabled', async () => {
    vi.useFakeTimers();
    vi.stubEnv('OPENNAVO_SITEMAP_WARMUP', 'false');
    const localFetch = vi.fn();
    const hook = vi.fn();
    vi.stubGlobal('defineNitroPlugin', (plugin: (app: unknown) => void) => plugin({ localFetch, hooks: { hook } }));
    await import('../server/plugins/sitemap-warmup');
    await vi.runAllTimersAsync();
    expect(localFetch).not.toHaveBeenCalled();
    expect(hook).not.toHaveBeenCalled();
  });

  it('defaults to enabled in production when the environment override is absent', async () => {
    vi.useFakeTimers();
    vi.stubEnv('OPENNAVO_SITEMAP_WARMUP', undefined);
    const localFetch = vi.fn(async () => ({ status: 200 }));
    vi.stubGlobal('defineNitroPlugin', (plugin: (app: unknown) => void) =>
      plugin({ localFetch, hooks: { hook: vi.fn() } })
    );
    await import('../server/plugins/sitemap-warmup');
    await vi.runAllTimersAsync();
    expect(localFetch).toHaveBeenCalledTimes(25);
  });

  it('yields before rendering and pauses between children while foreground capacity is busy', async () => {
    vi.useFakeTimers();
    let available = false;
    let stopped = false;
    const fetch = vi.fn(async () => {
      available = false;
      return { status: 200 };
    });
    const warming = warmSitemaps(
      fetch,
      () => stopped,
      () => available
    );
    expect(fetch).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(500);
    expect(fetch).not.toHaveBeenCalled();
    available = true;
    await vi.advanceTimersByTimeAsync(100);
    expect(fetch).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(500);
    expect(fetch).toHaveBeenCalledTimes(1);
    stopped = true;
    await vi.runAllTimersAsync();
    await warming;
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it('leaves page, OG and crawler capacity untouched for every warmup route, even under overload', async () => {
    const state = new LoadShedState();
    vi.spyOn(state, 'start').mockImplementation(() => {});
    const track = vi.spyOn(state, 'track');
    const spend = vi.spyOn(state, 'spend');
    const sockets: Socket[] = [];
    const event = (path: string) => {
      const socket = new Socket();
      sockets.push(socket);
      const req = new IncomingMessage(socket);
      req.url = path;
      req.headers = { 'user-agent': 'GPTBot' };
      return createEvent(req, new ServerResponse(req));
    };
    try {
      for (const pressure of [0, 1000]) {
        state.lagMs = pressure;
        await warmSitemaps(async path => {
          const request = event(path);
          expect(shedRequest(request, LOAD_SHED_DEFAULTS, state)).toBeUndefined();
          expect(request.context.loadShed).toBeUndefined();
          expect(state.inFlight).toBe(0);
          expect(state.imageInFlight).toBe(0);
          return { status: 200 };
        });
      }
      expect(track).not.toHaveBeenCalled();
      expect(spend).not.toHaveBeenCalled();
      state.lagMs = 0;
      // Even a one-request limit remains entirely available to real foreground work.
      const config = { ...LOAD_SHED_DEFAULTS, imageInFlight: 1, lowInFlight: 1, mediumInFlight: 1, extremeInFlight: 1 };
      for (const path of ['/_og/image.png', '/apps/example']) {
        const request = event(path);
        expect(shedRequest(request, config, state)).toBeUndefined();
        expect(request.context.loadShed).toBeUndefined();
        expect(state.inFlight).toBe(1);
        request.context.releaseLoadShed();
      }
    } finally {
      state.stop();
      for (const socket of sockets) socket.destroy();
    }
  });

  it('cancels scheduled work when the server closes before warmup starts', async () => {
    vi.useFakeTimers();
    const localFetch = vi.fn();
    const hook = vi.fn();
    vi.stubGlobal('defineNitroPlugin', (plugin: (app: unknown) => void) => plugin({ localFetch, hooks: { hook } }));
    await import('../server/plugins/sitemap-warmup');
    hook.mock.calls[0]![1]();
    await vi.runAllTimersAsync();
    expect(localFetch).not.toHaveBeenCalled();
  });

  it('continues past rejected requests and non-200 responses without logging error details', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const fetch = vi.fn(async () => ({ status: 200 }));
    fetch.mockRejectedValueOnce(new Error('private connection details')).mockResolvedValueOnce({ status: 503 });
    await expect(warmSitemaps(fetch)).resolves.toBeUndefined();
    expect(fetch).toHaveBeenCalledTimes(25);
    expect(warn).toHaveBeenCalledTimes(2);
    expect(JSON.stringify(warn.mock.calls)).not.toContain('private connection details');
  });

  it('stops scheduling further children on shutdown', async () => {
    let stopped = false;
    const fetch = vi.fn(async () => {
      stopped = true;
      return { status: 200 };
    });
    await warmSitemaps(fetch, () => stopped);
    expect(fetch).toHaveBeenCalledTimes(1);
  });
});
