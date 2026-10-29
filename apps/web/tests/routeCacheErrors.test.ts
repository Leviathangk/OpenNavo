import { afterEach, describe, expect, it, vi } from 'vitest';
import type { H3Event } from 'h3';
import { routeCacheKey } from '../server/utils/pageCache';

afterEach(() => {
  vi.unstubAllGlobals();
  vi.resetModules();
});

// What Nitro's cached route handler leaves on the event (nitropack defineCachedEventHandler).
const cachedRouteEvent = (key = 'apps.Abc123') =>
  ({
    context: { cache: { options: { group: 'nitro/routes', swr: true, getKey: async () => key } } }
  }) as unknown as H3Event;

describe('Route cache entries of error pages', () => {
  it('marks thrown OG and overload errors no-store and leaves already-sent SWR responses alone', async () => {
    const hooks: Record<string, (...args: unknown[]) => unknown> = {};
    vi.stubGlobal('defineNitroPlugin', (plugin: (app: unknown) => void) =>
      plugin({ hooks: { hook: (name: string, handler: (...args: unknown[]) => unknown) => (hooks[name] = handler) } })
    );
    await import('../server/plugins/error-cache-control');
    const headers = new Map<string, unknown>([['retry-after', '120']]);
    const response = {
      headersSent: false,
      setHeader: (name: string, value: unknown) => {
        if (response.headersSent) throw new Error('headers already sent');
        headers.set(name.toLowerCase(), value);
      },
      getHeader: (name: string) => headers.get(name.toLowerCase())
    };
    const event = { path: '/_og/image.png', node: { res: response } } as unknown as H3Event;
    for (const statusCode of [403, 429, 503]) {
      hooks.error!({ statusCode }, { event });
      for (const name of ['cache-control', 'cdn-cache-control', 'cloudflare-cdn-cache-control'])
        expect(headers.get(name)).toBe('no-store');
    }
    expect(headers.get('retry-after')).toBe('120');
    response.headersSent = true;
    expect(() => hooks.error!({ statusCode: 404 }, { event, tags: ['cache'] })).not.toThrow();
  });

  it('finds the entry a cached route stores its response under', async () => {
    expect(await routeCacheKey(cachedRouteEvent())).toBe('nitro:routes:_:apps.Abc123.json');
    expect(await routeCacheKey({ context: {} } as unknown as H3Event)).toBeUndefined();
    const other = { context: { cache: { options: { group: 'sitemap', getKey: () => 'x' } } } } as unknown as H3Event;
    expect(await routeCacheKey(other)).toBeUndefined();
  });

  it('drops the cached page when it now answers 404 or 410, never on outages', async () => {
    const hooks: Record<string, (...args: unknown[]) => Promise<void>> = {};
    const removeItem = vi.fn(async () => undefined);
    const storage = vi.fn(() => ({ removeItem }));
    vi.stubGlobal('useStorage', storage);
    vi.stubGlobal('defineNitroPlugin', (plugin: (app: unknown) => void) =>
      plugin({
        hooks: { hook: (name: string, handler: (...args: unknown[]) => Promise<void>) => (hooks[name] = handler) }
      })
    );
    await import('../server/plugins/route-cache-errors');
    const event = cachedRouteEvent();
    // A list page past its end throws during the (background) render.
    await hooks.error!({ statusCode: 404 }, { event, tags: ['cache'] });
    expect(storage).toHaveBeenCalledWith('cache');
    expect(removeItem).toHaveBeenCalledWith('nitro:routes:_:apps.Abc123.json');
    // Removed apps and withdrawn articles render a 410 page.
    await hooks['render:response']!({ statusCode: 410, body: '' }, { event });
    expect(removeItem).toHaveBeenCalledTimes(2);
    await hooks.error!({ statusCode: 503 }, { event });
    await hooks['render:response']!({ statusCode: 200, body: '' }, { event });
    await hooks.error!(new Error('no status'), { event });
    await hooks.error!({ statusCode: 404 }, {});
    await hooks.error!({ statusCode: 404 }, { event: { context: {} } });
    expect(removeItem).toHaveBeenCalledTimes(2);
  });
});
