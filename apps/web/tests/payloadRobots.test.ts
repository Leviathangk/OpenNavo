import { afterEach, expect, it, vi } from 'vitest';
afterEach(() => {
  vi.unstubAllGlobals();
  vi.resetModules();
});

it.each(['/apps/editor/_payload.json', '/zh/apps/editor/_payload.json?abc=1', '/_payload.json'])(
  'keeps payload header noindex after head resolution: %s',
  async path => {
    const header = { value: '' };
    let resolve!: (head: { tags: unknown[] }) => void;
    vi.stubGlobal('defineNuxtPlugin', (fn: () => void) => fn());
    vi.stubGlobal('useRequestURL', () => new URL(path, 'https://example.test'));
    vi.stubGlobal('useResponseHeader', () => header);
    vi.stubGlobal('injectHead', () => ({
      hooks: {
        hook: (_: string, fn: typeof resolve) => {
          resolve = fn;
        }
      }
    }));
    await import('../app/plugins/robots-header.server');
    expect(header.value).toBe('noindex');
    resolve({ tags: [{ tag: 'meta', props: { name: 'robots', content: 'index, follow' } }] });
    expect(header.value).toBe('noindex');
  }
);

it('overrides cached payload headers even if rendering rewrites the request path', async () => {
  type Event = { path: string; context: Record<string, unknown> };
  const hooks: Record<string, (event: Event) => void> = {};
  const setHeader = vi.fn();
  vi.stubGlobal('setHeader', setHeader);
  vi.stubGlobal('defineNitroPlugin', (fn: (nitro: unknown) => void) =>
    fn({
      hooks: {
        hook: (name: string, callback: (event: Event) => void) => {
          hooks[name] = callback;
        }
      }
    })
  );
  await import('../server/plugins/payload-robots');
  const event = { path: '/zh/apps/editor/_payload.json?hash=1', context: {} };
  hooks.request!(event);
  event.path = '/zh/apps/editor';
  hooks.beforeResponse!(event);
  expect(setHeader).toHaveBeenCalledWith(event, 'X-Robots-Tag', 'noindex');
  setHeader.mockClear();
  for (const path of ['/apps/editor', '/apps/editor/_payload.json/other']) {
    const html = { path, context: {} };
    hooks.request!(html);
    hooks.beforeResponse!(html);
  }
  expect(setHeader).not.toHaveBeenCalled();
});
