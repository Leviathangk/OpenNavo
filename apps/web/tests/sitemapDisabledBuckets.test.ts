import { IncomingMessage, ServerResponse } from 'node:http';
import { Socket } from 'node:net';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createEvent, getResponseHeader } from 'h3';

type RuntimeState = {
  fonts: boolean;
  versions: boolean;
};

const { getSiteSettingsMock } = vi.hoisted(() => ({ getSiteSettingsMock: vi.fn() }));
vi.mock('../server/utils/siteSettings', () => ({ getSiteSettings: getSiteSettingsMock }));

const state: RuntimeState = { fonts: false, versions: false };
const sockets: Socket[] = [];

beforeEach(() => {
  state.fonts = false;
  state.versions = false;
  vi.stubGlobal('useRuntimeConfig', () => ({ sitemapFonts: state.fonts }));
  getSiteSettingsMock.mockImplementation(async () => ({ pages: { versionsSitemap: state.versions } }));
});

afterEach(() => {
  for (const socket of sockets.splice(0)) socket.destroy();
  getSiteSettingsMock.mockReset();
  vi.unstubAllGlobals();
  vi.resetModules();
});

function request(path: string) {
  const socket = new Socket();
  sockets.push(socket);
  const incoming = new IncomingMessage(socket);
  incoming.url = path;
  return createEvent(incoming, new ServerResponse(incoming));
}

describe('disabled sitemap buckets', () => {
  it.each(['/__sitemap__/fonts-bucket-1.xml', '/__sitemap__/versions-bucket-1.xml'])(
    'returns 410 with uncacheable headers while the source is disabled: %s',
    async path => {
      const { default: middleware } = await import('../server/middleware/sitemap-fonts');
      const event = request(path);

      await expect(middleware(event)).rejects.toMatchObject({ statusCode: 410, statusMessage: 'Sitemap gone' });
      for (const header of ['cache-control', 'cdn-cache-control', 'cloudflare-cdn-cache-control'])
        expect(getResponseHeader(event, header), header).toBe('no-store');
    }
  );

  it('follows runtime switches without retaining a disabled response', async () => {
    const { default: middleware } = await import('../server/middleware/sitemap-fonts');
    const fonts = request('/__sitemap__/fonts-bucket-1.xml');
    const versions = request('/__sitemap__/versions-bucket-1.xml');

    state.fonts = true;
    state.versions = true;
    await expect(middleware(fonts)).resolves.toBeUndefined();
    await expect(middleware(versions)).resolves.toBeUndefined();

    state.fonts = false;
    state.versions = false;
    await expect(middleware(request('/__sitemap__/fonts-bucket-1.xml'))).rejects.toMatchObject({ statusCode: 410 });
    await expect(middleware(request('/__sitemap__/versions-bucket-1.xml'))).rejects.toMatchObject({ statusCode: 410 });

    state.fonts = true;
    state.versions = true;
    await expect(middleware(request('/__sitemap__/fonts-bucket-1.xml'))).resolves.toBeUndefined();
    await expect(middleware(request('/__sitemap__/versions-bucket-1.xml'))).resolves.toBeUndefined();
  });

  it('leaves unknown bucket numbers to the sitemap module when the source is enabled', async () => {
    state.fonts = true;
    state.versions = true;
    const { default: middleware } = await import('../server/middleware/sitemap-fonts');

    await expect(middleware(request('/__sitemap__/fonts-bucket-99.xml'))).resolves.toBeUndefined();
    await expect(middleware(request('/__sitemap__/versions-bucket-99.xml'))).resolves.toBeUndefined();
  });

  it('does not intercept other sitemaps while sources are disabled', async () => {
    const { default: middleware } = await import('../server/middleware/sitemap-fonts');
    for (const path of ['/sitemap_index.xml', '/__sitemap__/apps-bucket-1.xml', '/__sitemap__/en-static.xml'])
      await expect(middleware(request(path))).resolves.toBeUndefined();
    expect(getSiteSettingsMock).not.toHaveBeenCalled();
  });

  it('also prevents caching a 410 thrown by the sitemap source hook', async () => {
    type ErrorHook = (error: { statusCode: number }, context: { event: ReturnType<typeof request> }) => void;
    let onError!: ErrorHook;
    vi.stubGlobal('defineNitroPlugin', (plugin: (app: unknown) => void) =>
      plugin({
        hooks: {
          hook: (name: string, handler: ErrorHook) => {
            if (name === 'error') onError = handler;
          }
        }
      })
    );
    await import('../server/plugins/error-cache-control');
    for (const path of [
      '/__sitemap__/versions-bucket-1.xml',
      '/__nuxt_error?url=%2F__sitemap__%2Fversions-bucket-1.xml'
    ]) {
      const event = request(path);
      onError({ statusCode: 410 }, { event });
      for (const header of ['cache-control', 'cdn-cache-control', 'cloudflare-cdn-cache-control'])
        expect(getResponseHeader(event, header), header).toBe('no-store');
      expect(getResponseHeader(event, 'retry-after')).toBeUndefined();
    }
  });
});
