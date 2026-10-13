import { describe, expect, it, vi } from 'vitest';
import { ROBOTS_PRIVATE_PATHS } from '@opennavo/shared';

// Evaluate the actual Nuxt configuration without starting Nuxt or a browser.
vi.stubGlobal('defineNuxtConfig', (config: unknown) => config);
const { default: config } = await import('../nuxt.config');
vi.unstubAllGlobals();

const prefixes = ['', '/zh', '/ja', '/es', '/pt', '/ru'];
const ttls: Record<string, number> = {
  '/': 300,
  '/discover': 300,
  '/about': 900,
  '/apps': 600,
  '/apps/**': 600,
  '/categories/**': 900,
  '/rankings': 900,
  '/download': 600,
  '/articles': 600,
  '/articles/**': 600,
  '/authors/**': 600
};

describe('robots module configuration', () => {
  const robots = config.robots as { robotsTxt?: boolean; disallow: string[]; groups?: unknown[] };

  it('leaves robots.txt to the runtime route and keeps page rules independent of the User-Agent', () => {
    expect(robots.robotsTxt).toBe(false);
    expect(robots.groups).toBeUndefined();
    // Page-level noindex and robots.txt disallow the same private paths.
    expect(robots.disallow).toEqual(ROBOTS_PRIVATE_PATHS);
  });
});

describe('localized route policies', () => {
  it.each(prefixes)('preserves production cache TTLs at the actual %s locale paths', prefix => {
    for (const [path, swr] of Object.entries(ttls)) {
      const actualPath = path === '/' ? prefix || '/' : `${prefix}${path}`;
      expect(config.$production?.routeRules?.[actualPath], actualPath).toEqual({ swr });
      expect(config.routeRules?.[actualPath]?.swr).toBeUndefined();
    }
  });

  it.each(prefixes)('keeps %s search uncached and Brewfile client-only and excludes both from indexing', prefix => {
    const search = `${prefix}/search`;
    const brewfile = `${prefix}/brewfile`;
    expect(config.routeRules?.[search]).toEqual({ cache: false, headers: { 'cache-control': 'no-store' } });
    expect(config.$production?.routeRules?.[search]).toBeUndefined();
    expect(config.routeRules?.[brewfile]).toEqual({ ssr: false });
    expect(config.$production?.routeRules?.[brewfile]).toBeUndefined();
    const sitemap = config.sitemap as { sitemaps: { static: { exclude: string[] } } };
    const robots = config.robots as { disallow: string[] };
    for (const path of [search, brewfile]) {
      expect(sitemap.sitemaps.static.exclude).toContain(path);
      expect(robots.disallow).toContain(path);
    }
  });

  it.each(prefixes)('lists %s articles in their own sitemap group only', prefix => {
    const sitemap = config.sitemap as { sitemaps: Record<string, { includeAppSources?: boolean; exclude?: string[] }> };
    expect(sitemap.sitemaps.static?.exclude).toContain(`${prefix}/articles`);
    expect(sitemap.sitemaps.articles).toEqual({ includeAppSources: false });
    expect(sitemap.sitemaps.authors).toEqual({ includeAppSources: false });
  });

  it.each(prefixes)('does not cache %s collection HTML or extracted payloads', prefix => {
    for (const path of ['/collections', '/collections/**']) {
      expect(config.routeRules?.[`${prefix}${path}`]).toEqual({
        cache: false,
        headers: { 'cache-control': 'no-store' }
      });
      expect(config.$production?.routeRules?.[`${prefix}${path}`]).toBeUndefined();
    }
  });

  it('does not generate obsolete English prefixes or broad cache rules covering search', () => {
    const production = config.$production?.routeRules ?? {};
    expect(Object.keys(production)).toHaveLength(prefixes.length * Object.keys(ttls).length);
    for (const path of [...Object.keys(production), ...Object.keys(config.routeRules ?? {})]) {
      expect(path).not.toMatch(/^\/en(?:\/|$)/);
      expect(path).not.toMatch(/^(?:\/zh|\/ja|\/es|\/pt|\/ru)?\/\*\*$/);
    }
  });
});
