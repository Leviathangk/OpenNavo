import { packageSitemaps } from './shared/utils/sitemapBuckets';
import { LOCALES, LOCALE_METADATA, DEFAULT_LOCALE } from '@opennavo/shared/locales';

// Expand each policy over the same prefixes used to generate localized page routes.
function localizedRules<T>(rules: Record<string, T>): Record<string, T> {
  return Object.fromEntries(
    LOCALES.flatMap(locale => {
      const prefix = LOCALE_METADATA[locale].prefix;
      return Object.entries(rules).map(([path, rule]) => [path === '/' ? prefix || '/' : `${prefix}${path}`, rule]);
    })
  );
}
const privatePagePaths = Object.keys(localizedRules({ '/search': true, '/brewfile': true }));
// The article list joins the sitemap with the articles group, once there are articles to list.
const articleListPaths = Object.keys(localizedRules({ '/articles': true }));

// Web app (05-web.md): SSR, English at the root path, other languages at their manifest prefixes.
export default defineNuxtConfig({
  compatibilityDate: '2026-10-01',
  devtools: { enabled: false },
  // Combine site styles into one file (about 10 KB), leaving one render-blocking stylesheet request on first load (05 §8).
  vite: { build: { cssCodeSplit: false } },
  modules: ['@unocss/nuxt', '@nuxtjs/i18n', '@nuxt/fonts', '@nuxtjs/seo'],
  // Register components by filename without directory prefixes; names are unique throughout components/.
  components: [{ path: '~/components', pathPrefix: false }],
  // Links prefetch the linked page (its _payload.json and route chunks) only when a visitor points at
  // or focuses them. Prefetching on visibility made Google's renderer, which lays out the whole page
  // at once, fetch the payload of every linked app: about 75% of Googlebot's requests were payloads.
  // Every link on the site is this default NuxtLink, passed to @opennavo/ui as link-as.
  experimental: { defaults: { nuxtLink: { prefetchOn: { visibility: false, interaction: true } } } },
  css: [
    '@unocss/reset/tailwind.css',
    '@opennavo/tokens/tokens.css',
    '@opennavo/ui/base.css',
    '@opennavo/ui/system-fonts.css'
  ],
  runtimeConfig: {
    // SSR uses the internal URL (NUXT_API_BASE_INTERNAL), falling back to the public URL when unset.
    apiBaseInternal: '',
    // The public CDN's object store inside the Docker network (NUXT_CDN_BASE_INTERNAL): share images
    // read icons there (shared/utils/ogIcon.ts) instead of through Cloudflare; empty keeps the CDN.
    cdnBaseInternal: '',
    // Trust only explicitly configured ingress proxy IPs (comma-separated); ignore forwarded headers by default.
    trustedProxies: '',
    // Internal port (NUXT_METRICS_PORT) for crawler counters and page-cache invalidation; empty disables it.
    metricsPort: '',
    // Shared with the Go worker (WEB_INTERNAL_SECRET) to authenticate cache invalidation; empty refuses all.
    internalSecret: '',
    redisUrl: '',
    public: {
      apiBase: 'http://localhost:8080/api/v1',
      siteUrl: 'http://localhost:3000',
      cdnBase: 'http://localhost:9000/opennavo',
      appVersion: '0.0.0',
      // Inject the site URL only at runtime: nuxt.config is evaluated at build time, when process.env in the image is empty.
      // NUXT_PUBLIC_SITE_URL overrides both siteUrl above and site.url here (site configuration: og:image, sitemap, robots);
      // NUXT_PUBLIC_I18N_BASE_URL overrides i18n.baseUrl (canonical, hreflang); use the same value for both in deployment (05 §9).
      site: { url: '' },
      // Generate short route codes from manifest prefixes; API requests still use full locale codes.
      i18n: { baseUrl: 'http://localhost:3000' }
    }
  },
  app: {
    head: {
      // 08 §14: SVG icons adapt the star color to the system theme; PNG serves browsers without SVG icon support and the iOS home screen; root favicon.ico is the fallback.
      link: [
        { rel: 'icon', type: 'image/svg+xml', href: '/favicon.svg' },
        { rel: 'icon', type: 'image/png', sizes: '32x32', href: '/favicon-32.png' },
        { rel: 'icon', type: 'image/png', sizes: '512x512', href: '/icon-512.png' },
        { rel: 'apple-touch-icon', sizes: '180x180', href: '/apple-touch-icon.png' }
      ],
      meta: [{ name: 'color-scheme', content: 'dark' }]
    }
  },
  // Generate short route codes from manifest prefixes; API requests still use full locale codes.
  i18n: {
    vueI18n: './i18n.config.ts',
    defaultLocale: 'en',
    strategy: 'prefix_except_default',
    locales: LOCALES.map(code => ({
      code: LOCALE_METADATA[code].prefix.slice(1) || 'en',
      language: LOCALE_METADATA[code].hreflang,
      name: LOCALE_METADATA[code].name,
      file: `${code}.json`
    })),
    // Do not set baseUrl here: a build-time value takes highest priority in site configuration, overriding the runtime URL (see runtimeConfig.public.i18n).
    detectBrowserLanguage: false
  },
  // Development default; production overrides this via runtime NUXT_PUBLIC_SITE_URL (runtimeConfig.public.site).
  site: {
    url: 'http://localhost:3000',
    name: 'OpenNavo',
    defaultLocale: DEFAULT_LOCALE
  },
  // Pages provide their own titles. The SEO fallback translates URL segments as
  // message templates, so valid versioned tokens such as app@11 crash SSR.
  seo: { fallbackTitle: false },
  // Share images (05 §6.3, 08 §14): Satori renders at 1200×630; the Chinese font is a subset in public/fonts generated by scripts/og-font.mjs.
  ogImage: { defaults: { width: 1200, height: 630, emojis: false } },
  // Use @nuxt/fonts only to pass share-image fonts to the renderer: disable all remote providers; the site uses system fonts.
  fonts: {
    providers: {
      adobe: false,
      bunny: false,
      fontshare: false,
      fontsource: false,
      google: false,
      googleicons: false,
      npm: false
    },
    families: [
      { name: 'OG Noto Sans JP', src: '/fonts/og-noto-sans-jp-400.woff', weight: 400, global: true, preload: false },
      { name: 'OG Noto Sans JP', src: '/fonts/og-noto-sans-jp-600.woff', weight: 600, global: true, preload: false },
      { name: 'OG Noto Sans SC', src: '/fonts/og-noto-sans-sc-400.woff', weight: 400, global: true, preload: false },
      { name: 'OG Noto Sans SC', src: '/fonts/og-noto-sans-sc-600.woff', weight: 600, global: true, preload: false }
    ]
  },
  linkChecker: { enabled: false },
  // Sitemap (05 §6.4, WP3): one sitemap per business group so each can be diagnosed on its own. static
  // holds the app routes (home, lists, about, download); server/plugins/sitemap-sources.ts fills the
  // other groups from the API with per-language lastmod. Large groups shard at 10,000 URLs.
  sitemap: {
    sitemaps: {
      static: { includeAppSources: true, exclude: [...privatePagePaths, ...articleListPaths] },
      ...packageSitemaps,
      categories: { includeAppSources: false },
      collections: { includeAppSources: false },
      articles: { includeAppSources: false },
      authors: { includeAppSources: false }
    }
  },
  // Exclude search results and Brewfile lists from indexing (05 §6.4).
  // List actual manifest paths explicitly rather than assuming a particular language prefix.
  // robots.txt itself is generated at runtime from the admin crawler policy
  // (server/routes/robots.txt.ts). Only the default group stays here, so the robots meta tag and
  // X-Robots-Tag never depend on the requesting User-Agent and cannot leak into shared SWR caches.
  robots: {
    robotsTxt: false,
    disallow: privatePagePaths
  },
  // Generate the About page canonical URL at runtime too, so a generic image cannot embed the build host URL in HTML.
  routeRules: {
    ...localizedRules({
      '/search': { cache: false, headers: { 'cache-control': 'no-store' } },
      // Keep collection HTML and data fresh together, including empty results and API failure states.
      '/collections': { cache: false, headers: { 'cache-control': 'no-store' } },
      '/collections/**': { cache: false, headers: { 'cache-control': 'no-store' } },
      '/brewfile': { ssr: false }
    }),
    // Permanent moves for addresses devices and directories guess: older iOS asks for the
    // precomposed touch icon, and some directories expect a 512px logo. /sitemap.xml is in hooks.
    '/apple-touch-icon-precomposed.png': { redirect: { to: '/apple-touch-icon.png', statusCode: 301 } },
    '/img/logo_512.png': { redirect: { to: '/icon-512.png', statusCode: 301 } }
  },
  // Route caching applies only to production builds; development renders every request (05 §4).
  $production: {
    routeRules: localizedRules({
      '/': { swr: 300 },
      '/discover': { swr: 300 },
      '/about': { swr: 900 },
      // The form posts from the browser; the page itself has nothing per visitor (no cookies or
      // tokens), and prefilled ?type=&q= variants are cached per URL.
      '/feedback': { swr: 900 },
      '/apps': { swr: 600 },
      '/categories': { swr: 900 },
      '/categories/**': { swr: 900 },
      '/rankings': { swr: 900 },
      // Follows Discover, whose Recently Updated section it lists in full.
      '/recent': { swr: 300 },
      '/download': { swr: 600 },
      '/security': { swr: 900 },
      '/apps/**': { swr: 600 },
      // Publishing, withdrawing and translations purge these pages (WP4), so the cache can be long.
      '/articles': { swr: 600 },
      '/articles/**': { swr: 600 },
      '/authors/**': { swr: 600 }
    })
  },
  // Article feeds (WP17) at fixed paths per language, which take precedence over the cached
  // <prefix>/articles/** page routes.
  serverHandlers: LOCALES.map(code => ({
    route: `${LOCALE_METADATA[code].prefix}/articles/feed.xml`,
    handler: '~~/server/handlers/articleFeed.ts'
  })),
  // Generate gzip and brotli assets at build time and serve by Accept-Encoding (the production Caddy proxy also compresses dynamic pages).
  nitro: { compressPublicAssets: { gzip: true, brotli: true } },
  hooks: {
    // @nuxtjs/sitemap writes /sitemap.xml → /sitemap_index.xml straight into Nitro's route rules as a
    // 307 during module setup, over anything in routeRules; crawlers that guess the address should
    // see a permanent move.
    'nitro:config'(nitroConfig) {
      nitroConfig.routeRules = {
        ...nitroConfig.routeRules,
        '/sitemap.xml': { redirect: { to: '/sitemap_index.xml', statusCode: 301 } }
      };
    }
  },
  typescript: {
    tsConfig: {
      compilerOptions: { noUncheckedIndexedAccess: true }
    }
  }
});
