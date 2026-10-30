import { IncomingMessage, ServerResponse } from 'node:http';
import { Socket } from 'node:net';
import { createEvent, getResponseHeader, setHeader, type H3Event } from 'h3';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { DEFAULT_SITE_SETTINGS } from '../server/utils/siteSettings';

vi.stubGlobal('defineNitroPlugin', (plugin: unknown) => plugin);
const { default: inlineStyles } = await import('../server/plugins/html-styles');
const { default: htmlHeaders } = await import('../server/plugins/html-response-headers');
const sockets: Socket[] = [];
afterEach(() => {
  vi.unstubAllGlobals();
  for (const socket of sockets.splice(0)) socket.destroy();
});

function eventFor(path: string, contentType = 'text/html; charset=utf-8', status = 200) {
  const socket = new Socket();
  sockets.push(socket);
  const request = new IncomingMessage(socket);
  request.url = path;
  const event = createEvent(request, new ServerResponse(request));
  event.node.res.statusCode = status;
  setHeader(event, 'Content-Type', contentType);
  event.context.siteSettings = {
    ...DEFAULT_SITE_SETTINGS,
    pages: { ...DEFAULT_SITE_SETTINGS.pages, openLocales: ['en-US', 'zh-CN', 'ja-JP', 'es-ES', 'ru-RU'] }
  };
  return event;
}

async function respond(event: H3Event) {
  let beforeResponse = async (_event: H3Event) => {};
  htmlHeaders({
    hooks: {
      hook: (_name: string, callback: typeof beforeResponse) => {
        beforeResponse = callback;
      }
    }
  } as never);
  await beforeResponse(event);
}

describe('HTML first-paint styles', () => {
  it('reads this build once, replaces only its stylesheet, and preserves the cascade on cached renders', async () => {
    const getItem = vi.fn(async () => 'body{color:red}');
    vi.stubGlobal('useStorage', () => ({ getKeys: async () => ['style.fixture.css'], getItem }));
    let render = (_html: { head: string[] }) => {};
    await inlineStyles({
      hooks: {
        hook: (_name: string, callback: typeof render) => {
          render = callback;
        }
      }
    } as never);
    const original = [
      '<meta charset="utf-8"><link rel="stylesheet" href="/_nuxt/style.fixture.css" crossorigin><style>.override{color:blue}</style>',
      '<link rel="stylesheet" href="/_nuxt/style.fixture.css">',
      '<link rel="stylesheet" href="/other.css"><link rel="preload" as="font" href="/font.woff">'
    ];
    for (let request = 0; request < 2; request++) {
      const html = { head: [...original] };
      render(html);
      expect(html.head[0]).toBe(
        '<meta charset="utf-8"><style data-site-css="style.fixture.css">body{color:red}</style><style>.override{color:blue}</style>'
      );
      expect(html.head[1]).toBe('');
      expect(html.head[2]).toBe(original[2]);
    }
    expect(getItem).toHaveBeenCalledOnce();
  });

  it('leaves the original stylesheet available if there is no matching bundled asset', async () => {
    vi.stubGlobal('useStorage', () => ({ getKeys: async () => [], getItem: vi.fn() }));
    let render = (_html: { head: string[] }) => {};
    await inlineStyles({
      hooks: {
        hook: (_name: string, callback: typeof render) => {
          render = callback;
        }
      }
    } as never);
    const html = { head: ['<link rel="stylesheet" href="/_nuxt/style.fixture.css">'] };
    render(html);
    expect(html.head).toEqual(['<link rel="stylesheet" href="/_nuxt/style.fixture.css">']);
  });
});

