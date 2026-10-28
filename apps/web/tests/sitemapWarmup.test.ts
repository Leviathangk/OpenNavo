import { IncomingMessage, ServerResponse } from 'node:http';
import { Socket } from 'node:net';
import { createEvent } from 'h3';
import { LOAD_SHED_DEFAULTS, LoadShedState, shedRequest } from '../server/utils/loadShed';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { SITEMAP_WARMUP_PATHS, warmSitemaps } from '../server/utils/sitemapWarmup';

beforeEach(() => {
  vi.stubGlobal('getSiteSettings', async () => ({ pages: { fontsSitemap: true } }));
  vi.stubEnv('OPENNAVO_SITEMAP_WARMUP', 'true');
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => ({ status: 200 }))
  );
  vi.spyOn(console, 'info').mockImplementation(() => {});
  vi.stubGlobal('useRuntimeConfig', () => ({
    loadShed: LOAD_SHED_DEFAULTS,
    apiBaseInternal: 'http://api.example.test/api/v1'
  }));
});

afterEach(() => {
  vi.unstubAllEnvs();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  vi.resetModules();
});

describe('sitemap startup warmup', () => {
  it('loads the font setting only after API readiness', async () => {
    vi.useFakeTimers();
    let ready = false;
    const settings = vi.fn(async () => {
      expect(ready).toBe(true);
      return { pages: { fontsSitemap: true } };
    });
    vi.stubGlobal('getSiteSettings', settings);
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => {
        ready = true;
        return { status: 200 };
      })
    );
    const localFetch = vi.fn(async () => ({ status: 200 }));
    vi.stubGlobal('defineNitroPlugin', (plugin: (app: unknown) => void) =>
      plugin({ localFetch, hooks: { hook: vi.fn() } })
    );
    await import('../server/plugins/sitemap-warmup');
    await vi.runAllTimersAsync();
    expect(settings).toHaveBeenCalledOnce();
    expect(localFetch).toHaveBeenCalledTimes(25);
  });

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

  it('skips all font shards when the site setting is disabled', async () => {
    vi.stubGlobal('getSiteSettings', async () => ({ pages: { fontsSitemap: false } }));
    vi.useFakeTimers();
    vi.stubGlobal('useRuntimeConfig', () => ({
      loadShed: LOAD_SHED_DEFAULTS,
      apiBaseInternal: 'http://api.example.test/api/v1'
    }));
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
    expect(globalThis.fetch).not.toHaveBeenCalled();
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
    vi.useFakeTimers();
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const fetch = vi.fn(async () => ({ status: 200 }));
    fetch
      .mockRejectedValueOnce(new Error('private connection details'))
      .mockResolvedValueOnce({ status: 503 })
      .mockResolvedValueOnce({ status: 503 });
    const pending = warmSitemaps(fetch);
    await vi.runAllTimersAsync();
    await pending;
    expect(fetch).toHaveBeenCalledTimes(27);
    expect(warn).toHaveBeenCalledTimes(1);
    expect(console.info).toHaveBeenCalledWith(expect.stringContaining('success=24 failed=1'));
    expect(JSON.stringify(warn.mock.calls)).not.toContain('private connection details');
  });

  it('waits for API recovery before rendering any XML and retries a transient shard', async () => {
    vi.useFakeTimers();
    const ready = vi
      .fn()
      .mockRejectedValueOnce(new Error('ECONNREFUSED private'))
      .mockResolvedValueOnce(false)
      .mockResolvedValue(true);
    const fetch = vi.fn().mockResolvedValueOnce({ status: 503 }).mockResolvedValue({ status: 200 });
    const pending = warmSitemaps(fetch, undefined, undefined, true, { ready });
    await vi.advanceTimersByTimeAsync(29_999);
    expect(fetch).not.toHaveBeenCalled();
    await vi.runAllTimersAsync();
    await pending;
    expect(ready).toHaveBeenCalledTimes(3);
    expect(fetch).toHaveBeenCalledTimes(26);
    expect(fetch.mock.calls[0]![0]).toBe(fetch.mock.calls[1]![0]);
    expect(console.info).toHaveBeenCalledWith(expect.stringContaining('success=25 failed=0'));
  });

  it('abandons readiness after ten minutes, including a probe that never settles', async () => {
    vi.useFakeTimers();
    const ready = vi.fn(() => new Promise<boolean>(() => {}));
    const fetch = vi.fn();
    const pending = warmSitemaps(fetch, undefined, undefined, false, { ready });
    await vi.advanceTimersByTimeAsync(600_000);
    await pending;
    expect(fetch).not.toHaveBeenCalled();
    expect(console.info).toHaveBeenCalledWith(
      expect.stringContaining('failed=17 durationMs=600000 outcome=readiness_timeout')
    );
  });

  it.each(['probe', 'backoff', 'render'])(
    'immediately cancels during %s without waiting for timers or transport',
    async phase => {
      vi.useFakeTimers();
      const controller = new AbortController();
      const ready = phase === 'probe' ? () => new Promise<boolean>(() => {}) : async () => phase === 'render';
      const fetch = vi.fn(() => new Promise<{ status: number }>(() => {}));
      const pending = warmSitemaps(fetch, undefined, undefined, true, { ready, signal: controller.signal });
      await vi.advanceTimersByTimeAsync(1);
      controller.abort();
      await pending;
      expect(vi.getTimerCount()).toBe(0);
      expect(console.info).toHaveBeenCalledWith(expect.stringContaining('outcome=cancelled'));
      expect(fetch).toHaveBeenCalledTimes(phase === 'render' ? 1 : 0);
    }
  );

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
