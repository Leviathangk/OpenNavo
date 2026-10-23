// @vitest-environment jsdom
// @vitest-environment-options {"runScripts":"outside-only"}
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createHead, renderDOMHead } from 'unhead/client';

// Evaluate the actual Nuxt configuration without starting Nuxt or a browser.
vi.stubGlobal('defineNuxtConfig', (config: unknown) => config);
const { default: config } = await import('../nuxt.config');
vi.unstubAllGlobals();

type HtmlContext = { htmlAttrs: string[] };

afterEach(() => {
  delete document.documentElement.dataset.theme;
  localStorage.clear();
  vi.unstubAllGlobals();
  vi.resetModules();
});

describe('Color theme of a page (08 §3.7)', () => {
  it('serves every page dark', async () => {
    const hooks: Record<string, (html: HtmlContext) => void> = {};
    vi.stubGlobal('defineNitroPlugin', (plugin: (app: unknown) => void) =>
      plugin({ hooks: { hook: (name: string, handler: (html: HtmlContext) => void) => (hooks[name] = handler) } })
    );
    await import('../server/plugins/theme');
    const html: HtmlContext = { htmlAttrs: ['lang="zh-Hans"'] };
    hooks['render:html']!(html);
    expect(html.htmlAttrs).toEqual(['lang="zh-Hans"', 'data-theme="dark"']);
  });

  it.each([
    ['light', 'light'],
    ['dark', 'dark'],
    ['system', undefined]
  ])('keeps a saved %s choice after hydration', (choice, theme) => {
    const appHead = config.app!.head!;
    const root = document.documentElement;
    // The served page, then the head script applying the saved choice before the bundle loads.
    root.dataset.theme = 'dark';
    localStorage.setItem('onv:theme', choice);
    new Function(String(appHead.script!.at(0)!.innerHTML))();
    expect(root.dataset.theme).toBe(theme);
    // Nuxt pushes app.head on the client too and renders it when hydration ends (installClientHead). The head
    // script already ran from the served HTML; jsdom does not run it again (runScripts above), like a browser
    // that finds the served element.
    const head = createHead({ document });
    head.push(appHead as Parameters<typeof head.push>[0]);
    renderDOMHead(head, { document });
    expect(root.dataset.theme).toBe(theme);
  });
});