it('sets COOP and agent headers on HTML responses, including cache hits', async () => {
  vi.stubGlobal('useRuntimeConfig', () => ({ public: { markdownAlternates: true } }));
  for (const type of ['text/html;charset=utf-8', 'text/html']) {
    const event = eventFor('/', type);
    await respond(event);
    expect(getResponseHeader(event, 'Cross-Origin-Opener-Policy')).toBe('same-origin-allow-popups');
    expect(getResponseHeader(event, 'Link')).toBe('</llms.txt>; rel="describedby"');
    expect(getResponseHeader(event, 'Vary')).toBe('Accept');
  }
});

it.each(['application/json', 'text/css', 'text/markdown'])('leaves %s responses alone', async type => {
  const event = eventFor('/', type);
  await respond(event);
  expect(getResponseHeader(event, 'Cross-Origin-Opener-Policy')).toBeUndefined();
  expect(getResponseHeader(event, 'Link')).toBeUndefined();
  expect(getResponseHeader(event, 'Vary')).toBeUndefined();
});

it.each([
  { previous: undefined },
  { previous: '<https://opennavo.example/zh/apps/iterm2>; rel="canonical", </font.woff>; rel="preload"; title="a, b"' },
  { previous: ['<https://opennavo.example/zh/apps/iterm2>; rel="canonical"', '</font.woff>; rel="preload"'] }
])('merges describedby and Markdown alternate links into existing Link headers: %j', async ({ previous }) => {
  vi.stubGlobal('useRuntimeConfig', () => ({ public: { markdownAlternates: true } }));
  const event = eventFor('/zh/apps/iterm2');
  if (previous) setHeader(event, 'Link', previous);
  setHeader(event, 'Vary', 'Accept-Language');
  await respond(event);
  expect(getResponseHeader(event, 'Vary')).toBe('Accept-Language, Accept');
  expect(getResponseHeader(event, 'Link')).toBe(
    [
      ...[previous ?? []].flat(),
      '</llms.txt>; rel="describedby"',
      '</zh/apps/iterm2.md>; rel="alternate"; type="text/markdown"'
    ].join(', ')
  );
});

it.each([false, '1'])('keeps describedby independent of the disabled Markdown switch: %j', async disabled => {
  vi.stubGlobal('useRuntimeConfig', () => ({ public: { markdownAlternates: disabled } }));
  const event = eventFor('/zh/apps/iterm2');
  setHeader(event, 'Link', '</font.woff>; rel="preload"');
  await respond(event);
  expect(getResponseHeader(event, 'Link')).toBe('</font.woff>; rel="preload", </llms.txt>; rel="describedby"');
  expect(getResponseHeader(event, 'Vary')).toBeUndefined();
});

it.each([301, 404, 410, 500])('does not add agent headers to an HTML %s response', async status => {
  const event = eventFor('/zh/apps/iterm2', 'text/html', status);
  await respond(event);
  expect(getResponseHeader(event, 'Cross-Origin-Opener-Policy')).toBe('same-origin-allow-popups');
  expect(getResponseHeader(event, 'Link')).toBeUndefined();
  expect(getResponseHeader(event, 'Vary')).toBeUndefined();
});

it.each(['status', 'statusCode'])(
  'reads the final error %s when Nuxt internally renders a 404 with node status 200',
  async field => {
    const event = eventFor(`/__nuxt_error?${field}=404&url=/apps/no-such-app-zz`);
    await respond(event);
    expect(getResponseHeader(event, 'Link')).toBeUndefined();
    expect(getResponseHeader(event, 'Vary')).toBeUndefined();
  }
);

it.each(['/pt', '/de', '/fr', '/it', '/zh-hant'])(
  'does not advertise Markdown for the closed locale %s',
  async prefix => {
    vi.stubGlobal('useRuntimeConfig', () => ({ public: { markdownAlternates: true } }));
    for (const path of [prefix, `${prefix}/apps/iterm2`]) {
      const event = eventFor(path);
      await respond(event);
      expect(getResponseHeader(event, 'Link')).toBe('</llms.txt>; rel="describedby"');
      expect(getResponseHeader(event, 'Vary')).toBeUndefined();
    }
  }
);
