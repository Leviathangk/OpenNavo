import { afterEach, describe, expect, it, vi } from 'vitest';

vi.stubGlobal('defineNitroPlugin', (plugin: unknown) => plugin);
const { default: inlineStyles } = await import('../server/plugins/html-styles');
const { default: htmlHeaders } = await import('../server/plugins/html-response-headers');
afterEach(() => vi.unstubAllGlobals());

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

it('sets COOP on HTML responses, including cache hits, without changing JSON or asset responses', () => {
  let respond = (_event: unknown) => {};
  const setHeader = vi.fn();
  vi.stubGlobal('setHeader', setHeader);
  htmlHeaders({
    hooks: {
      hook: (_name: string, callback: typeof respond) => {
        respond = callback;
      }
    }
  } as never);
  for (const type of ['text/html;charset=utf-8', 'application/json', 'text/css', 'text/html']) {
    respond({ node: { res: { getHeader: () => type } } });
  }
  expect(setHeader).toHaveBeenCalledTimes(2);
  expect(setHeader).toHaveBeenCalledWith(expect.anything(), 'Cross-Origin-Opener-Policy', 'same-origin-allow-popups');
});
