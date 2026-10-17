import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { access, readdir, readFile } from 'node:fs/promises';
import { createServer } from 'node:http';
import { createServer as createPortReservation } from 'node:net';
import { fileURLToPath } from 'node:url';
import { test } from 'node:test';

// Run after `pnpm --filter @opennavo/web build`. Exercise the real production
// renderer against synthetic API data, without a browser or production services.
test('SSR preserves literal @ in package routes and page-authored metadata', { timeout: 60000 }, async t => {
  const entry = fileURLToPath(new URL('../.output/server/index.mjs', import.meta.url));
  await access(entry);
  const requestedTokens = new Set();
  // Admin SEO settings (WP24) as served by GET /site/settings.
  const siteSettings = {
    revision: 7,
    identity: { name: 'OpenNavo', alternateNames: [], logoUrl: '', sameAs: [] },
    verification: { google: 'fixture-google-code', bing: 'FIXTUREBINGCODE' },
    analytics: { ga4Enabled: true, ga4MeasurementId: 'G-FIXTURE1', consentRegions: ['DE', 'GB'] },
    edgeCache: { enabled: true },
    crawlers: {
      training: { mode: 'block', delaySeconds: 10, bots: ['GPTBot', 'ClaudeBot'] },
      seoTools: { mode: 'limit', delaySeconds: 20, bots: ['AhrefsBot'] }
    },
    pages: { titleTemplate: 'new', indexPolicy: { mode: 'enforce', scope: 'all', since: '' }, versionsSitemap: true }
  };
  // Index decisions per fixture app (WP11); versioned-app@11 mimics an older API without them.
  const indexing = {
    'ordinary-app': { indexable: true, reason: null, locales: ['en-US', 'zh-CN'], enforced: true, notesCount: 5 },
    'thin-app': { indexable: false, reason: 'missing_summary', locales: [], enforced: true, notesCount: 0 }
  };
  // WP9/WP10: an app with verified price, a disputed macOS supplement, an alternative and --zap files.
  const factsApp = {
    displayName: 'Facts App',
    names: ['Facts App'],
    autoUpdates: true,
    homepage: 'https://vendor.example/',
    downloadUrl: 'https://vendor.example/FactsApp.dmg',
    downloadSha256: 'b'.repeat(64),
    primaryCategory: { slug: 'developer-tools', name: 'Developer Tools', icon: 'lucide:code' },
    dependsOn: { macos: '>= 13', arch: [], formulae: [], casks: [], requirements: { macos: { '>=': ['13'] } } },
    artifacts: {
      apps: ['Facts App.app'],
      pkgs: [],
      binaries: [],
      uninstallNotes: [],
      entries: [
        {
          type: 'zap',
          phase: 'cleanup',
          sources: [],
          target: null,
          declaration: { zap: [{ trash: ['~/Library/Preferences/facts.plist'] }] }
        }
      ]
    },
    homebrewStatus: { state: 'active', reason: null, date: null },
    price: {
      type: 'subscription',
      amount: 9.99,
      currency: 'USD',
      region: 'US',
      plan: 'Pro',
      period: 'month',
      appVersion: '11.0',
      sourceUrl: 'https://vendor.example/pricing',
      method: 'vendor_site',
      verifiedAt: '2025-12-01T08:00:00Z',
      stale: false,
      offer: true
    },
    macosSupplement: {
      ranges: [{ arch: 'arm64', support: 'native', minimum: '12', maximum: null }],
      appVersion: '11.0',
      sourceUrl: 'https://vendor.example/requirements',
      method: 'vendor_docs',
      verifiedAt: '2025-12-01T08:00:00Z',
      stale: false,
      conflicts: ['minimum']
    },
    alternatives: [
      {
        package: {
          kind: 'cask',
          token: 'ordinary-app',
          name: 'Example App @11',
          displayName: 'Example App @11',
          summary: 'Synthetic package summary',
          iconUrl: null,
          accentColor: null,
          version: '11.0',
          primaryCategory: null,
          installs30d: 10,
          rank30d: 1,
          autoUpdates: false,
          deprecated: false,
          disabled: false,
          isFont: false,
          isLibrary: false,
          editorChoice: false,
          versionChangedAt: '2026-01-01T00:00:00Z'
        },
        reasons: ['open_source'],
        limits: ['fewer_features'],
        bestFor: 'developers'
      }
    ]
  };
  // WP17 articles: a signed roundup ready in English and Chinese, an editorial tutorial, a former
  // slug and a withdrawn article; thirteen in total so the list has a second page.
  const appSummary = (token, displayName) => ({
    kind: 'cask',
    token,
    name: displayName,
    displayName,
    summary: `${displayName} summary`,
    iconUrl: null,
    accentColor: null,
    version: '11.0',
    primaryCategory: null,
    installs30d: 1852,
    rank30d: 1,
    autoUpdates: false,
    deprecated: false,
    disabled: false,
    isFont: false,
    isLibrary: false,
    editorChoice: false,
    versionChangedAt: '2026-01-01T00:00:00Z'
  });
  const author = {
    slug: 'lin-wei',
    name: 'Lin Wei',
    role: 'Editor',
    bio: 'Tests Mac productivity apps.',
    avatarUrl: null,
    links: [{ title: 'Personal site', url: 'https://lin.example' }]
  };
  const roundupCard = zh => ({
    slug: 'choose-a-screenshot-app',
    type: 'roundup',
    title: zh ? '如何选择截图工具' : 'How to choose a screenshot app',
    summary: zh ? '比较什么。' : 'What to compare when picking a screenshot tool.',
    cover: null,
    author: { slug: 'lin-wei', name: 'Lin Wei' },
    publishedAt: '2026-10-12T10:00:00Z',
    updatedAt: '2026-10-14T10:00:00Z',
    readingMinutes: 6,
    locale: zh ? 'zh-CN' : 'en-US',
    machineTranslated: Boolean(zh)
  });
  const tutorialCard = {
    slug: 'manage-homebrew-without-terminal',
    type: 'tutorial',
    title: 'Manage Homebrew apps without the terminal',
    summary: null,
    cover: null,
    author: null,
    publishedAt: '2026-10-10T10:00:00Z',
    updatedAt: '2026-10-10T10:00:00Z',
    readingMinutes: 5,
    locale: 'en-US',
    machineTranslated: false
  };
  const roundup = zh => ({
    ...roundupCard(zh),
    bodyMarkdown: `## ${zh ? '怎么选' : 'What to compare'}\n\nPick by task.\n\n::app[ordinary-app]\n\n## ${zh ? '对比' : 'Side by side'}\n\n| App | Price |\n| --- | --- |\n| A | Free |\n\n::app[facts-app]\n\n::app[missing-app]`,
    testNote: 'MacBook Pro (M4), macOS 26.',
    seoTitle: null,
    seoDescription: null,
    sourceLocale: 'en-US',
    availableLocales: ['en-US', 'zh-CN'],
    author,
    sources: [{ title: 'Vendor docs', url: 'https://vendor.example/docs' }],
    packages: [appSummary('ordinary-app', 'Ordinary App'), appSummary('facts-app', 'Facts App')],
    images: [],
    related: [tutorialCard],
    revision: 2
  });
  let sitemapState = 'ok';
  let articleSitemapState = 'ok';
  // Changes what ordinary-app's description says, to see whether a page came from the cache.
  let revision = 1;
  let collectionState = 'empty';
  let collectionRequests = 0;
  const packageQueries = [];
  // App list total, so a cached list page can be made to run past its end.
  let packagesTotal = 50;
  let sitemapPackageRequests = 0;
  const listCapabilities = [];
  const api = createServer((req, res) => {
    const path = decodeURIComponent(new URL(req.url, 'http://localhost').pathname);
    const match = /^\/api\/v1\/packages\/cask\/([^/]+)(\/related)?$/.exec(path);
    const linkMatch = /^\/api\/v1\/packages\/link\/([^/]+)(\/related)?$/.exec(path);
    const query = new URL(req.url, 'http://localhost').searchParams;
    let data;
    let status = 200;
    const lifecycle = /^\/api\/v1\/packages\/cask\/([^/]+)\/lifecycle$/.exec(path)?.[1];
    if (lifecycle === 'old-token') {
      data = { state: 'renamed', token: 'ordinary-app' };
    } else if (lifecycle === 'gone-app') {
      data = {
        state: 'removed',
        token: 'gone-app',
        removed: {
          token: 'gone-app',
          displayName: 'Gone App',
          summary: 'Archived utility',
          iconUrl: null,
          lastVersion: '2.4.1',
          removedAt: '2026-09-01T08:00:00Z',
          replacement: null,
          alternatives: [{ kind: 'cask', token: 'ordinary-app', displayName: 'Ordinary App', summary: 'Still here' }],
          categories: [{ slug: 'utilities', name: 'Utilities', icon: 'lucide:wrench' }]
        }
      };
    } else if (lifecycle === 'flaky-app') {
      status = 503;
    } else if (linkMatch?.[1] === 'lumen-notes') {
      // Link app (store/security plan §4): no Homebrew data, only the developer's download addresses.
      data = linkMatch[2]
        ? []
        : {
            id: 2,
            kind: 'link',
            token: 'lumen-notes',
            name: 'Lumen Notes',
            displayName: 'Lumen Notes',
            names: ['Lumen Notes'],
            isFont: false,
            summary: 'Local-first Markdown notes',
            description: 'A synthetic link app for SSR regression tests.',
            version: '2.4.1',
            versionChangedAt: '2026-10-13T00:00:00Z',
            tap: '',
            installCommand: '',
            homepage: 'https://lumen.app/',
            artifacts: { apps: [], pkgs: [], binaries: [], uninstallNotes: [] },
            installs: { d30: 0, d90: 0, d365: 0 },
            categories: [],
            tags: [],
            conflictsWith: { casks: [], formulae: [] },
            supports: { status: 'unknown', arm64: false, x86_64: false },
            platforms: [],
            screenshots: [],
            releaseCount: 0,
            releaseStats: { count30d: 0, cadence: 'unknown', brewLag: null },
            sourceUrl: 'https://github.com/lumen-labs/lumen',
            formulaeUrl: 'https://github.com/lumen-labs/lumen',
            sourceLocale: 'en-US',
            machineTranslated: false,
            bundleIds: ['app.lumen.notes'],
            link: {
              homepage: 'https://lumen.app/',
              downloadUrl: 'https://lumen.app/download',
              github: {
                repo: 'lumen-labs/lumen',
                latest: {
                  tag: 'v2.4.1',
                  version: '2.4.1',
                  publishedAt: '2026-10-13T00:00:00Z',
                  url: 'https://github.com/lumen-labs/lumen/releases/tag/v2.4.1',
                  assets: [
                    {
                      name: 'Lumen-2.4.1-arm64.dmg',
                      url: 'https://github.com/lumen-labs/lumen/releases/download/v2.4.1/Lumen-2.4.1-arm64.dmg',
                      size: 50000000,
                      arch: 'arm64',
                      format: 'dmg',
                      sha256: null
                    },
                    {
                      name: 'Lumen-2.4.1.dmg',
                      url: 'javascript:alert(1)',
                      size: 1,
                      arch: 'x86_64',
                      format: 'dmg',
                      sha256: null
                    }
                  ]
                }
              },
              teamId: '7Q2XK4M9PL',
              pricing: 'open_source',
              notes: null,
              adoptedByCask: null
            }
          };
    } else if (path === '/api/v1/packages') {
      packageQueries.push(Object.fromEntries(query));
      listCapabilities.push(req.headers['x-opennavo-caps'] ?? '');
      const summary = {
        kind: 'cask',
        token: 'ordinary-app',
        name: 'Example App @11',
        displayName: 'Example App @11',
        summary: 'Synthetic package summary',
        iconUrl: null,
        accentColor: null,
        version: '11.0',
        primaryCategory: null,
        installs30d: 10,
        rank30d: 1,
        autoUpdates: false,
        deprecated: false,
        disabled: false,
        isFont: false,
        isLibrary: false,
        editorChoice: false,
        versionChangedAt: '2026-01-01T00:00:00Z'
      };
      data = {
        current: Number(query.get('current') ?? 1),
        size: Number(query.get('size') ?? 24),
        total: query.get('includeDisabled') === 'true' ? 100 : packagesTotal,
        records: [summary]
      };
    } else if (path === '/api/v1/rankings') {
      const current = Number(query.get('current') ?? 1);
      const size = Number(query.get('size') ?? 50);
      data = {
        current,
        size,
        total: 120,
        records: [
          {
            rank: (current - 1) * size + 1,
            rankChange: 0,
            package: appSummary('ordinary-app', 'Ordinary App'),
            installs: 1852
          }
        ]
      };
    } else if (path === '/api/v1/home') {
      data = {
        features: [],
        categories: [],
        popularApps: [appSummary('ordinary-app', 'Ordinary App')],
        popularCli: [],
        recentlyUpdated: [appSummary('facts-app', 'Facts App')],
        collections: [],
        stats: { casks: 50, formulae: 0, updatedLast24h: 3 }
      };
    } else if (path === '/api/v1/packages/cask/plus-app++/releases') {
      const original = query.get('original') === 'true';
      const locale = query.get('locale');
      const notesCount = original ? 2 : locale === 'zh-CN' ? 1 : 3;
      const total = query.get('onlyWithNotes') === 'true' ? notesCount : 3;
      const size = Number(query.get('size') ?? 20);
      data = {
        current: 1,
        size,
        total,
        records: Array.from({ length: Math.min(total, size) }, (_, index) => ({
          id: null,
          version: `1.${index}.0`,
          title: null,
          publishedAt: '2026-10-01T00:00:00Z',
          brewCommittedAt: null,
          source: 'github_release',
          sourceUrl: 'https://example.test/releases',
          isLatest: index === 0,
          isPrerelease: false,
          hasNotes: true,
          summary: null,
          sections: [],
          bodyMarkdown: 'Release fixture notes',
          translation: { status: 'manual', locale: locale ?? 'en-US' }
        })),
        stats: {
          count30d: 3,
          cadence: 'irregular',
          brewLag: { medianMinutes: null, earlierCount: 0, compared: 0 }
        }
      };
    } else if (path === '/api/v1/packages/link/lumen-notes/releases') {
      data = {
        current: 1,
        size: Number(query.get('size') ?? 20),
        total: 1,
        records: [
          {
            id: null,
            version: '2.4.1',
            title: null,
            publishedAt: '2026-10-13T00:00:00Z',
            brewCommittedAt: null,
            source: 'github_release',
            sourceUrl: 'https://github.com/lumen-labs/lumen/releases/tag/v2.4.1',
            isLatest: true,
            isPrerelease: false,
            hasNotes: true,
            summary: null,
            sections: [],
            bodyMarkdown: 'Lumen link release notes',
            translation: { status: 'original', locale: 'en-US' }
          }
        ],
        stats: { count30d: 1, cadence: 'irregular', brewLag: { medianMinutes: null, earlierCount: 0, compared: 0 } }
      };
    } else if (path === '/api/v1/desktop/releases/latest') {
      data = {
        version: '0.3.0',
        channel: 'stable',
        pubDate: '2026-09-30T08:00:00Z',
        notes: 'Improve stability.',
        minMacos: '13.0',
        downloads: [
          { target: 'dmg-universal', url: 'https://example.test/OpenNavo.dmg', bytes: 1000, sha256: 'a'.repeat(64) }
        ]
      };
    } else if (
      match &&
      ['versioned-app@11', 'plus-app++', 'ordinary-app', 'thin-app', 'font-sample', 'facts-app', 'wechat'].includes(
        match[1]
      )
    ) {
      const token = match[1];
      requestedTokens.add(token);
      // WeChat's catalog names already name the platform, and its summary is long (audit, 2026-10-17).
      const zh = req.headers['accept-language'] === 'zh-CN';
      const wechat =
        token === 'wechat'
          ? {
              displayName: zh ? '微信 Mac 版' : 'WeChat for Mac',
              names: ['WeChat for Mac'],
              summary: zh
                ? '腾讯的 Mac 即时通信客户端，可与 WeChat 联系人收发文字消息和文件，以及进行语音和视频通话。'
                : 'Tencent’s Mac messaging client for text, files, voice and video calls with your WeChat contacts.'
            }
          : {};
      data = match[2]
        ? []
        : {
            id: 1,
            kind: 'cask',
            token,
            displayName: token === 'font-sample' ? 'Sample Mono' : 'Example App @11',
            names: [token === 'font-sample' ? 'Sample Mono' : 'Example App @11'],
            isFont: token === 'font-sample',
            summary: 'Synthetic package summary',
            description:
              token === 'ordinary-app'
                ? `Revision ${revision} of a synthetic package.`
                : 'A synthetic package for SSR regression tests.',
            version: '11.0',
            versionChangedAt: '2026-01-01T00:00:00Z',
            tap: 'homebrew/cask',
            installCommand: `brew install --cask ${token}`,
            artifacts: { apps: ['Example.app'], pkgs: [], binaries: [], uninstallNotes: [] },
            installs: { d30: 10, d90: 20, d365: 30 },
            categories: [],
            tags: [],
            conflictsWith: { casks: [], formulae: [] },
            supports: { status: 'known', arm64: true, x86_64: true },
            platforms: [],
            screenshots: [],
            releaseCount: 0,
            releaseStats: { count30d: 0, cadence: 'unknown', brewLag: null },
            sourceUrl: 'https://example.test/source',
            formulaeUrl: 'https://example.test/package',
            sourceLocale: 'en-US',
            machineTranslated: false,
            ...(indexing[token] ? { indexing: indexing[token] } : {}),
            ...(token === 'ordinary-app' ? { articles: [roundupCard(false)] } : {}),
            ...(token === 'facts-app' ? factsApp : {}),
            ...wechat
          };
    } else if (path === '/api/v1/sitemap/packages') {
      sitemapPackageRequests++;
      status = sitemapState === 'error' ? 503 : 200;
      const records =
        query.get('group') === 'fonts'
          ? []
          : [
              {
                kind: 'cask',
                token: 'ordinary-app',
                updatedAt: '2026-10-07T00:00:00Z',
                isFont: false,
                notesCount: 5,
                locales: [
                  { locale: 'en-US', lastmod: '2025-03-18T10:00:00Z' },
                  { locale: 'zh-CN', lastmod: null }
                ]
              },
              // Tokens with + (nextpad++, logi-options+) must keep it in sitemap URLs.
              {
                kind: 'cask',
                token: 'plus-app++',
                updatedAt: '2026-10-07T00:00:00Z',
                isFont: false,
                notesCount: 3,
                locales: [
                  { locale: 'en-US', lastmod: '2026-10-01T10:00:00Z' },
                  { locale: 'zh-CN', lastmod: '2026-10-02T10:00:00Z' }
                ]
              }
            ];
      data = status === 200 ? { current: 1, size: 5000, total: records.length, records } : null;
    } else if (path === '/api/v1/categories') {
      data = [
        {
          slug: 'utilities',
          name: 'Utilities',
          description: 'Small tools for everyday Mac tasks',
          icon: 'lucide:wrench',
          appliesTo: 'both',
          hiddenByDefault: false,
          children: [],
          count: 1
        }
      ];
    } else if (path === '/api/v1/articles') {
      const current = Number(query.get('current') ?? 1);
      const zh = (query.get('locale') ?? req.headers['accept-language']) === 'zh-CN';
      const all = query.get('type') === 'tutorial' ? [tutorialCard] : [roundupCard(zh), tutorialCard];
      const total = query.get('type') ? all.length : 13;
      data = { current, size: 12, total, records: current === 1 ? all : current === 2 ? [tutorialCard] : [] };
    } else if (path === '/api/v1/articles/choose-a-screenshot-app') {
      data = {
        state: 'published',
        slug: 'choose-a-screenshot-app',
        article: roundup((query.get('locale') ?? req.headers['accept-language']) === 'zh-CN')
      };
    } else if (path === '/api/v1/articles/old-slug') {
      data = { state: 'moved', slug: 'choose-a-screenshot-app' };
    } else if (path === '/api/v1/articles/pulled-article') {
      data = { state: 'withdrawn', slug: 'pulled-article' };
    } else if (path === '/api/v1/authors/lin-wei') {
      data = { author, articles: { current: 1, size: 12, total: 1, records: [roundupCard(false)] } };
    } else if (path === '/api/v1/sitemap/articles') {
      status = articleSitemapState === 'error' ? 503 : 200;
      data = {
        articles: [
          {
            slug: 'choose-a-screenshot-app',
            locales: [
              { locale: 'en-US', lastmod: '2026-10-14T10:00:00Z' },
              { locale: 'zh-CN', lastmod: '2026-10-15T10:00:00Z' }
            ]
          }
        ],
        authors: [{ slug: 'lin-wei', lastmod: '2026-10-14T10:00:00Z' }]
      };
      if (articleSitemapState === 'empty') data = { articles: [], authors: [] };
      if (articleSitemapState === 'no-authors') data.authors = [];
    } else if (path === '/api/v1/collections/editors') {
      data = {
        slug: 'editors',
        title: 'Code editors that start in under a second',
        subtitle: 'Lightweight picks',
        body: null,
        coverUrl: null,
        itemCount: 0,
        updatedAt: '2026-10-01T00:00:00Z',
        sourceLocale: 'en-US',
        machineTranslated: false,
        items: []
      };
    } else if (path === '/api/v1/site/settings') {
      data = siteSettings;
    } else if (path === '/api/v1/site/indexnow-keys/fixture-indexnow-key-0001') {
      data = { key: 'fixture-indexnow-key-0001' };
    } else if (path === '/api/v1/collections') {
      collectionRequests++;
      status = collectionState === 'error' ? 503 : 200;
      const records =
        collectionState === 'published'
          ? [
              {
                slug: 'recovery-check',
                title: 'Collection recovery marker',
                subtitle: 'Fixture',
                itemCount: 0,
                iconUrls: [],
                previewItems: [],
                sourceLocale: 'en-US',
                updatedAt: '2026-01-01T00:00:00Z'
              }
            ]
          : [];
      data = status === 200 ? { current: 1, size: 24, total: records.length, records } : null;
    } else {
      status = 404;
      data = null;
    }
    res.writeHead(status, { 'content-type': 'application/json' });
    res.end(JSON.stringify({ code: status === 200 ? '0000' : '4040', msg: 'fixture', data }));
  });
  api.listen(0, '127.0.0.1');
  await once(api, 'listening');
  t.after(() => new Promise(resolve => api.close(resolve)));
  const apiBase = `http://127.0.0.1:${api.address().port}/api/v1`;
  // Nitro treats PORT=0 as its default port; reserve a free local port instead.
  const reservation = createPortReservation();
  reservation.listen(0, '127.0.0.1');
  await once(reservation, 'listening');
  const port = String(reservation.address().port);
  await new Promise(resolve => reservation.close(resolve));
  const internalReservation = createPortReservation();
  internalReservation.listen(0, '127.0.0.1');
  await once(internalReservation, 'listening');
  const internalPort = String(internalReservation.address().port);
  await new Promise(resolve => internalReservation.close(resolve));
  const internal = `http://127.0.0.1:${internalPort}`;
  const internalSecret = 'test-only-internal-secret-'.repeat(2);
  // Test-only clock: SIGUSR2 moves the server's Date.now() an hour ahead, so cached pages can expire
  // without waiting for their TTL.
  const clock = `data:text/javascript,${encodeURIComponent(
    'const now = Date.now.bind(Date); let offset = 0; process.on("SIGUSR2", () => { offset += 3600000; }); Date.now = () => now() + offset;'
  )}`;
  const child = spawn(process.execPath, ['--import', clock, entry], {
    env: {
      ...process.env,
      NODE_ENV: 'production',
      HOST: '127.0.0.1',
      PORT: port,
      NITRO_HOST: '127.0.0.1',
      NITRO_PORT: port,
      NITRO_UNIX_SOCKET: '',
      NUXT_API_BASE_INTERNAL: apiBase,
      NUXT_PUBLIC_API_BASE: apiBase,
      NUXT_REDIS_URL: '',
      NUXT_PUBLIC_SITE_URL: 'https://example.test',
      NUXT_PUBLIC_I18N_BASE_URL: 'https://example.test',
      NUXT_PUBLIC_CDN_BASE: 'https://example.test/assets',
      NUXT_METRICS_PORT: internalPort,
      NUXT_INTERNAL_SECRET: internalSecret
    },
    stdio: ['ignore', 'pipe', 'pipe']
  });
  let logs = '';
  const exited = once(child, 'exit');
  child.stderr.on('data', chunk => {
    logs += chunk;
  });
  t.after(async () => {
    child.kill('SIGTERM');
    const timeout = setTimeout(() => child.kill('SIGKILL'), 5000);
    try {
      await exited;
    } finally {
      clearTimeout(timeout);
    }
  });
  const origin = await new Promise((resolve, reject) => {
    const timeout = setTimeout(() => reject(new Error(`SSR did not start: ${logs}`)), 15000);
    child.once('error', error => {
      clearTimeout(timeout);
      reject(error);
    });
    child.once('exit', () => {
      clearTimeout(timeout);
      reject(new Error(`SSR exited: ${logs}`));
    });
    child.stdout.on('data', chunk => {
      logs += chunk;
      const url = /Listening on (http:\/\/127\.0\.0\.1:\d+)/.exec(logs)?.[1];
      if (url) {
        clearTimeout(timeout);
        resolve(url);
      }
    });
  });
  for (const prefix of ['', '/zh', '/ja', '/es', '/pt', '/ru']) {
    await t.test(`${prefix || '/en (root)'} versioned detail`, async () => {
      const path = `${prefix}/apps/versioned-app@11`;
      const response = await fetch(`${origin}${path}`, { headers: { accept: 'text/html' } });
      const html = await response.text();
      assert.equal(response.status, 200, `${path}\n${logs}`);
      assert.match(html, /<title>[^<]*Example App @11[^<]*OpenNavo<\/title>/);
      assert.match(html, /<h1\b[^>]*>Example App @11<\/h1>/);
      // The visible command block wraps the token onto a continuation line.
      assert.match(html, /brew install --cask \\<\/span>[\s\S]{0,200}?versioned-app@11<\/span>/);
      assert.ok(html.includes(`https://example.test${path}`), 'canonical URL retains the version suffix');
      // WP1: the description says what the page answers instead of stacking the install command.
      const description = /<meta name="description" content="([^"]*)"/.exec(html)?.[1] ?? '';
      assert.ok(description.includes('Example App @11'), description);
      assert.ok(!description.includes('brew install'), description);
      if (prefix === '/zh') assert.match(html, /<title>Example App @11 Mac 版下载安装 · OpenNavo<\/title>/);
      if (prefix === '')
        assert.match(html, /<title>Example App @11 for Mac – Download &amp; Install · OpenNavo<\/title>/);
    });
  }
  await t.test('link apps share /apps and download from the developer instead of installing', async () => {
    const response = await fetch(`${origin}/apps/lumen-notes`, { headers: { accept: 'text/html' } });
    const html = await response.text();
    assert.equal(response.status, 200, logs);
    assert.match(html, /<h1\b[^>]*>Lumen Notes<\/h1>/);
    assert.doesNotMatch(html, /brew install/, 'no install command');
    // Header and panel point at the developer's own addresses, in a new tab.
    assert.match(html, /<a\b[^>]*href="https:\/\/lumen\.app\/download"[^>]*target="_blank"/);
    assert.ok(html.includes('Lumen-2.4.1-arm64.dmg'), 'release files are listed');
    assert.ok(!html.includes('javascript:alert'), 'only https files become links');
    assert.ok(html.includes('OpenNavo hosts no installers'), 'the page says nothing is hosted');
    // JSON-LD names the official download; tabs without Homebrew data are absent and 404.
    assert.match(html, /"downloadUrl":"https:\/\/lumen\.app\/download"/);
    assert.ok(!html.includes('/apps/lumen-notes/dependencies'), 'no dependencies tab');
    assert.ok(!html.includes('/apps/lumen-notes/details'), 'no install details tab');
    for (const tab of ['dependencies', 'details'])
      assert.equal((await fetch(`${origin}/apps/lumen-notes/${tab}`)).status, 404, tab);
    // Version history comes from the link app's own releases.
    const versions = await fetch(`${origin}/apps/lumen-notes/versions`);
    const versionsHtml = await versions.text();
    assert.equal(versions.status, 200, logs);
    assert.ok(versionsHtml.includes('Lumen link release notes'));
    assert.ok(!versionsHtml.includes('Added to Homebrew'), 'no Homebrew adoption lag for link apps');
  });

  await t.test('release label totals match locale and original mode', async () => {
    for (const [prefix, original, count, label, locale] of [
      ['/zh', false, 1, '有更新说明', 'zh-CN'],
      ['/zh', true, 2, '有更新说明', 'zh-CN'],
      ['', false, 3, 'With notes', 'en-US'],
      ['', true, 2, 'With notes', 'en-US']
    ]) {
      const response = await fetch(`${origin}${prefix}/apps/plus-app++/versions?notes=1&original=${original ? 1 : 0}`);
      const html = await response.text();
      assert.equal(response.status, 200, logs);
      assert.ok(html.includes(`${label} ${count}`), `${prefix} original=${original}: label count`);
      assert.equal((html.match(/Release fixture notes<\/p>/g) ?? []).length, count);
      assert.ok(
        html.includes(`releases-count:cask:plus-app++:${locale}:${original}`),
        'cache key includes language mode'
      );
    }
  });
  await t.test('encoded package tokens preserve path semantics in SSR and payload requests', async () => {
    const redirect = await fetch(`${origin}/zh/apps/plus-app%2B%2B/versions?source=a%2Bb`, { redirect: 'manual' });
    assert.equal(redirect.status, 301);
    assert.equal(redirect.headers.get('location'), '/zh/apps/plus-app++/versions?source=a%2Bb');
    for (const path of ['/apps/plus-app%2B%2B', '/zh/apps/plus-app%2b%2b/versions', '/apps/versioned-app%4011']) {
      const response = await fetch(`${origin}${path}`);
      assert.equal(response.status, 200, `${path}\n${logs}`);
      const html = await response.text();
      assert.ok(html.includes(path.includes('plus-app') ? 'plus-app++' : 'versioned-app@11'));
    }
    const payload = await fetch(`${origin}/ja/apps/plus-app%2B%2B/versions/_payload.json`);
    assert.equal(payload.status, 200);
    for (const token of ['plus-app%252B%252B', 'bad%20token', 'bad%2Ftoken', 'bad%ZZ']) {
      assert.equal((await fetch(`${origin}/apps/${token}`)).status, 404, token);
    }
  });
  for (const path of ['/apps/ordinary-app', '/apps/versioned-app@11/details']) {
    await t.test(path, async () => {
      const response = await fetch(`${origin}${path}`, { headers: { accept: 'text/html' } });
      assert.equal(response.status, 200, logs);
      assert.match(await response.text(), /<title>[^<]*Example App @11[^<]*OpenNavo<\/title>/);
    });
  }
  await t.test('unknown versioned package remains a 404', async () => {
    const response = await fetch(`${origin}/apps/missing-app@0`, { headers: { accept: 'text/html' } });
    assert.equal(response.status, 404, logs);
    assert.match(await response.text(), /<title>[^<]+<\/title>/);
  });
  for (const path of ['/collections', '/zh/collections', '/zh/collections/']) {
    await t.test(`${path} does not retain empty or failed responses`, async () => {
      for (const state of ['empty', 'published', 'error', 'published']) {
        collectionState = state;
        const before = collectionRequests;
        const response = await fetch(`${origin}${path}`);
        const html = await response.text();
        assert.equal(response.headers.get('cache-control'), 'no-store');
        assert.equal(response.status, state === 'error' ? 503 : 200);
        if (state === 'error') assert.equal(response.headers.get('retry-after'), '5');
        assert.ok(collectionRequests > before, 'each render must recheck current collection data');
        assert.equal(html.includes('Collection recovery marker'), state === 'published');
      }
    });
  }
  await t.test('structured data has one identity and breadcrumbs that match the page', async () => {
    const html = await (await fetch(`${origin}/apps/ordinary-app`, { headers: { accept: 'text/html' } })).text();
    const graph = JSON.parse(/<script type="application\/ld\+json"[^>]*>([\s\S]*?)<\/script>/.exec(html)?.[1] ?? '{}')[
      '@graph'
    ];
    const nodes = type => graph.filter(node => [node['@type']].flat().includes(type));
    assert.equal(nodes('WebSite').length, 1);
    assert.equal(nodes('Organization').length, 1);
    const [organization] = nodes('Organization');
    assert.equal(organization['@id'], 'https://example.test/#identity');
    assert.equal(nodes('WebSite')[0].publisher['@id'], organization['@id']);
    assert.deepEqual(
      nodes('BreadcrumbList')[0].itemListElement.map(item => item.name),
      ['Discover', 'Example App @11'],
      'JSON-LD follows the visible breadcrumb (no primary category in the fixture)'
    );
    assert.ok(!html.includes('"alternateName":[]'));
    const footer = /<footer[\s\S]*?<\/footer>/.exec(html)?.[0] ?? '';
    for (const path of ['/categories', '/rankings', '/recent', '/collections', '/download', '/about'])
      assert.ok(footer.includes(`href="${path}"`), path);
    const github = /<a[^>]*href="https:\/\/github\.com\/opennavo\/OpenNavo"[^>]*>/.exec(footer)?.[0] ?? '';
    assert.match(github, /target="_blank"/, 'the GitHub link opens in a new tab');
    assert.match(github, /rel="noopener noreferrer"/);
  });
  await t.test('GA4 never loads from shared HTML and asks consent by region', async () => {
    const html = await (await fetch(`${origin}/apps/ordinary-app`, { headers: { accept: 'text/html' } })).text();
    assert.ok(!html.includes('googletagmanager'), 'GA4 loads only on the client after the consent check');
    assert.ok(/<footer[\s\S]*Analytics settings[\s\S]*<\/footer>/.test(html));
    for (const [country, expected] of [
      ['DE', true],
      ['US', false],
      [undefined, true],
      ['XX', true]
    ]) {
      const response = await fetch(`${origin}/api/consent-region`, {
        headers: country ? { 'cf-ipcountry': country } : {}
      });
      assert.equal(response.status, 200, logs);
      assert.equal(response.headers.get('cache-control'), 'private, no-store');
      assert.deepEqual(await response.json(), { requireConsent: expected }, String(country));
    }
  });
  await t.test('index decisions reach robots meta and hreflang', async () => {
    const html = async path => (await fetch(`${origin}${path}`, { headers: { accept: 'text/html' } })).text();
    const robots = page => [...page.matchAll(/<meta name="robots" content="([^"]*)"/g)].map(m => m[1]);
    assert.deepEqual(robots(await html('/apps/thin-app')), ['noindex, follow']);
    const overview = await html('/apps/ordinary-app');
    assert.ok(!robots(overview).some(value => value.includes('noindex')), robots(overview).join());
    const hreflangs = [...overview.matchAll(/<link[^>]+hreflang="([^"]+)"/g)].map(m => m[1]).sort();
    assert.deepEqual(hreflangs, ['en', 'x-default', 'zh', 'zh-Hans']);
    assert.deepEqual(robots(await html('/ja/apps/ordinary-app')), ['noindex, follow']);
    assert.deepEqual(robots(await html('/apps/ordinary-app/details')), ['noindex, follow']);
    // An older API without index decisions keeps every language.
    const legacy = await html('/apps/versioned-app@11');
    assert.equal([...legacy.matchAll(/<link[^>]+hreflang="/g)].length, 9);
  });
  await t.test('robots response headers match final page directives, including cached responses', async () => {
    for (const path of [
      '/apps/thin-app',
      '/ja/apps/ordinary-app',
      '/apps/ordinary-app/details',
      '/apps/gone-app',
      '/search?q=example',
      '/articles?type=roundup',
      '/ja/articles/choose-a-screenshot-app',
      '/articles/pulled-article'
    ]) {
      for (const agent of ['Googlebot', 'Bingbot']) {
        const response = await fetch(`${origin}${path}`, {
          headers: { accept: 'text/html', 'user-agent': agent }
        });
        const html = await response.text();
        const directives = [...html.matchAll(/<meta name="robots" content="([^"]*)"/g)].map(m => m[1]);
        assert.deepEqual(directives, ['noindex, follow'], `${path}: ${agent}`);
        assert.equal(response.headers.get('x-robots-tag'), directives[0], `${path}: ${agent}`);
      }
      const head = await fetch(`${origin}${path}`, { method: 'HEAD' });
      assert.equal(head.headers.get('x-robots-tag'), 'noindex, follow', `${path}: HEAD`);
    }
  });
  await t.test('sitemaps fail loudly and list grouped URLs with per-language lastmod', async () => {
    sitemapState = 'error';
    const failed = await fetch(`${origin}/__sitemap__/fonts-bucket-0.xml`);
    assert.ok(failed.status >= 500, `a failed API must not render an empty sitemap (${failed.status})`);
    sitemapState = 'ok';
    const index = await (await fetch(`${origin}/sitemap_index.xml`)).text();
    // static is split per language by the i18n integration; the large groups shard.
    for (const name of ['en-static', 'zh-Hans-static', 'categories', 'collections'])
      assert.ok(index.includes(`/__sitemap__/${name}.xml`), name);
    const appsUrls = [...index.matchAll(/<loc>([^<]*\/__sitemap__\/apps-bucket-\d+\.xml)<\/loc>/g)].map(
      match => match[1]
    );
    assert.ok(appsUrls.length > 0, index);
    const apps = (
      await Promise.all(appsUrls.map(async url => (await fetch(url.replace('https://example.test', origin))).text()))
    ).join('');
    const urlEntry = loc => new RegExp(`<url>\\s*<loc>${loc}</loc>([\\s\\S]*?)</url>`).exec(apps)?.[1];
    assert.match(urlEntry('https://example.test/apps/ordinary-app') ?? '', /<lastmod>2025-03-18/);
    assert.ok(!/<lastmod>/.test(urlEntry('https://example.test/zh/apps/ordinary-app') ?? 'missing <lastmod>'));
    assert.ok(!apps.includes('/ja/apps/ordinary-app'), 'unpublished languages stay out');
    assert.ok(!apps.includes('2026-10-07'), 'sync times never become lastmod');
    const versionsUrls = [...index.matchAll(/<loc>([^<]*\/__sitemap__\/versions-bucket-\d+\.xml)<\/loc>/g)].map(
      match => match[1]
    );
    const versions = (
      await Promise.all(
        versionsUrls.map(async url => (await fetch(url.replace('https://example.test', origin))).text())
      )
    ).join('');
    assert.ok(versions.includes('https://example.test/apps/ordinary-app/versions'));
    // Tokens keep their + in every loc, as page canonicals and hreflang spell them, so no loc redirects.
    assert.ok(apps.includes('<loc>https://example.test/apps/plus-app++</loc>'), apps);
    assert.ok(apps.includes('<loc>https://example.test/zh/apps/plus-app++</loc>'), apps);
    assert.ok(apps.includes('hreflang="zh-Hans" href="https://example.test/zh/apps/plus-app++"'), apps);
    assert.ok(versions.includes('<loc>https://example.test/apps/plus-app++/versions</loc>'), versions);
    assert.ok(!/<loc>[^<]*%2B/i.test(apps + versions), 'no escaped + in any loc');
    for (const loc of ['/apps/plus-app++', '/zh/apps/plus-app++', '/apps/plus-app++/versions']) {
      const response = await fetch(`${origin}${loc}`, { redirect: 'manual', headers: { accept: 'text/html' } });
      await response.text();
      assert.equal(response.status, 200, `${loc} answers without a redirect\n${logs}`);
    }
    // The index keeps its list of populated buckets: a warm request reads no catalog.
    const reads = sitemapPackageRequests;
    const warm = await fetch(`${origin}/sitemap_index.xml`);
    assert.equal(warm.status, 200);
    assert.ok((await warm.text()).includes('/__sitemap__/apps-bucket-'));
    assert.equal(sitemapPackageRequests, reads, 'a warm index answers from its cached result');
  });
  await t.test('renamed, removed and unavailable apps get 301, 410 and 503', async () => {
    const moved = await fetch(`${origin}/apps/old-token`, { redirect: 'manual' });
    assert.equal(moved.status, 301, logs);
    assert.equal(new URL(moved.headers.get('location'), origin).pathname, '/apps/ordinary-app');
    const movedTab = await fetch(`${origin}/zh/apps/old-token/versions`, { redirect: 'manual' });
    assert.equal(movedTab.status, 301);
    assert.equal(new URL(movedTab.headers.get('location'), origin).pathname, '/zh/apps/ordinary-app/versions');
    const gone = await fetch(`${origin}/apps/gone-app`, { headers: { accept: 'text/html' } });
    const goneHtml = await gone.text();
    assert.equal(gone.status, 410, logs);
    assert.match(goneHtml, /<h1[^>]*>Gone App was removed from Homebrew<\/h1>/);
    assert.ok(goneHtml.includes('href="/apps/ordinary-app"'), 'alternatives link to available apps');
    assert.ok(goneHtml.includes('href="/categories/utilities"'));
    assert.match(goneHtml, /<meta name="robots" content="noindex, follow"/);
    // The payload keeps 200, or the client would drop it and refetch from the browser.
    assert.equal((await fetch(`${origin}/apps/gone-app/_payload.json`)).status, 200);
    assert.ok(!goneHtml.includes('brew install --cask gone-app'), 'no install command for removed apps');
    const flaky = await fetch(`${origin}/apps/flaky-app`, { headers: { accept: 'text/html' } });
    await flaky.text();
    assert.equal(flaky.status, 503);
    assert.equal(flaky.headers.get('cache-control'), 'no-store');
    assert.equal(flaky.headers.get('retry-after'), '5');
  });
  await t.test('the internal port refreshes cached pages for the URL change feed', async () => {
    const page = async () =>
      (await fetch(`${origin}/es/apps/ordinary-app`, { headers: { accept: 'text/html' } })).text();
    const invalidate = authorization =>
      fetch(`${internal}/cache/invalidate`, {
        method: 'POST',
        headers: { authorization, 'content-type': 'application/json' },
        body: JSON.stringify({ paths: ['/es/apps/ordinary-app'] })
      });
    revision = 1;
    const first = await page();
    assert.ok(first.includes('Revision 1'), /About[\s\S]{0,400}/.exec(first)?.[0] ?? first.slice(0, 300));
    revision = 2;
    const second = await page();
    assert.ok(second.includes('Revision 1'), /About[\s\S]{0,400}/.exec(second)?.[0] ?? 'served from the SWR cache');
    assert.equal((await invalidate('Bearer wrong-secret')).status, 401);
    const accepted = await invalidate(`Bearer ${internalSecret}`);
    assert.equal(accepted.status, 200);
    assert.ok((await accepted.json()).removed >= 1);
    assert.ok((await page()).includes('Revision 2'), 'the next request renders fresh data');
    const probe = await fetch(`${internal}/cache/probe`, {
      method: 'POST',
      headers: { authorization: `Bearer ${internalSecret}`, 'content-type': 'application/json' },
      body: JSON.stringify({ paths: ['/apps/ordinary-app', '/apps/old-token', '/apps/missing-app@0'] })
    });
    assert.equal(probe.status, 200);
    assert.deepEqual((await probe.json()).statuses, {
      '/apps/ordinary-app': 200,
      '/apps/old-token': 301,
      '/apps/missing-app@0': 404
    });
    const unauthorizedProbe = await fetch(`${internal}/cache/probe`, { method: 'POST', body: '{"paths":["/"]}' });
    assert.equal(unauthorizedProbe.status, 401);
    // The release check: the build id the worker compares is the one pages are built with.
    assert.equal((await fetch(`${internal}/cache/build`)).status, 401);
    const build = await fetch(`${internal}/cache/build`, { headers: { authorization: `Bearer ${internalSecret}` } });
    assert.equal(build.status, 200);
    const { build: buildId } = await build.json();
    assert.match(buildId, /^[\w-]{8,}$/);
    assert.ok((await page()).includes(buildId), 'pages reference the reported build');
    const metrics = await (await fetch(`${internal}/metrics`)).text();
    assert.match(metrics, /opennavo_web_requests_total\{agent="browser",route="app",status="200"\} \d+/);
  });
  await t.test('only public cached pages may be cached at the edge', async () => {
    const edge = async path => {
      const response = await fetch(`${origin}${path}`, { headers: { accept: 'text/html' } });
      await response.text();
      return [response.status, response.headers.get('cloudflare-cdn-cache-control')];
    };
    // App and article pages are purged on change and after each release, so they stay for hours.
    assert.deepEqual(await edge('/apps/ordinary-app'), [200, 'max-age=21600, stale-while-revalidate=86400']);
    assert.deepEqual(await edge('/apps/ordinary-app/details'), [200, 'no-store']);
    // Search and collections send no-store themselves (search also fails here: no search fixture).
    assert.equal((await edge('/search?q=editor'))[1], 'no-store');
    assert.equal((await edge('/collections'))[1], 'no-store');
    assert.deepEqual(await edge('/apps/gone-app'), [410, 'no-store']);
    assert.deepEqual(await edge('/apps/missing-app@0'), [404, 'no-store']);
    // The category overview and the feedback form are cached at the origin like their neighbours.
    for (const path of ['/categories', '/zh/categories', '/feedback', '/ja/feedback?type=broken_link']) {
      const response = await fetch(`${origin}${path}`, { headers: { accept: 'text/html' } });
      await response.text();
      assert.equal(response.status, 200, `${path}\n${logs}`);
      assert.match(response.headers.get('cache-control') ?? '', /s-maxage=900/, path);
    }
    assert.deepEqual(await edge('/categories'), [200, 'max-age=600, stale-while-revalidate=3600']);
    // Recently Updated is a list like /apps, paginated pages included. (Later tests need /recent itself
    // uncached by the route cache, so these are other languages' URLs.)
    for (const path of ['/pt/recent', '/ru/recent?page=2', '/es/recent?page=2&size=12'])
      assert.deepEqual(await edge(path), [200, 'max-age=600, stale-while-revalidate=3600'], path);
    // GA4 page_type and the crawler counters still call it "other".
    const metrics = await (await fetch(`${internal}/metrics`)).text();
    assert.match(metrics, /opennavo_web_requests_total\{agent="browser",route="other",status="200"\} \d+/);
    assert.ok(!metrics.includes('route="recent"'), 'no new route label');
  });
  await t.test('guessed sitemap and icon addresses move permanently', async () => {
    for (const [path, target] of [
      ['/sitemap.xml', '/sitemap_index.xml'],
      ['/apple-touch-icon-precomposed.png', '/apple-touch-icon.png'],
      ['/img/logo_512.png', '/icon-512.png']
    ]) {
      const response = await fetch(`${origin}${path}`, { redirect: 'manual' });
      assert.equal(response.status, 301, path);
      assert.equal(new URL(response.headers.get('location') ?? '', origin).pathname, target, path);
      assert.equal((await fetch(`${origin}${target}`)).status, 200, target);
    }
  });
  await t.test('titles distinguish fonts, languages and pages of a list', async () => {
    const title = async path =>
      /<title>([^<]*)<\/title>/.exec(
        await (await fetch(`${origin}${path}`, { headers: { accept: 'text/html' } })).text()
      )?.[1];
    assert.equal(await title('/apps/font-sample'), 'Sample Mono Font for Mac – Download &amp; Install · OpenNavo');
    const titles = {
      '': 'Example App @11 for Mac – Download &amp; Install · OpenNavo',
      '/zh': 'Example App @11 Mac 版下载安装 · OpenNavo',
      '/ja': 'Example App @11 for Mac ダウンロード・インストール · OpenNavo',
      '/es': 'Descargar Example App @11 para Mac · OpenNavo',
      '/pt': 'Baixar Example App @11 para Mac · OpenNavo',
      '/ru': 'Example App @11 для Mac — скачать и установить · OpenNavo'
    };
    for (const [prefix, expected] of Object.entries(titles))
      assert.equal(await title(`${prefix}/apps/versioned-app@11`), expected);
    const paged = await (await fetch(`${origin}/apps?page=2`, { headers: { accept: 'text/html' } })).text();
    assert.match(paged, /<title>[^<]* · Page 2 · OpenNavo<\/title>/);
    assert.ok(
      paged.includes('rel="canonical" href="https://example.test/apps?page=2"'),
      'pagination keeps page in canonical'
    );
    const before = packageQueries.length;
    assert.ok(!(await title('/apps')).includes('Page'), 'the first page has no page number');
    // All apps mix in link apps: no kind filter, and the client says it understands them.
    assert.deepEqual(
      packageQueries.slice(before).map(({ kind }) => kind),
      [undefined]
    );
    assert.equal(listCapabilities.at(-1), 'link');
  });
  await t.test('app titles name the platform once and descriptions keep their closing sentence', async () => {
    const page = async path => {
      const response = await fetch(`${origin}${path}`, { headers: { accept: 'text/html' } });
      assert.equal(response.status, 200, `${path}\n${logs}`);
      return response.text();
    };
    const title = html => /<title>([^<]*)<\/title>/.exec(html)?.[1];
    const description = html => /<meta name="description" content="([^"]*)"/.exec(html)?.[1]?.replaceAll('&amp;', '&');
    for (const [prefix, expected] of [
      ['', 'WeChat for Mac – Download &amp; Install · OpenNavo'],
      ['/zh', '微信 Mac 版下载安装 · OpenNavo'],
      ['/ja', 'WeChat for Mac ダウンロード・インストール · OpenNavo'],
      ['/es', 'Descargar WeChat para Mac · OpenNavo'],
      ['/pt', 'Baixar WeChat para Mac · OpenNavo'],
      ['/ru', 'WeChat для Mac — скачать и установить · OpenNavo']
    ])
      assert.equal(title(await page(`${prefix}/apps/wechat`)), expected, prefix || '/');
    // The summary gives way, so search results show the whole closing sentence (about 160 columns).
    assert.equal(
      description(await page('/apps/wechat')),
      'Tencent’s Mac messaging client for text, files, voice… See how to install WeChat for Mac on your Mac, along with its system requirements and release history.'
    );
    // Chinese names join the Chinese text around them; this one fits whole.
    assert.equal(
      description(await page('/zh/apps/wechat')),
      '腾讯的 Mac 即时通信客户端，可与 WeChat 联系人收发文字消息和文件，以及进行语音和视频通话。查看微信 Mac 版在 Mac 上的安装方法、系统要求和版本记录。'
    );
  });
  await t.test('catalog headings and the categories description read well in every language', async () => {
    const page = async path => {
      const response = await fetch(`${origin}${path}`, { headers: { accept: 'text/html' } });
      assert.equal(response.status, 200, `${path}\n${logs}`);
      return response.text();
    };
    assert.match(await page('/ru/apps'), /<h1\b[^>]*>Приложения<\/h1>/);
    for (const [prefix, expected] of [
      [
        '',
        'Browse Mac apps in Homebrew by category, from AI and developer tools to browsers, design and productivity, with each category’s app count and subcategories.'
      ],
      [
        '/zh',
        '按用途浏览 Homebrew 收录的 Mac App，从 AI 工具、开发工具到浏览器、设计和效率软件，每个分类都列出 App 数量和子分类。'
      ],
      [
        '/ru',
        'Просматривайте приложения для Mac из Homebrew по категориям: от ИИ и разработки до браузеров, дизайна и продуктивности, с числом приложений и подкатегорий.'
      ]
    ]) {
      const html = await page(`${prefix}/categories`);
      assert.equal(/<meta name="description" content="([^"]*)"/.exec(html)?.[1], expected, prefix || '/');
    }
    // The page keeps its short subtitle.
    assert.ok((await page('/categories')).includes('Browse Homebrew apps by what they do.'));
  });
  await t.test('recently updated lists the newest releases with real page links', async () => {
    const page = async path => {
      const response = await fetch(`${origin}${path}`, { headers: { accept: 'text/html' } });
      return { status: response.status, html: await response.text() };
    };
    const canonical = html => /<link[^>]*rel="canonical"[^>]*href="([^"]+)"/.exec(html)?.[1]?.replaceAll('&amp;', '&');
    const before = packageQueries.length;
    const first = await page('/recent');
    assert.equal(first.status, 200, logs);
    assert.deepEqual(
      packageQueries.slice(before).map(({ sort, kind, current, size }) => ({ sort, kind, current, size })),
      [{ sort: 'updated', kind: 'cask', current: '1', size: '24' }]
    );
    assert.match(first.html, /<title>Recently Updated Mac Apps · OpenNavo<\/title>/);
    assert.match(first.html, /<h1[^>]*>Recently Updated<\/h1>/);
    assert.equal(canonical(first.html), 'https://example.test/recent');
    const appLink = html => {
      const href = /href="([^"]*\/apps\/ordinary-app\?[^"]*)"/.exec(html)?.[1];
      assert.ok(href, 'app cards retain their return location');
      return new URL(href.replaceAll('&amp;', '&'), origin);
    };
    assert.equal(appLink(first.html).pathname, '/apps/ordinary-app');
    assert.equal(appLink(first.html).searchParams.get('returnTo'), '/recent?page=1&size=24');
    assert.ok(first.html.includes('href="/recent?page=2"'), 'pages are real links');
    assert.ok(first.html.includes('"ItemList"'), 'the listed apps are structured data');
    assert.ok(/aria-current="page"[^>]*href="\/recent"|href="\/recent"[^>]*aria-current="page"/.test(first.html));
    const second = await page('/zh/recent?page=2');
    assert.equal(second.status, 200);
    assert.match(second.html, /<title>最近更新的 Mac App · 第 2 页 · OpenNavo<\/title>/);
    assert.equal(canonical(second.html), 'https://example.test/zh/recent?page=2', 'page 2 is its own canonical');
    assert.equal(appLink(second.html).pathname, '/zh/apps/ordinary-app');
    assert.equal(appLink(second.html).searchParams.get('returnTo'), '/recent?page=2&size=24');
    assert.equal((await page('/recent?page=4')).status, 404, 'past the last page');
  });
  await t.test('paginated lists answer 404 for pages that do not exist and canonicalize the first page', async () => {
    const get = async path => {
      const response = await fetch(`${origin}${path}`, { headers: { accept: 'text/html' } });
      return { response, html: await response.text() };
    };
    const canonical = html => /<link[^>]*rel="canonical"[^>]*href="([^"]+)"/.exec(html)?.[1]?.replaceAll('&amp;', '&');
    const description = html => /<meta name="description" content="([^"]*)"/.exec(html)?.[1] ?? '';
    const ogUrl = html => /<meta[^>]*property="og:url"[^>]*content="([^"]+)"/.exec(html)?.[1]?.replaceAll('&amp;', '&');
    const lists = [
      '/apps',
      '/zh/apps',
      '/categories/utilities',
      '/rankings',
      '/ja/rankings',
      '/recent',
      '/collections',
      '/articles',
      '/authors/lin-wei',
      '/apps/plus-app++/versions'
    ];
    for (const list of lists) {
      for (const value of ['99999', 'abc', '0', '-1', '1.5', '01', '']) {
        const path = `${list}?page=${value}`;
        const { response } = await get(path);
        assert.equal(response.status, 404, path);
        // Nuxt's error page answers no-cache; neither browsers, Cloudflare nor the route cache keep it.
        assert.match(response.headers.get('cache-control') ?? '', /\bno-(?:store|cache)\b/, path);
        assert.equal(response.headers.get('cloudflare-cdn-cache-control'), 'no-store', path);
      }
      // The route cache never turns the 404 into a cached 200.
      assert.equal((await get(`${list}?page=99999`)).response.status, 404, list);
      const first = await get(`${list}?page=1`);
      assert.equal(first.response.status, 200, `${list}?page=1\n${logs}`);
      assert.equal(canonical(first.html), `https://example.test${list}`, `${list}?page=1 canonical`);
      assert.equal(ogUrl(first.html), `https://example.test${list}`, `${list}?page=1 og:url`);
      assert.ok(!/<link[^>]+hreflang="[^"]+"[^>]*page=|<link[^>]+page=[^>]+hreflang=/.test(first.html), list);
      assert.ok(!description(first.html).startsWith('Page'), list);
    }
    for (const [path, title, lead] of [
      ['/apps?page=2', 'Mac Apps · Page 2 · OpenNavo', 'Page 2 · '],
      ['/rankings?page=2', 'Most Popular Mac Software on Homebrew · Page 2 · OpenNavo', 'Page 2 · '],
      ['/zh/rankings?page=3', 'Homebrew 最受欢迎的 Mac 软件排行 · 第 3 页 · OpenNavo', '第 3 页 · '],
      ['/categories/utilities?page=2', undefined, 'Page 2 · '],
      ['/recent?page=3', 'Recently Updated Mac Apps · Page 3 · OpenNavo', 'Page 3 · ']
    ]) {
      const { response, html } = await get(path);
      assert.equal(response.status, 200, `${path}\n${logs}`);
      if (title) assert.ok(html.includes(`<title>${title}</title>`), `${path}: ${/<title>[^<]*/.exec(html)?.[0]}`);
      else assert.match(html, /<title>[^<]* · Page 2 · OpenNavo<\/title>/, path);
      assert.ok(description(html).startsWith(lead), `${path}: ${description(html)}`);
      assert.equal(canonical(html), `https://example.test${path}`, `${path} is its own canonical`);
      assert.equal(ogUrl(html), `https://example.test${path}`, path);
    }
    assert.equal((await get('/rankings?page=4')).response.status, 404, 'past the last ranking page');
    assert.equal((await get('/apps?page=4')).response.status, 404, 'past the last app page');
    for (const prefix of ['', '/zh']) {
      const filtered = await get(`${prefix}/apps?page=4&disabled=1&sort=name`);
      assert.equal(filtered.response.status, 200);
      const target = canonical(filtered.html);
      assert.equal(target, `https://example.test${prefix}/apps?page=4&disabled=1`);
      assert.equal(ogUrl(filtered.html), target);
      assert.match(filtered.html, /<meta name="robots" content="[^"]*noindex/);
      assert.equal((await get(target.replace('https://example.test', ''))).response.status, 200);
      assert.equal((await get(`${prefix}/apps?page=6&disabled=1`)).response.status, 404);
    }
  });
  await t.test('only paginated lists keep page, size and disabled in canonical, hreflang and og:url', async () => {
    const get = async path => {
      const response = await fetch(`${origin}${path}`, { headers: { accept: 'text/html' } });
      return { status: response.status, html: await response.text() };
    };
    const unescape = value => value?.replaceAll('&amp;', '&');
    const canonical = html => unescape(/<link[^>]*rel="canonical"[^>]*href="([^"]+)"/.exec(html)?.[1]);
    const ogUrl = html => unescape(/<meta[^>]*property="og:url"[^>]*content="([^"]+)"/.exec(html)?.[1]);
    const alternates = html =>
      [...html.matchAll(/<link\b[^>]*\bhreflang="[^"]+"[^>]*>/g)].map(([tag]) =>
        unescape(/href="([^"]+)"/.exec(tag)?.[1])
      );
    for (const [path, expected] of [
      ['/?page=2', 'https://example.test'],
      ['/?page=2&size=12&disabled=1', 'https://example.test'],
      ['/zh?page=3', 'https://example.test/zh'],
      ['/discover?page=2', 'https://example.test/discover'],
      ['/categories?page=2', 'https://example.test/categories'],
      ['/apps/ordinary-app?page=3', 'https://example.test/apps/ordinary-app'],
      ['/ja/apps/plus-app++?page=2&size=12', 'https://example.test/ja/apps/plus-app++'],
      ['/articles/choose-a-screenshot-app?page=2', 'https://example.test/articles/choose-a-screenshot-app'],
      ['/download?page=2&disabled=1', 'https://example.test/download']
    ]) {
      const { status, html } = await get(path);
      assert.equal(status, 200, `${path}\n${logs}`);
      assert.equal(canonical(html), expected, `${path} canonical`);
      assert.equal(ogUrl(html), expected, `${path} og:url`);
      const hrefs = alternates(html);
      assert.ok(hrefs.length > 0, path);
      for (const href of hrefs) assert.ok(href && !href.includes('?'), `${path} hreflang: ${href}`);
      assert.ok(!/<title>[^<]*(?:Page \d|第 \d+ 页)/.test(html), `${path} title`);
    }
    // Each list keeps its own bound parameters and drops the rest.
    for (const [path, expected] of [
      ['/apps?page=2&size=12&disabled=1', 'https://example.test/apps?page=2&disabled=1&size=12'],
      ['/recent?page=2&size=12&disabled=1', 'https://example.test/recent?page=2&size=12'],
      ['/zh/rankings?page=2&size=12&disabled=1', 'https://example.test/zh/rankings?page=2'],
      ['/articles?page=2&size=12', 'https://example.test/articles?page=2']
    ]) {
      const { status, html } = await get(path);
      assert.equal(status, 200, `${path}\n${logs}`);
      assert.equal(canonical(html), expected, path);
      assert.equal(ogUrl(html), expected, path);
      for (const href of alternates(html)) assert.equal(new URL(href).search, new URL(expected).search, href);
    }
  });
  await t.test('grid list canonicals preserve the page size that makes a page valid', async () => {
    const get = async path => {
      const response = await fetch(`${origin}${path}`, { headers: { accept: 'text/html' } });
      return {
        status: response.status,
        html: await response.text(),
        robotsHeader: response.headers.get('x-robots-tag')
      };
    };
    const canonical = html => /<link[^>]*rel="canonical"[^>]*href="([^"]+)"/.exec(html)?.[1]?.replaceAll('&amp;', '&');
    const robots = html => [...html.matchAll(/<meta name="robots" content="([^"]*)"/g)].map(m => m[1]);
    // Another page size splits the same items into other pages: its canonical keeps the size (without
    // it, page 4 below would be past the end), and it stays out of the index like other variants.
    const unindexed = (page, label) => {
      assert.deepEqual(robots(page.html), ['noindex, follow'], label);
      assert.equal(page.robotsHeader, 'noindex, follow', label);
    };
    const indexed = (page, label) => {
      assert.ok(!robots(page.html).some(value => value.includes('noindex')), label);
      assert.ok(!(page.robotsHeader ?? '').includes('noindex'), label);
    };
    for (const path of [
      '/apps',
      '/zh/apps',
      '/recent',
      '/ja/recent',
      '/categories/utilities',
      '/ru/categories/utilities'
    ]) {
      const before = packageQueries.length;
      const sized = await get(`${path}?page=4&size=12`);
      assert.equal(sized.status, 200, path);
      assert.ok(
        packageQueries.slice(before).some(query => query.current === '4' && query.size === '12'),
        path
      );
      const target = canonical(sized.html);
      assert.equal(target, `https://example.test${path}?page=4&size=12`);
      unindexed(sized, path);
      assert.equal((await get(target.replace('https://example.test', ''))).status, 200, target);
      indexed(await get(`${path}?page=2`), `${path}?page=2`);
      assert.equal((await get(`${path}?page=4`)).status, 404, path);
      assert.equal((await get(`${path}?page=6&size=12`)).status, 404, path);
      const ogUrl = /<meta[^>]*property="og:url"[^>]*content="([^"]+)"/.exec(sized.html)?.[1]?.replaceAll('&amp;', '&');
      assert.equal(ogUrl, target, path);
      const alternates = [...sized.html.matchAll(/<link[^>]*rel="alternate"[^>]*href="([^"]+)"/g)];
      assert.ok(alternates.length > 0, path);
      for (const [, href] of alternates) {
        const url = new URL(href.replaceAll('&amp;', '&'));
        assert.equal(url.searchParams.get('size'), '12', href);
        assert.equal(url.searchParams.get('page'), '4', href);
      }
    }
    const combined = await get('/apps?page=8&disabled=1&size=12');
    assert.equal(combined.status, 200);
    assert.equal(canonical(combined.html), 'https://example.test/apps?page=8&disabled=1&size=12');
    unindexed(combined, 'disabled and size');
    for (const size of ['24', '101', 'abc', '12&size=12']) {
      const page = await get(`/apps?page=2&size=${size}`);
      assert.equal(canonical(page.html), 'https://example.test/apps?page=2');
      // The default size, or a value that falls back to it, is the list itself.
      indexed(page, `size=${size}`);
    }
    const first = await get('/apps?page=1&size=012');
    assert.equal(canonical(first.html), 'https://example.test/apps?size=12');
    unindexed(first, 'size=012');
  });
  await t.test('URL variants reach their page in one 301 that keeps the query', async () => {
    for (const [from, to] of [
      ['/apps/ordinary-app/', '/apps/ordinary-app'],
      ['/Apps/ordinary-app', '/apps/ordinary-app'],
      ['/APPS/Plus-App++/Versions/?page=1', '/apps/plus-app++/versions?page=1'],
      ['/zh/', '/zh'],
      ['/en', '/'],
      ['/en/download', '/download'],
      ['/en/apps/ordinary-app?ref=old&A=B', '/apps/ordinary-app?ref=old&A=B'],
      ['/Zh/Apps/plus-app%2B%2B/?source=a%2Bb', '/zh/apps/plus-app++?source=a%2Bb']
    ]) {
      const response = await fetch(`${origin}${from}`, { redirect: 'manual' });
      assert.equal(response.status, 301, from);
      assert.equal(response.headers.get('location'), to, from);
      const target = await fetch(`${origin}${to}`, { redirect: 'manual', headers: { accept: 'text/html' } });
      await target.text();
      assert.ok(target.status < 300 || target.status >= 400, `${to} answers itself (${target.status})`);
    }
    for (const path of [
      '/',
      '/zh',
      '/apps/ordinary-app',
      '/robots.txt',
      '/sitemap_index.xml',
      '/apps/ordinary-app/_payload.json',
      '/api/consent-region',
      '/articles/feed.xml',
      '/favicon.svg'
    ]) {
      const response = await fetch(`${origin}${path}`, { redirect: 'manual' });
      await response.arrayBuffer();
      assert.ok(response.status < 300 || response.status >= 400, `${path} is not redirected (${response.status})`);
    }
  });
  await t.test('the home heading reads as one sentence in every language', async () => {
    const heading = async path => {
      const html = await (await fetch(`${origin}${path}`, { headers: { accept: 'text/html' } })).text();
      return (/<h1\b[^>]*>([\s\S]*?)<\/h1>/.exec(html)?.[1] ?? '').replace(/<[^>]*>/g, '');
    };
    assert.equal(await heading('/'), 'Installing Mac apps should be this simple.');
    assert.equal(await heading('/ru'), 'Устанавливать приложения для Mac должно быть так просто.');
    // Chinese and Japanese join their two lines without a space.
    assert.equal(await heading('/zh'), '装 Mac 软件，本该这么简单。');
    assert.equal(await heading('/ja'), 'Macアプリのインストールをもっとシンプルに。');
  });
  await t.test('the home page and Discover describe themselves differently', async () => {
    const description = async path =>
      /<meta name="description" content="([^"]*)"/.exec(
        await (await fetch(`${origin}${path}`, { headers: { accept: 'text/html' } })).text()
      )?.[1];
    for (const prefix of ['', '/zh', '/ja', '/es', '/pt', '/ru']) {
      const home = await description(prefix || '/');
      const discover = await description(`${prefix}/discover`);
      assert.ok(home && discover, prefix);
      assert.notEqual(home, discover, prefix);
    }
  });
  await t.test('links prefetch on pointer or focus only in the built client', async () => {
    // Prefetching on visibility had Google's renderer fetch the payload of every linked page.
    const dir = fileURLToPath(new URL('../.output/public/_nuxt/', import.meta.url));
    const scripts = await Promise.all(
      (await readdir(dir)).filter(name => name.endsWith('.js')).map(name => readFile(`${dir}${name}`, 'utf8'))
    );
    const options = scripts.flatMap(source => source.match(/prefetchOn:\{[^}]*\}/g) ?? []);
    assert.ok(options.includes('prefetchOn:{visibility:!1,interaction:!0}'), options.join());
    assert.ok(!options.includes('prefetchOn:{visibility:!0}'), options.join());
  });
  await t.test('robots.txt follows the admin crawler policy', async () => {
    const response = await fetch(`${origin}/robots.txt`);
    const text = await response.text();
    assert.equal(response.status, 200, logs);
    assert.match(response.headers.get('content-type') ?? '', /^text\/plain/);
    assert.ok(text.startsWith('User-agent: *\nDisallow: /search\nDisallow: /brewfile\n'), text);
    assert.ok(text.includes('\n\nUser-agent: AhrefsBot\nCrawl-delay: 20\nDisallow: /search\n'), text);
    assert.ok(text.includes('\n\nUser-agent: GPTBot\nUser-agent: ClaudeBot\nDisallow: /\n'), text);
    assert.ok(text.endsWith('\nSitemap: https://example.test/sitemap_index.xml\n'), text);
    assert.ok(!/googlebot|bingbot|oai-searchbot/i.test(text));
  });
  await t.test('IndexNow key file answers only the configured key', async () => {
    const configured = await fetch(`${origin}/fixture-indexnow-key-0001.txt`);
    assert.equal(configured.status, 200, logs);
    assert.equal(await configured.text(), 'fixture-indexnow-key-0001');
    assert.match(configured.headers.get('content-type') ?? '', /^text\/plain/);
    const other = await fetch(`${origin}/fixture-indexnow-key-0002.txt`);
    assert.equal(other.status, 404);
    assert.ok(!(await other.text()).includes('fixture-indexnow-key-0001'));
  });
  await t.test('root home page carries ownership verification codes', async () => {
    const html = await (await fetch(`${origin}/`, { headers: { accept: 'text/html' } })).text();
    assert.match(html, /<meta name="google-site-verification" content="fixture-google-code">/);
    assert.match(html, /<meta name="msvalidate.01" content="FIXTUREBINGCODE">/);
    const zh = await (await fetch(`${origin}/zh`, { headers: { accept: 'text/html' } })).text();
    assert.ok(!zh.includes('google-site-verification'));
  });
  await t.test('page robots rules do not depend on the User-Agent', async () => {
    for (const agent of ['MJ12bot/v1.4.8', 'Mozilla/5.0 (compatible; ClaudeBot/1.0)']) {
      const response = await fetch(`${origin}/apps/ordinary-app`, {
        headers: { accept: 'text/html', 'user-agent': agent }
      });
      const html = await response.text();
      assert.equal(response.status, 200, logs);
      assert.ok(!/<meta name="robots" content="[^"]*noindex/.test(html), agent);
      assert.ok(!(response.headers.get('x-robots-tag') ?? '').includes('noindex'), agent);
    }
  });
  await t.test('app pages answer from recorded facts and mark structured data honestly', async () => {
    const html = async path => (await fetch(`${origin}${path}`, { headers: { accept: 'text/html' } })).text();
    const graphOf = page =>
      JSON.parse(/<script type="application\/ld\+json"[^>]*>([\s\S]*?)<\/script>/.exec(page)?.[1] ?? '{}')['@graph'];
    const page = await html('/apps/facts-app');
    const lead = /data-testid="app-answer"[^>]*>([^<]*)</.exec(page)?.[1];
    assert.equal(
      lead,
      'Facts App is a Developer Tools app for Mac. It requires a subscription. It needs macOS 13 or later.'
    );
    const section = id => new RegExp(`data-testid="${id}"[\\s\\S]*?</section>`).exec(page)?.[0] ?? '';
    const guide = section('package-guide');
    assert.ok(guide.includes('Homebrew installs the app in your Applications folder.'), guide);
    assert.ok(guide.includes('href="https://vendor.example/FactsApp.dmg"'));
    assert.ok(guide.includes('it is not a security certification'));
    assert.ok(guide.includes('data-testid="package-zap"'), 'zap only because the definition lists files');
    const facts = section('package-facts');
    assert.ok(facts.includes('Pending verification'), facts);
    assert.ok(facts.includes('vendor.example/pricing'));
    assert.ok(facts.includes('$9.99 per month'));
    const alternatives = section('package-alternatives');
    assert.ok(alternatives.includes('href="/apps/ordinary-app"'));
    assert.ok(alternatives.includes('Open source') && alternatives.includes('Fewer features'));
    const faq = section('package-faq');
    // Questions are headings inside collapsed details, in search order.
    assert.match(faq, /<details[\s\S]*?<summary[^>]*><h3[^>]*>Is Facts App safe\?<\/h3>/);
    // Inline code renders each word in its own element (OnCodeText), so compare the text without tags.
    const faqText = faq.replace(/<[^>]*>/g, '');
    assert.ok(faqText.includes('Is Facts App free?') && faqText.includes('requires a subscription'), faqText);
    assert.ok(faqText.includes('brew uninstall --zap --cask facts-app'), 'collapsed answers are in the HTML');
    assert.ok(faqText.includes('Its settings are in ~/Library/Preferences/facts.plist.'), faqText);
    assert.ok(!faqText.includes('without Homebrew'), 'no boilerplate question');
    // Only the minimum version is disputed, so the vendor's chip statement still answers this.
    assert.ok(faq.includes('The vendor states it runs natively on Apple silicon.'), faq);
    const [app] = graphOf(page).filter(node => node['@type'] === 'SoftwareApplication');
    assert.equal(app.offers.price, 9.99);
    assert.equal(app.offers.priceCurrency, 'USD');
    assert.equal(app.offers.priceValidUntil, '2026-12-01');
    assert.equal(app.softwareRequirements, 'macOS >= 13');
    assert.equal(app.downloadUrl, 'https://vendor.example/FactsApp.dmg');
    assert.ok(!('aggregateRating' in app), 'no invented ratings');
    // Other languages render the same answer in their own words.
    assert.match(
      await html('/ja/apps/facts-app'),
      /data-testid="app-answer"[^>]*>Facts App は「Developer Tools」カテゴリ/
    );
    // Apps without evidence keep a plain lead and no offer; fonts are not SoftwareApplication.
    const ordinary = await html('/apps/ordinary-app');
    const [plain] = graphOf(ordinary).filter(node => node['@type'] === 'SoftwareApplication');
    assert.ok(!('offers' in plain));
    assert.ok(!ordinary.includes('data-testid="package-facts"'), 'no facts card without facts');
    const font = await html('/apps/font-sample');
    assert.equal(graphOf(font).filter(node => node['@type'] === 'SoftwareApplication').length, 0);
    // Lists name their entries in page order; the second page continues the numbering.
    const list = graphOf(await html('/apps?page=2'));
    const [items] = list.filter(node => node['@type'] === 'ItemList');
    assert.deepEqual(
      items.itemListElement.map(item => [item.position, item.url]),
      [[25, 'https://example.test/apps/ordinary-app']]
    );
    assert.ok(list.some(node => [node['@type']].flat().includes('CollectionPage')));
    // The download page describes OpenNavo itself, published by the site organization.
    const [client] = graphOf(await html('/download')).filter(node => node['@type'] === 'SoftwareApplication');
    assert.equal(client['@id'], 'https://example.test/#desktop-app');
    assert.equal(client.publisher['@id'], 'https://example.test/#identity');
  });
  await t.test('share images do not vary with the page query', async () => {
    // patches/nuxt-og-image: a page's query string never reaches its share image URL, so return
    // links and tracking parameters reuse the one image instead of rendering it again.
    const shareImage = async path => {
      const html = await (await fetch(`${origin}${path}`, { headers: { accept: 'text/html' } })).text();
      return /<meta property="og:image" content="([^"]+)"/.exec(html)?.[1];
    };
    const plain = await shareImage('/apps/ordinary-app');
    assert.ok(plain && !plain.includes(',q_'), plain);
    for (const query of ['?utm_source=newsletter', '?returnTo=%2Frecent', '?nocache=1'])
      assert.equal(await shareImage(`/apps/ordinary-app${query}`), plain, query);
  });
  await t.test('every shared page type has a 1200×630 share image', async () => {
    const image = async path => {
      const html = await (await fetch(`${origin}${path}`, { headers: { accept: 'text/html' } })).text();
      const url = /<meta property="og:image" content="([^"]+)"/.exec(html)?.[1]?.replaceAll('&amp;', '&');
      assert.ok(url?.startsWith('https://example.test/_og/'), `${path}: ${url}`);
      assert.match(html, /<meta property="og:image:width" content="1200">/);
      assert.match(html, /<meta property="og:image:height" content="630">/);
      assert.ok(html.includes(`<meta name="twitter:image" content="${url.replaceAll('&', '&amp;')}">`), path);
      // The template's hash is in the URL, so an edited template gets new URLs past immutable caches.
      assert.match(url, /,ch_\w+/, path);
      return url;
    };
    assert.match(await image('/apps/ordinary-app/versions'), /c_OgPackageSatori,/);
    assert.match(await image('/categories/utilities'), /c_OgListSatori,.*label_Category/);
    assert.match(await image('/collections/editors'), /c_OgListSatori,.*label_Collection/);
    assert.match(await image('/download'), /c_OgDefaultSatori,.*variant_home/);
    assert.match(await image('/articles'), /c_OgListSatori,.*label_Articles/);
    assert.match(await image('/articles/choose-a-screenshot-app'), /c_OgListSatori,.*label_Roundup/);
    assert.match(await image('/authors/lin-wei'), /c_OgListSatori,.*label_Author/);
    // Pages that used to declare a large card without an image.
    assert.match(await image('/about'), /c_OgDefaultSatori,.*variant_home/);
    assert.match(await image('/feedback'), /c_OgDefaultSatori,.*variant_home/);
    assert.match(await image('/apps'), /c_OgListSatori,.*label_Apps/);
    assert.match(await image('/rankings'), /c_OgListSatori,.*label_Rankings/);
    assert.match(await image('/recent'), /c_OgListSatori,.*label_Recently/);
    assert.match(await image('/categories'), /c_OgListSatori,.*label_Categories/);
    assert.match(await image('/collections'), /c_OgListSatori,.*label_Collections/);
    assert.match(await image('/zh/rankings'), /c_OgListSatori,/);
    // The image itself renders, here with the Russian label.
    const rendered = await fetch((await image('/ru/categories/utilities')).replace('https://example.test', origin));
    assert.equal(rendered.status, 200, logs);
    assert.equal(rendered.headers.get('content-type'), 'image/png');
  });
  await t.test('article lists paginate with real links and keep filters out of the index', async () => {
    const page = async path => {
      const response = await fetch(`${origin}${path}`, { headers: { accept: 'text/html' } });
      return { status: response.status, html: await response.text() };
    };
    const canonical = html => /<link[^>]*rel="canonical"[^>]*href="([^"]+)"/.exec(html)?.[1]?.replaceAll('&amp;', '&');
    const robots = html => [...html.matchAll(/<meta name="robots" content="([^"]*)"/g)].map(m => m[1]);
    const first = await page('/articles');
    assert.equal(first.status, 200, logs);
    assert.match(first.html, /<title>Mac App Articles: Picks, Reviews, Tutorials and Roundups · OpenNavo<\/title>/);
    assert.equal(canonical(first.html), 'https://example.test/articles');
    assert.ok(!robots(first.html).some(value => value.includes('noindex')));
    assert.match(
      first.html,
      /<link rel="alternate" type="application\/rss\+xml"[^>]*href="https:\/\/example.test\/articles\/feed.xml"/
    );
    assert.ok(first.html.includes('href="/articles/choose-a-screenshot-app"'));
    assert.ok(first.html.includes('href="/articles?page=2"'), 'pages are real links');
    assert.ok(first.html.includes('href="/articles"') && first.html.includes('aria-label="Article type"'));
    const second = await page('/articles?page=2');
    assert.equal(second.status, 200);
    assert.equal(canonical(second.html), 'https://example.test/articles?page=2', 'page 2 is its own canonical');
    assert.match(
      second.html,
      /<title>Mac App Articles: Picks, Reviews, Tutorials and Roundups · Page 2 · OpenNavo<\/title>/
    );
    assert.equal((await page('/articles?page=3')).status, 404, 'past the last page');
    const filtered = await page('/articles?type=tutorial');
    assert.deepEqual(robots(filtered.html), ['noindex, follow']);
    for (const html of [first.html, second.html, filtered.html])
      assert.equal([...html.matchAll(/rel="canonical"/g)].length, 1, 'exactly one canonical link');
    assert.equal(canonical(filtered.html), 'https://example.test/articles?type=tutorial');
    const zh = await page('/zh/articles');
    assert.ok(zh.html.includes('如何选择截图工具'));
    assert.ok(zh.html.includes('href="/zh/articles/feed.xml"'));
  });
  await t.test('article pages render the full body with honest structured data and languages', async () => {
    const response = await fetch(`${origin}/zh/articles/choose-a-screenshot-app`, { headers: { accept: 'text/html' } });
    const html = await response.text();
    assert.equal(response.status, 200, logs);
    assert.match(html, /<title>如何选择截图工具 · OpenNavo<\/title>/);
    assert.match(html, /<h1[^>]*>\s*如何选择截图工具\s*<\/h1>/);
    assert.match(html, /<h2 id="怎么选">怎么选<\/h2>/);
    assert.ok(html.includes('<table>'), 'Markdown tables render');
    assert.ok(html.includes('href="#对比"'), 'table of contents links to anchors');
    assert.equal([...html.matchAll(/data-testid="article-app-card"/g)].length, 2, 'unknown apps render no card');
    assert.ok(html.includes('当前目录数据'));
    assert.ok(html.includes('AI 翻译') && html.includes('href="/articles/choose-a-screenshot-app"'), 'view original');
    assert.ok(html.includes('href="/zh/authors/lin-wei"'));
    assert.ok(html.includes('<time datetime="2026-10-12T10:00:00Z">'), 'publication date');
    assert.ok(html.includes('<time datetime="2026-10-14T10:00:00Z">'), 'a later update shows its own date');
    assert.ok(html.includes('MacBook Pro (M4), macOS 26.'));
    assert.ok(html.includes('href="https://vendor.example/docs"'));
    assert.ok(html.includes('href="/zh/articles/manage-homebrew-without-terminal"'), 'related articles');
    assert.ok(!/<meta name="robots" content="[^"]*noindex/.test(html));
    const hreflangs = [...html.matchAll(/<link[^>]+hreflang="([^"]+)"/g)].map(m => m[1]).sort();
    assert.deepEqual(hreflangs, ['en', 'x-default', 'zh', 'zh-Hans'], 'only languages whose text is ready');
    const graph = JSON.parse(/<script type="application\/ld\+json"[^>]*>([\s\S]*?)<\/script>/.exec(html)?.[1] ?? '{}')[
      '@graph'
    ];
    const nodes = type => graph.filter(node => [node['@type']].flat().includes(type));
    const [post] = nodes('BlogPosting');
    assert.equal(post.headline, '如何选择截图工具');
    assert.equal(post.inLanguage, 'zh-CN');
    assert.equal(post.datePublished, '2026-10-12T10:00:00Z');
    assert.equal(post.dateModified, '2026-10-14T10:00:00Z');
    assert.equal(post.publisher['@id'], 'https://example.test/#identity');
    const imageRef = [post.image].flat()[0];
    const imageUrl =
      typeof imageRef === 'string'
        ? imageRef
        : (graph.find(node => node['@id'] === imageRef?.['@id']) ?? imageRef)?.url;
    assert.ok(
      imageUrl?.startsWith('https://example.test/_og/'),
      `the share image stands in for a missing cover: ${imageUrl}`
    );
    const person = graph.find(node => node['@id'] === 'https://example.test/authors/lin-wei#person');
    assert.equal(person?.name, 'Lin Wei');
    assert.equal(person?.url, 'https://example.test/zh/authors/lin-wei');
    assert.equal([post.author].flat()[0]['@id'], person['@id']);
    assert.deepEqual(
      post.mentions.map(item => item['@id']),
      ['https://example.test/apps/ordinary-app#app', 'https://example.test/apps/facts-app#app']
    );
    const [list] = nodes('ItemList');
    assert.deepEqual(
      list.itemListElement.map(item => item.url),
      ['https://example.test/zh/apps/ordinary-app', 'https://example.test/zh/apps/facts-app']
    );
    assert.deepEqual(
      nodes('BreadcrumbList')[0].itemListElement.map(item => item.name),
      ['文章', '合集', '如何选择截图工具']
    );
    // A language whose text is not ready shows the source text, unindexed.
    const ja = await (
      await fetch(`${origin}/ja/articles/choose-a-screenshot-app`, { headers: { accept: 'text/html' } })
    ).text();
    assert.match(ja, /<meta name="robots" content="noindex, follow"/);
    assert.match(ja, /<article[^>]*lang="en-US"/);
    assert.ok(ja.includes('data-testid="article-source-text"'));
    // App pages link back to the articles that cover them, by stable entity IDs in structured data.
    const app = await (await fetch(`${origin}/apps/ordinary-app`, { headers: { accept: 'text/html' } })).text();
    assert.ok(/data-testid="package-articles"[\s\S]*href="\/articles\/choose-a-screenshot-app"/.test(app));
    assert.ok(app.includes('"@id":"https://example.test/apps/ordinary-app#app"'));
  });
  await t.test('moved, withdrawn and unknown articles get 301, 410 and 404', async () => {
    const moved = await fetch(`${origin}/zh/articles/old-slug`, { redirect: 'manual' });
    assert.equal(moved.status, 301, logs);
    assert.equal(new URL(moved.headers.get('location'), origin).pathname, '/zh/articles/choose-a-screenshot-app');
    const withdrawn = await fetch(`${origin}/articles/pulled-article`, { headers: { accept: 'text/html' } });
    const html = await withdrawn.text();
    assert.equal(withdrawn.status, 410);
    assert.ok(html.includes('This article has been withdrawn'));
    assert.match(html, /<meta name="robots" content="noindex, follow"/);
    const payload = await fetch(`${origin}/articles/pulled-article/_payload.json`);
    assert.equal(payload.status, 200);
    assert.ok((await payload.text()).includes('withdrawn'));
    assert.equal((await fetch(`${origin}/articles/missing-article`)).status, 404);
    assert.equal((await fetch(`${origin}/articles/Bad_Slug`)).status, 404);
  });
  await t.test('sitemap index omits empty article groups and restores them when content returns', async () => {
    const readIndex = () => fetch(`${origin}/sitemap_index.xml`);
    try {
      articleSitemapState = 'empty';
      const response = await readIndex();
      assert.equal(response.status, 200);
      const empty = await response.text();
      assert.ok(!empty.includes('/__sitemap__/articles.xml'), empty);
      assert.ok(!empty.includes('/__sitemap__/authors.xml'), empty);
      assert.ok(empty.includes('/__sitemap__/en-static.xml'), empty);
      assert.ok(/\/__sitemap__\/apps-bucket-\d+\.xml/.test(empty), empty);

      articleSitemapState = 'no-authors';
      const partial = await (await readIndex()).text();
      assert.ok(partial.includes('/__sitemap__/articles.xml'), partial);
      assert.ok(!partial.includes('/__sitemap__/authors.xml'), partial);

      articleSitemapState = 'error';
      assert.ok((await readIndex()).status >= 500, 'API failure must not become an empty successful index');
    } finally {
      articleSitemapState = 'ok';
    }
    const restored = await readIndex();
    assert.equal(restored.status, 200);
    const xml = await restored.text();
    assert.ok(xml.includes('/__sitemap__/articles.xml'), xml);
    assert.ok(xml.includes('/__sitemap__/authors.xml'), xml);
  });
  await t.test('author pages, feeds and sitemaps cover published articles', async () => {
    const response = await fetch(`${origin}/authors/lin-wei`, { headers: { accept: 'text/html' } });
    const html = await response.text();
    assert.equal(response.status, 200, logs);
    assert.match(html, /<title>Lin Wei · OpenNavo Author<\/title>/);
    assert.ok(html.includes('rel="me noopener noreferrer"'));
    const graph = JSON.parse(/<script type="application\/ld\+json"[^>]*>([\s\S]*?)<\/script>/.exec(html)?.[1] ?? '{}')[
      '@graph'
    ];
    const profile = graph.find(node => [node['@type']].flat().includes('ProfilePage'));
    assert.equal(profile.mainEntity['@id'], 'https://example.test/authors/lin-wei#person');
    const person = graph.find(node => node['@id'] === 'https://example.test/authors/lin-wei#person');
    assert.deepEqual([person.sameAs].flat(), ['https://lin.example']);
    assert.equal(person.jobTitle, 'Editor');
    const feed = await fetch(`${origin}/zh/articles/feed.xml`);
    const xml = await feed.text();
    assert.equal(feed.status, 200);
    assert.match(feed.headers.get('content-type') ?? '', /application\/rss\+xml/);
    assert.ok(xml.includes('<link>https://example.test/zh/articles/choose-a-screenshot-app</link>'));
    assert.ok(
      xml.includes(
        '<atom:link href="https://example.test/zh/articles/feed.xml" rel="self" type="application/rss+xml"/>'
      ),
      'the feed names its own URL'
    );
    assert.ok(!xml.includes('manage-homebrew-without-terminal'), 'only articles written in the feed language');
    const english = await (await fetch(`${origin}/articles/feed.xml`)).text();
    assert.ok(english.includes('manage-homebrew-without-terminal'));
    assert.ok(english.includes('<atom:link href="https://example.test/articles/feed.xml" rel="self"'));
    assert.equal((await fetch(`${origin}/xx/articles/feed.xml`)).status, 404);
    const index = await (await fetch(`${origin}/sitemap_index.xml`)).text();
    const group = async name => {
      const url = new RegExp(`<loc>([^<]*\\/__sitemap__\\/${name}\\.xml)</loc>`).exec(index)?.[1];
      assert.ok(url, `${name}: ${index}`);
      return (await fetch(url.replace('https://example.test', origin))).text();
    };
    const articles = await group('articles');
    assert.ok(articles.includes('<loc>https://example.test/articles/choose-a-screenshot-app</loc>'));
    assert.ok(articles.includes('<loc>https://example.test/zh/articles/choose-a-screenshot-app</loc>'));
    assert.ok(!articles.includes('/ja/articles/choose-a-screenshot-app'), 'languages without text stay out');
    assert.ok(articles.includes('<loc>https://example.test/articles</loc>'));
    assert.ok((await group('authors')).includes('<loc>https://example.test/authors/lin-wei</loc>'));
    assert.ok(!(await group('en-static')).includes('<loc>https://example.test/articles</loc>'));
  });
  // Moves the server clock, so it runs last.
  await t.test('a cached page that turns into a 404 stops answering 200 once it expires', async () => {
    const status = async path => {
      const response = await fetch(`${origin}${path}`, { headers: { accept: 'text/html' } });
      await response.text();
      return response.status;
    };
    try {
      assert.equal(await status('/ja/apps?page=3'), 200, logs);
      // The catalog shrinks: page 3 is now past the end, but stays a cached 200 until it expires.
      packagesTotal = 30;
      assert.equal(await status('/ja/apps?page=3'), 200);
      child.kill('SIGUSR2');
      await new Promise(resolve => setTimeout(resolve, 200));
      // Expired: one stale answer while the page is rendered again, then the real status.
      let last;
      for (let attempt = 0; attempt < 40 && last !== 404; attempt++) {
        last = await status('/ja/apps?page=3');
        if (last !== 404) await new Promise(resolve => setTimeout(resolve, 50));
      }
      assert.equal(last, 404, 'the expired 200 must not outlive the page');
      assert.equal(await status('/ja/apps?page=3'), 404);
      assert.equal(await status('/ja/apps?page=2'), 200);
      // Expired sitemap data keeps answering through an API outage instead of failing or shrinking.
      sitemapState = 'error';
      const index = await fetch(`${origin}/sitemap_index.xml`);
      assert.equal(index.status, 200);
      assert.match(await index.text(), /\/__sitemap__\/apps-bucket-\d+\.xml/);
    } finally {
      packagesTotal = 50;
      sitemapState = 'ok';
    }
  });
  assert.deepEqual([...requestedTokens].sort(), [
    'facts-app',
    'font-sample',
    'ordinary-app',
    'plus-app++',
    'thin-app',
    'versioned-app@11',
    'wechat'
  ]);
});
