import { resolve } from 'node:path';
import { packageSitemaps } from './shared/utils/sitemapBuckets';
import { THEME_STORAGE_KEY } from './shared/utils/theme';
import { themeBootScript } from '@opennavo/ui/theme';
// The website serves every site locale (L1): the six app languages plus de, fr, it and zh-Hant.
import { SITE_LOCALES as LOCALES, LOCALE_METADATA, DEFAULT_LOCALE } from '@opennavo/shared/locales';

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
// Icons and the web app manifest keep their names across releases, unlike the fingerprinted /_nuxt/ files:
// browsers and the CDN keep them a day, then refresh them in the background.
const iconPaths = [
  '/favicon.ico',
  '/favicon.svg',
  '/favicon-32.png',
  '/icon-512.png',
  '/apple-touch-icon.png',
  '/site.webmanifest'
];
const iconRules = Object.fromEntries(
  iconPaths.map(path => [
    path,
    { headers: { 'cache-control': 'public, max-age=86400, stale-while-revalidate=604800' } }
  ])
);

// Web app (05-web.md): SSR, English at the root path, other languages at their manifest prefixes.
export default defineNuxtConfig({
  buildId: process.env.NUXT_BUILD_ID || undefined,
  compatibilityDate: '2026-10-01',
  devtools: { enabled: false },
  // One complete stylesheet is inlined by html-styles.ts, including client-only scenes.
  vite: {
    build: {
      cssCodeSplit: false,
      rolldownOptions: {
        output: {
          codeSplitting: {
            groups: [
              {
                // Keep primitives used by the shell/hero together without pulling in catalog views.
                name: 'ui-core',
                priority: 2,
                test: /packages[\\/]ui[\\/]src[\\/]components[\\/](OnAppCard|OnAppIcon|OnBadge|OnButton|OnGetButton|OnHighlightText|OnIcon|OnInstallCommand|OnLogo|OnMenu|OnNavItem|OnToast|OnToastRegion|OnTopNav)\.vue/
              },
              {
                name: 'ui-catalog',
                test: /packages[\\/]ui[\\/]src[\\/]components[\\/]/
              }
            ]
          }
        }
      }
    }
  },
  features: { inlineStyles: false },
  modules: ['@unocss/nuxt', '@nuxtjs/i18n', '@nuxt/fonts', '@nuxtjs/seo'],
  // Register components by filename without directory prefixes; names are unique throughout components/.
  components: [{ path: '~/components', pathPrefix: false }],
  // Links prefetch the linked page (its _payload.json and route chunks) only when a visitor points at
  // or focuses them. Prefetching on visibility made Google's renderer, which lays out the whole page
  // at once, fetch the payload of every linked app: about 75% of Googlebot's requests were payloads.
  // Every link on the site is this default NuxtLink, passed to @opennavo/ui as link-as.
  experimental: {
    // T6: the first render inlines its payload; _payload.json is only fetched for client-side navigation.
    payloadExtraction: 'client',
    defaults: { nuxtLink: { prefetchOn: { visibility: false, interaction: true } } }
  },
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
    // G1: NUXT_CF_VERIFIED_BOT_ENABLED is a deployment-only opt-in. Caddy must strip direct/spoofed
    // values; Cloudflare must overwrite true/false on every request. trustedProxies alone is insufficient.
    cfVerifiedBotEnabled: false,
    verifiedBotHeader: 'X-OpenNavo-Verified-Bot',
    // G6: short English /llms.txt, independently disabled with NUXT_LLMS_ENABLED=false.
    llmsEnabled: true,
    // Internal port (NUXT_METRICS_PORT) for crawler counters and page-cache invalidation; empty disables it.
    metricsPort: '',
    // Shared with the Go worker (WEB_INTERNAL_SECRET) to authenticate cache invalidation; empty refuses all.
    internalSecret: '',
    // NUXT_PROBE_CONCURRENCY limits all internal warmup callers together.
    probeConcurrency: 1,
    // NUXT_PROBE_MAX_RETRY_AFTER_SECONDS caps server-provided cooldowns.
    probeMaxRetryAfterSeconds: 15 * 60,
    redisUrl: '',
    // Separate Redis process: database numbers do not isolate eviction or memory.
    imageRedisUrl: '',
    // NUXT_LOAD_SHED_* overrides; one SSR thread at ~100ms CPU/render (~10 cold renders/s).
    // Normal 2–3/s stays below these limits. Reserve the last 48 slots for protected traffic.
    loadShed: {
      enabled: true,
      imageInFlight: 2,
      lowInFlight: 8,
      mediumInFlight: 16,
      extremeInFlight: 64,
      lowLagMs: 50,
      mediumLagMs: 200,
      extremeLagMs: 1000,
      // Per AI training crawler (gptbot, claudebot, amazonbot…): requests a minute and burst, then 429.
      crawlerPerMinute: 6,
      crawlerBurst: 12
    },
    public: {
      desktopBrewCaskAvailable: false,
      apiBase: 'http://localhost:8080/api/v1',
      siteUrl: 'http://localhost:3000',
      cdnBase: 'http://localhost:9000/opennavo',
      appVersion: '0.0.0',
      // X4 experiment: /apps/{token}.md and its <link rel="alternate" type="text/markdown"> (env
      // NUXT_PUBLIC_MARKDOWN_ALTERNATES); off until the fetch logs show agents use it.
      markdownAlternates: false,
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
      // The manifest gives home-screen shortcuts the name, icons and dark page color; it stays a website (display: browser).
      link: [
        { rel: 'icon', type: 'image/svg+xml', href: '/favicon.svg' },
        { rel: 'icon', type: 'image/png', sizes: '32x32', href: '/favicon-32.png' },
        { rel: 'icon', type: 'image/png', sizes: '512x512', href: '/icon-512.png' },
        { rel: 'apple-touch-icon', sizes: '180x180', href: '/apple-touch-icon.png' },
        { rel: 'manifest', href: '/site.webmanifest' }
      ],
      // Both themes (08 §3.7): pages are served dark (server/plugins/theme.ts) and this blocking head script applies
      // a saved choice before first paint. Never set data-theme here: the client renders app.head too and would
      // undo the saved choice after hydration.
      meta: [{ name: 'color-scheme', content: 'dark light' }],
      script: [{ innerHTML: themeBootScript(THEME_STORAGE_KEY), tagPosition: 'head', tagPriority: 'critical' }]
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
      // zh-Hans and zh-Hant share the language "zh"; its catch-all alternate stays on Simplified Chinese.
      ...(code === 'zh-CN' ? { isCatchallLocale: true } : {}),
      name: LOCALE_METADATA[code].name,
      file: `${code}.json`
    })),
    // Do not set baseUrl here: a build-time value takes highest priority in site configuration, overriding the runtime URL (see runtimeConfig.public.i18n).
    detectBrowserLanguage: false
    // T6 tried experimental.preload + stripMessagesPayload (messages in the SSR HTML, unused keys stripped).
    // In @nuxtjs/i18n 10.6.0 the preload plugin puts the module's deep-frozen cached messages into the request
    // context by reference, and the locale plugin's later loadMessages then writes into them: every page
    // answered 500 ("Cannot assign to read only property"). Off until a module release fixes it.
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
  // Structured data is explicit (T4): app.vue declares the one Organization and WebSite, and each page its
  // WebPage (usePageSchema). The module's per-language WebSite/WebPage defaults pointed at /zh#website.
  schemaOrg: { defaults: false },
  // Share images (05 §6.3, 08 §14): Satori renders at 1200×630; the Chinese font is a subset in public/fonts generated by scripts/og-font.mjs.
  ogImage: {
    runtimeCacheStorage: true,
    defaults: { width: 1200, height: 630, emojis: false, cacheMaxAgeSeconds: 30 * 86400 }
  },
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
    exclude: ['/llms.txt'],
    sitemaps: {
      static: { includeAppSources: true, exclude: [...privatePagePaths, ...articleListPaths] },
      ...packageSitemaps,
      categories: { includeAppSources: false },
      collections: { includeAppSources: false },
      articles: { includeAppSources: false },
      browse: { includeAppSources: false },
      'background-items': { includeAppSources: false },
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
    '/llms.txt': { cache: false, headers: { 'cache-control': 'no-store' } },
    ...localizedRules({
      '/search': { cache: false, headers: { 'cache-control': 'no-store' } },
      '/brewfile': { ssr: false }
    }),
    // Permanent moves for addresses devices and directories guess: older iOS asks for the
    // precomposed touch icon, and some directories expect a 512px logo. /sitemap.xml is in hooks.
    '/apple-touch-icon-precomposed.png': { redirect: { to: '/apple-touch-icon.png', statusCode: 301 } },
    '/img/logo_512.png': { redirect: { to: '/icon-512.png', statusCode: 301 } },
    ...iconRules
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
      // Changed apps purge every localized listing, including pagination and payloads.
      '/recent': { cache: { maxAge: 60, swr: true, staleMaxAge: 300 } },
      '/download': { swr: 600 },
      '/security': { swr: 900 },
      '/security/**': { swr: 900 },
      // T1 directory and F1 encyclopedia: lists of catalog data, refreshed like the categories.
      '/browse': { swr: 900 },
      '/browse/**': { swr: 900 },
      '/background-items': { swr: 900 },
      '/background-items/**': { swr: 900 },
      // Also the per-app release feeds (F6) at /apps/{token}/feed.xml. An app's changes drop its pages
      // by prefix (internal-server /cache/invalidate), and every web build caches under its own Redis
      // namespace (cache-storage.ts), so six hours never serves the previous build's HTML.
      '/apps/**': { swr: 21600 },
      '/apps/*/versions': { cache: { maxAge: 21600, swr: true, staleMaxAge: 64800 } },
      // X5: collections are cached like other lists; editing one purges its pages by prefix (S6 outbox,
      // BE-1), and error responses are never cached (route-cache-errors.ts, error-cache-control.ts).
      '/collections': { swr: 300 },
      '/collections/**': { swr: 300 },
      // Publishing, withdrawing and translations purge these pages (WP4), so the cache can be long.
      '/articles': { swr: 600 },
      '/articles/**': { swr: 600 },
      '/authors/**': { swr: 600 }
    })
  },
  // Feeds at fixed paths per language, which take precedence over the cached page routes: articles
  // (WP17), and release feeds per app and per collection (F6).
  serverHandlers: LOCALES.flatMap(code => {
    const prefix = LOCALE_METADATA[code].prefix;
    return [
      { route: `${prefix}/articles/feed.xml`, handler: '~~/server/handlers/articleFeed.ts' },
      { route: `${prefix}/apps/:token/feed.xml`, handler: '~~/server/handlers/packageFeed.ts' },
      { route: `${prefix}/collections/:slug/feed.xml`, handler: '~~/server/handlers/collectionFeed.ts' }
    ];
  }),
  // Generate gzip and brotli assets at build time and serve by Accept-Encoding (the production Caddy proxy also compresses dynamic pages).
  nitro: { compressPublicAssets: { gzip: true, brotli: true } },
  hooks: {
    // @nuxtjs/sitemap writes /sitemap.xml → /sitemap_index.xml straight into Nitro's route rules as a
    // 307 during module setup, over anything in routeRules; crawlers that guess the address should
    // see a permanent move.
    'nitro:config'(nitroConfig) {
      // Let the runtime _nuxt fallback route serve retained assets that are not in this build's
      // manifest. Current build assets remain handled by Nitro's manifest-backed static handler.
      nitroConfig.publicAssets = nitroConfig.publicAssets?.map(asset =>
        asset && asset.baseURL === '/_nuxt/' ? { ...asset, fallthrough: true } : asset
      );
      // Bundle only this build's CSS as server assets; no runtime reads of another release.
      nitroConfig.serverAssets ||= [];
      nitroConfig.serverAssets.push({
        baseName: 'site-css',
        dir: resolve(nitroConfig.buildDir!, 'dist/client/_nuxt'),
        pattern: '*.css'
      });
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
