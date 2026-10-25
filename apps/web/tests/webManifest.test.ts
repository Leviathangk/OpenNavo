import { readFileSync } from 'node:fs';
import { describe, expect, it, vi } from 'vitest';
import { BRAND } from '@opennavo/shared';
import { color } from '@opennavo/tokens';

// Evaluate the actual Nuxt configuration without starting Nuxt or a browser.
vi.stubGlobal('defineNuxtConfig', (config: unknown) => config);
const { default: config } = await import('../nuxt.config');
vi.unstubAllGlobals();

type Icon = { src: string; sizes: string; type: string };
const publicFile = (path: string) => readFileSync(new URL(`../public${path}`, import.meta.url));
const manifest = JSON.parse(publicFile('/site.webmanifest').toString('utf8')) as {
  name: string;
  short_name: string;
  display: string;
  theme_color: string;
  background_color: string;
  icons: Icon[];
};
const headLinks = config.app!.head!.link as Array<{ rel: string; href: string; sizes?: string; type?: string }>;

describe('Web app manifest', () => {
  it('names the brand, uses the dark page color and stays a website', () => {
    expect(manifest.name).toBe(BRAND.name);
    expect(manifest.short_name).toBe(BRAND.name);
    // Pages are served dark (08 §3.7).
    expect(manifest.theme_color).toBe(color.surface.page);
    expect(manifest.background_color).toBe(color.surface.page);
    // Installing it would look like installing the Mac app, which is a separate download.
    expect(manifest.display).toBe('browser');
  });

  it('lists the icons the head links, at their real sizes', () => {
    const icons = headLinks.filter(link => link.rel === 'icon' || link.rel === 'apple-touch-icon');
    expect(manifest.icons.map(icon => icon.src).sort()).toEqual(icons.map(icon => icon.href).sort());
    for (const icon of manifest.icons) {
      const file = publicFile(icon.src);
      if (icon.type === 'image/svg+xml') {
        expect(icon.sizes).toBe('any');
        continue;
      }
      expect(icon.type).toBe('image/png');
      // Width and height from the PNG header.
      expect(icon.sizes).toBe(`${file.readUInt32BE(16)}x${file.readUInt32BE(20)}`);
    }
  });

  it('is linked from every page', () => {
    expect(headLinks).toContainEqual({ rel: 'manifest', href: '/site.webmanifest' });
  });
});

describe('Icon caching', () => {
  it.each([
    '/favicon.ico',
    '/favicon.svg',
    '/favicon-32.png',
    '/icon-512.png',
    '/apple-touch-icon.png',
    '/site.webmanifest'
  ])('%s may be cached for a day and refreshed in the background', path => {
    expect(() => publicFile(path)).not.toThrow();
    expect(config.routeRules?.[path]?.headers).toEqual({
      'cache-control': 'public, max-age=86400, stale-while-revalidate=604800'
    });
  });
});
