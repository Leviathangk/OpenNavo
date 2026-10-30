import { describe, expect, it, vi } from 'vitest';
import { ROBOTS_PRIVATE_PATHS } from '@opennavo/shared';

// Evaluate the actual Nuxt configuration without starting Nuxt or a browser.
vi.stubGlobal('defineNuxtConfig', (config: unknown) => config);
const { default: config } = await import('../nuxt.config');
vi.unstubAllGlobals();

// Every site language (L1), the four site-only ones included.
const prefixes = ['', '/zh', '/ja', '/es', '/pt', '/ru', '/de', '/fr', '/it', '/zh-hant'];
const ttls: Record<string, number> = {
  '/': 300,
  '/discover': 300,
  '/about': 900,
  '/feedback': 900,
  '/apps': 600,
  // App pages and their feeds are dropped on change and cached per build, so they keep six hours.
  '/apps/**': 21600,
  '/categories': 900,
  '/categories/**': 900,
  '/rankings': 900,
  '/download': 600,
  '/security': 900,
  // F8 security topics, T1 A–Z directory and F1 background-items encyclopedia.
  '/security/**': 900,
  '/browse': 900,
  '/browse/**': 900,
  '/background-items': 900,
  '/background-items/**': 900,
  '/articles': 600,
  '/articles/**': 600,
  '/authors/**': 600,
  // X5: collections are cached like other lists; editing one purges its pages by prefix.
  '/collections': 300,
  '/collections/**': 300
};

describe('link prefetching', () => {
  it('prefetches linked pages on pointer or focus, never because a link is on screen', () => {
    // Visibility prefetching made Google's renderer fetch the payload of every linked page.
    const experimental = config.experimental as {
      defaults?: { nuxtLink?: { prefetchOn?: unknown; prefetch?: unknown } };
    };
    expect(experimental.defaults?.nuxtLink).toEqual({ prefetchOn: { visibility: false, interaction: true } });
  });
});

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
      const expected =
        path === '/'
          ? { cache: { swr: true, maxAge: swr } }
          : path === '/apps/**'
            ? { cache: { swr: true, maxAge: swr } }
            : { swr };
      expect(config.$production?.routeRules?.[actualPath], actualPath).toEqual(expected);
      expect(config.routeRules?.[actualPath]?.swr).toBeUndefined();
    }
    expect(config.$production?.routeRules?.[`${prefix}/recent`]).toEqual({
      cache: { maxAge: 60, swr: true, staleMaxAge: 300 }
    });
    expect(config.$production?.routeRules?.[`${prefix}/apps/*/versions`]).toEqual({
      cache: { maxAge: 21600, swr: true, staleMaxAge: 64800 }
    });
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

  it.each(prefixes)('caches %s collection pages like other lists (X5)', prefix => {
    for (const path of ['/collections', '/collections/**']) {
      expect(config.routeRules?.[`${prefix}${path}`]).toBeUndefined();
      expect(config.$production?.routeRules?.[`${prefix}${path}`]).toEqual({ swr: 300 });
    }
  });

  it('does not generate obsolete English prefixes or broad cache rules covering search', () => {
    const production = config.$production?.routeRules ?? {};
    expect(Object.keys(production)).toHaveLength(prefixes.length * (Object.keys(ttls).length + 2));
    for (const path of [...Object.keys(production), ...Object.keys(config.routeRules ?? {})]) {
      expect(path).not.toMatch(/^\/en(?:\/|$)/);
      expect(path).not.toMatch(/^(?:\/zh|\/ja|\/es|\/pt|\/ru|\/de|\/fr|\/it|\/zh-hant)?\/\*\*$/);
    }
  });
});

it('explicitly caches signed OG images in the existing bounded image storage for thirty days at the CDN', () => {
  expect(config.ogImage).toEqual({
    runtimeCacheStorage: true,
    defaults: { width: 1200, height: 630, emojis: false, cacheMaxAgeSeconds: 2592000 }
  });
});

describe('G1 deployment-only bot verification', () => {
  it('defaults to disabled even though the ingress header name is configured', () => {
    expect(config.runtimeConfig?.cfVerifiedBotEnabled).toBe(false);
    expect(config.runtimeConfig?.verifiedBotHeader).toBe('X-OpenNavo-Verified-Bot');
    expect(config.runtimeConfig?.trustedProxies).toBe('');
    expect(config.runtimeConfig?.public).not.toHaveProperty('cfVerifiedBotEnabled');
  });
});
