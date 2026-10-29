import assert from 'node:assert/strict';
import { redisFixture } from './fixtures/redis.mjs';
import { packageSitemapName } from '../shared/utils/sitemapBuckets.ts';
import { labelPath } from '../shared/utils/backgroundLabels.ts';
import { spawn } from 'node:child_process';
import { createHash } from 'node:crypto';
import { once } from 'node:events';
import { access, readdir, readFile } from 'node:fs/promises';
import { createServer } from 'node:http';
import { createServer as createPortReservation } from 'node:net';
import { fileURLToPath } from 'node:url';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';
import { LOCALE_METADATA, SITE_LOCALES } from '@opennavo/shared/locales';

const lifecycleCases = JSON.parse(
  await readFile(new URL('../../../packages/shared/tests/fixtures/packageInstallStates.json', import.meta.url), 'utf8')
);
const qaTokens = [
  ...lifecycleCases.map((_, index) => `qa-lifecycle-${index}`),
  'qa-spec-metadata',
  'qa-invalid-repository'
];

// Former public labels, retained only as regression assertions.
const translationProvenanceLabels = {
  'en-US': ['AI translation', 'View original', 'AI translated'],
  'zh-CN': ['AI 翻译', '查看原文'],
  'ja-JP': ['AI 翻訳', '原文を見る', '原文を表示'],
  'es-ES': ['Traducción con IA', 'Ver original'],
  'pt-BR': ['Tradução por IA', 'Ver original'],
  'ru-RU': ['Перевод ИИ', 'Открыть оригинал', 'Показать оригинал'],
  'de-DE': ['KI-Übersetzung', 'Original ansehen', 'KI-übersetzt'],
  'fr-FR': ['Traduction IA', 'Voir l’original', 'Traduit par IA'],
  'it-IT': ['Traduzione con IA', 'Mostra l’originale', 'Tradotto con IA'],
  'zh-Hant': ['AI 翻譯', '檢視原文']
};

// Check the rendered HTML that crawlers receive. Empty alt is valid for explicit decorations and for an
// image inside a named graphic, such as OnAppIcon's standalone mode; content images need their own text.
function assertImageAlternatives(html, page) {
  const images = [...JSDOM.fragment(html).querySelectorAll('img')];
  for (const image of images) {
    const context = `${page}: ${image.getAttribute('src') ?? '<img>'}`;
    assert.ok(image.hasAttribute('alt'), `${context}: missing alt attribute`);
    const alt = image.getAttribute('alt');
    if (alt !== '') {
      assert.ok(alt.trim(), `${context}: alt contains only whitespace`);
      continue;
    }
    const decorative = image.closest('[aria-hidden="true"]') || ['none', 'presentation'].includes(image.role);
    const namedGraphic = image.closest('[role="img"][aria-label]')?.getAttribute('aria-label')?.trim();
    assert.ok(decorative || namedGraphic, `${context}: content image has empty alt without a decorative marker`);
  }
  return images;
}

test('image alternatives reject missing or blank descriptions while accepting explicit decorations', () => {
  for (const html of [
    '<img src="/screenshot.png">',
    '<img src="/screenshot.png" alt="">',
    '<img src="/screenshot.png" alt=" \t ">',
    '<img src="/screenshot.png" alt="&#x20;&#xA0;">',
    '<span aria-hidden="true"><img src="/icon.png"></span>',
    '<span role="presentation"><img src="/screenshot.png" alt=""></span>',
    '<span role="img" aria-label=" "><img src="/screenshot.png" alt=""></span>'
  ]) {
    assert.throws(() => assertImageAlternatives(html, 'fixture'), /alt/);
  }
  for (const html of [
    '<img src="/screenshot.png" alt="Export settings with format and quality controls">',
    '<span aria-hidden="true"><span><img src="/icon.png" alt=""></span></span>',
    '<img src="/icon.png" alt="" aria-hidden="true">',
    '<img src="/decoration.png" alt="" role="presentation">',
    '<span role="img" aria-label="Inkscape"><img src="/icon.png" alt=""></span>'
  ]) {
    assert.equal(assertImageAlternatives(html, 'fixture').length, 1);
  }
  assert.equal(
    assertImageAlternatives('<script>const example = \'<img src="/example.png">\';</script>', 'fixture').length,
    0
  );
});

// Mirror packageFixtures.ts and the API's normalized facts for the synthetic details below.
const evidence = (sourceUrl, overrides = {}) => ({
  sourceUrl,
  checkedAt: '2025-12-01T08:00:00Z',
  validUntil: '2026-12-01T08:00:00Z',
  status: 'verified',
  ...overrides
});
function factsFrom(detail) {
  const proof = evidence(
    detail.sourceUrl,
    detail.kind === 'link' ? { checkedAt: null, validUntil: null, status: 'unknown' } : {}
  );
  const requirements = (detail.platforms ?? []).map(platform => ({
    versions: [platform.version],
    architectures: platform.arch ? [platform.arch] : [],
    platformMacOS: platform.macos ? [platform.macos] : [],
    minimumMacOS: platform.minMacos ?? null,
    maximumMacOS: null,
    appleSilicon:
      (platform.requiresRosetta ?? detail.supports?.requiresRosetta) && platform.arch === 'arm64' ? 'no' : 'unknown',
    downloadUrl: platform.downloadUrl ?? null,
    sha256: /^[a-f0-9]{64}$/i.test(platform.downloadSha256 ?? '') ? platform.downloadSha256.toLowerCase() : null,
    evidence: proof
  }));
  const supplement = detail.macosSupplement;
  if (supplement)
    for (const range of supplement.ranges)
      requirements.push({
        versions: [supplement.appVersion],
        architectures: [range.arch],
        platformMacOS: [],
        minimumMacOS: range.minimum,
        maximumMacOS: range.maximum,
        appleSilicon: range.arch === 'arm64' ? (range.support === 'native' ? 'yes' : 'no') : 'unknown',
        downloadUrl: null,
        sha256: null,
        evidence: evidence(supplement.sourceUrl, {
          checkedAt: supplement.verifiedAt,
          status: supplement.conflicts.length || supplement.appVersion !== detail.version ? 'needs_review' : 'verified'
        })
      });
  const price = detail.price;
  const prices = price
    ? [
        {
          region: price.region ?? 'ZZ',
          edition: price.plan ?? 'unspecified',
          period:
            price.type === 'free'
              ? 'free'
              : ({ one_time: 'one_time', month: 'monthly', year: 'yearly' }[price.period] ?? 'unknown'),
          usage: price.type,
          downloadFree: ['free', 'free_with_iap'].includes(price.type),
          ...(price.amount != null ? { amount: String(price.amount) } : {}),
          ...(price.currency ? { currency: price.currency } : {}),
          purchaseUrl: price.sourceUrl,
          channel: 'vendor',
          evidence: evidence(price.sourceUrl, {
            checkedAt: price.verifiedAt,
            status: price.stale ? 'stale' : 'verified'
          })
        }
      ]
    : [];
  const uninstall = [];
  for (const entry of detail.artifacts.entries ?? []) {
    if (!['uninstall', 'cleanup'].includes(entry.phase)) continue;
    const phase = entry.phase === 'cleanup' ? 'zap' : 'uninstall';
    const walk = declaration => {
      for (const key of ['uninstall', 'zap']) {
        const nested = declaration[key];
        for (const item of Array.isArray(nested) ? nested : [nested]) if (item && typeof item === 'object') walk(item);
      }
      for (const key of ['launchctl', 'login_item', 'pkgutil', 'trash', 'delete', 'rmdir']) {
        const kind = ['trash', 'delete', 'rmdir'].includes(key) ? 'zap' : key;
        const raw = declaration[key];
        for (const value of Array.isArray(raw) ? raw : [raw])
          if (
            typeof value === 'string' &&
            value &&
            !uninstall.some(item => item.phase === phase && item.kind === kind && item.value === value)
          )
            uninstall.push({
              kind,
              value,
              phase,
              deletesUserData: phase === 'zap',
              evidence: proof
            });
      }
    };
    walk(entry.declaration);
  }
  return {
    requirements,
    uninstall,
    overview: {
      version: detail.version,
      downloadUrl: detail.downloadUrl ?? null,
      sha256: detail.downloadSha256 ?? null,
      artifacts: detail.artifacts,
      caveats: detail.caveats ?? null,
      aliases: [],
      dependencyCount: (detail.dependsOn?.casks.length ?? 0) + (detail.dependsOn?.formulae.length ?? 0),
      conflictCount: (detail.conflictsWith?.casks.length ?? 0) + (detail.conflictsWith?.formulae.length ?? 0)
    },
    alternatives: (detail.alternatives ?? []).map(item => {
      // refreshFacts keeps only comparisons backed by verified attributes, never legacy marketing codes.
      const attributes = (item.attributes ?? []).filter(attribute => attribute.evidence.status === 'verified');
      return {
        ...item,
        reasons: attributes.some(attribute => attribute.name === 'open_source' && attribute.booleanValue)
          ? ['open_source']
          : [],
        limits: [],
        attributes,
        prices: []
      };
    }),
    ...(price ? { price } : {}),
    ...(supplement ? { macosSupplement: supplement } : {}),
    prices,
    offers: prices.filter(
      item =>
        item.evidence.status === 'verified' &&
        item.amount != null &&
        item.currency &&
        item.region !== 'ZZ' &&
        item.edition !== 'unspecified' &&
        ['free', 'paid', 'subscription'].includes(item.usage)
    ),
    security: {
      identities: [],
      vulnerabilities: [],
      scanUrl: `opennavo://scan/${detail.token}?preview=1`
    },
    issues: [],
    integrations: []
  };
}

const localePrefixes = {
  'en-US': '',
  'zh-CN': '/zh',
  'zh-Hant': '/zh-hant',
  'ja-JP': '/ja',
  'es-ES': '/es',
  'pt-BR': '/pt',
  'ru-RU': '/ru',
  'de-DE': '/de',
  'fr-FR': '/fr',
  'it-IT': '/it'
};
const INDEX_ROBOTS = 'index, follow, max-image-preview:large, max-snippet:-1, max-video-preview:-1';
function pageDecisions(
  token,
  locale,
  locales,
  notesCount = 0,
  hasDependencyContent = false,
  isFont = false,
  fontsSitemap = true
) {
  const indexable = locales.includes(locale);
  const fontIndexable = !isFont || fontsSitemap;
  const decision = (allowed, hreflang = [], canonicalPath = '', reason = '') => ({
    robots: allowed ? INDEX_ROBOTS : 'noindex, follow',
    canonicalPath,
    hreflang,
    inSitemap: allowed && !canonicalPath,
    announce: allowed && !canonicalPath,
    reason
  });
  const overview = decision(
    indexable && fontIndexable,
    indexable && fontIndexable ? locales : [],
    '',
    !fontIndexable ? 'font_page' : indexable ? '' : 'locale_not_ready'
  );
  return {
    overview,
    versions: decision(
      indexable && fontIndexable && notesCount >= 3,
      indexable && fontIndexable && notesCount >= 3 ? locales : [],
      '',
      !fontIndexable ? 'font_page' : ''
    ),
    // Mirror seo.DecidePage: empty dependencies and details consolidate even in an unpublished locale, and a
    // dependencies tab with content supports the overview: its own canonical, but noindex in every language.
    dependencies: hasDependencyContent
      ? decision(false, [], '', 'supporting_page')
      : decision(false, [], `${localePrefixes[locale]}/apps/${token}`, 'duplicate_of_overview'),
    details: decision(false, [], `${localePrefixes[locale]}/apps/${token}`, 'consolidated'),
    alternatives: decision(false),
    uninstall: decision(false),
    troubleshooting: decision(false)
  };
}

// Run after `pnpm --filter @opennavo/web build`. Exercise the real production
// renderer against synthetic API data, without a browser or production services.
test('SSR preserves literal @ in package routes and page-authored metadata', { timeout: 60000 }, async t => {
  const entry = fileURLToPath(new URL('../.output/server/index.mjs', import.meta.url));
  await access(entry);
  const requestedTokens = new Set();
  const backgroundLabels = [
    'Google Drive',
    'Vitamin-R 4',
    'com.steinberg.dorico.*',
    "Agent (helper)'!",
    'homebrew@16',
    'a+b:c',
    '后台项目',
    'literal%25',
    'literal%2F'
  ];
  const requestedLabels = [];
  let backgroundItemsReady = false;
  // Requests for the retired SEO experiment API; no page may ask for it any more.
  const seoApiRequests = [];
  // Synthetic "SEO App" overviews: no extra facts, a verified free price, full uninstall and alternatives
  // facts, and per-language search overrides (English and Chinese for both fields, English title only, and
  // an app Homebrew has disabled).
  const seoApps = ['seo-basic', 'seo-free', 'seo-uninstall', 'seo-override', 'seo-override-title', 'seo-disabled'];
  const seoOverrides = {
    'seo-override': {
      'en-US': {
        title: 'SEO App for Mac, picked by editors',
        description: 'An editor-written description of SEO App.'
      },
      'zh-CN': { title: 'SEO App 编辑精选', description: '编辑撰写的 SEO App 介绍。' }
    },
    'seo-override-title': { 'en-US': { title: 'Custom SEO App title' } },
    'seo-disabled': {
      'en-US': { title: 'SEO App for Mac – Free Download', description: 'Download SEO App for free on your Mac.' }
    }
  };
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
    pages: {
      titleTemplate: 'new',
      indexPolicy: { mode: 'enforce', scope: 'all', since: '' },
      fontsSitemap: false,
      versionsSitemap: true
    }
  };
  // Index decisions per fixture app (WP11); versioned-app@11 mimics an older API without them.
  const indexing = {
    'ordinary-app': {
      indexable: true,
      reason: null,
      locales: ['en-US', 'zh-CN'],
      enforced: true,
      notesCount: 5
    },
    'thin-app': {
      indexable: false,
      reason: 'missing_summary',
      locales: [],
      enforced: true,
      notesCount: 0
    }
  };
  // WP9/WP10: an app with verified price, a disputed macOS supplement, an alternative and --zap files.
  const factsApp = {
    displayName: 'Facts App',
    names: ['Facts App', 'facts-app', 'Facts Alternate'],
    autoUpdates: true,
    homepage: 'https://vendor.example/',
    downloadUrl: 'https://vendor.example/FactsApp.dmg',
    downloadSha256: 'b'.repeat(64),
    // Requirements now come from normalized platform rows, not the legacy dependsOn.macos string.
    platforms: [
      {
        version: '11.0',
        arch: 'arm64',
        minMacos: '13',
        downloadUrl: 'https://vendor.example/FactsApp.dmg',
        downloadSha256: 'b'.repeat(64)
      }
    ],
    primaryCategory: { slug: 'developer-tools', name: 'Developer Tools', icon: 'lucide:code' },
    dependsOn: {
      macos: '>= 13',
      arch: [],
      formulae: [],
      casks: [],
      requirements: { macos: { '>=': ['13'] } }
    },
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
        attributes: [
          {
            name: 'open_source',
            booleanValue: true,
            evidence: evidence('https://example.test/source')
          }
        ],
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
    avatarUrl: 'https://example.test/images/editor-avatar.png',
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
    cover: {
      url: 'https://example.test/images/homebrew-app-list.png',
      width: 1280,
      height: 720,
      alt: 'Homebrew app list showing installed apps and available updates'
    },
    author: null,
    publishedAt: '2026-10-10T10:00:00Z',
    updatedAt: '2026-10-10T10:00:00Z',
    readingMinutes: 5,
    locale: 'en-US',
    machineTranslated: false
  };
  const roundup = zh => ({
    ...roundupCard(zh),
    bodyMarkdown: [
      `## ${zh ? '怎么选' : 'What to compare'}`,
      'Pick by task.',
      '::app[ordinary-app]',
      `## ${zh ? '对比' : 'Side by side'}`,
      '| App | Price |\n| --- | --- |\n| A | Free |',
      '::app[facts-app]',
      '::app[missing-app]',
      '[Relative app](/apps/ordinary-app?notes=1&original=1#install)',
      '[Absolute app](https://opennavo.com/es/apps/ordinary-app#install)',
      '[WWW app](https://www.opennavo.com/ru/apps/ordinary-app)',
      '[Traditional app](/zh-hant/apps/ordinary-app)',
      '[Simplified app](/zh/apps/ordinary-app)',
      '[Dotted app](/apps/tool.app)',
      '[Dotted label](/background-items/com.Example.js)',
      '[Article feed](/articles/feed.xml)',
      '[App feed](/es/apps/ordinary-app/feed.xml)',
      '[Collection feed](/collections/picks/feed.xml)',
      '[API](/api/v1/articles?raw=1#data)',
      '[Asset](https://opennavo.com/_og/article.png)',
      '[Other feed](/articles/feed.json)',
      '[External](https://vendor.example/docs)',
      '[Anchor](#install)',
      '[Email](mailto:reader@example.test)',
      '```md\n[Block](/zh/apps/ordinary-app)\n```',
      '`[Inline](/zh-hant/apps/ordinary-app)`',
      '![Article capture](/images/article-capture.png)'
    ].join('\n\n'),
    testNote: 'MacBook Pro (M4), macOS 26.',
    seoTitle: null,
    seoDescription: null,
    sourceLocale: 'en-US',
    availableLocales: ['en-US', 'zh-CN'],
    author,
    sources: [{ title: 'Vendor docs', url: 'https://vendor.example/docs' }],
    packages: [appSummary('ordinary-app', 'Ordinary App'), appSummary('facts-app', 'Facts App')],
    images: [{ url: '/images/article-capture.png', width: 1280, height: 800 }],
    related: [tutorialCard],
    revision: 2
  });
  const repairReleases = Array.from({ length: 24 }, (_, index) => ({
    id: null,
    version: index === 1 ? '5.0,501' : `${5 - Math.floor(index / 10)}.${index}.0`,
    title: null,
    publishedAt: '2026-10-01T00:00:00Z',
    brewCommittedAt: null,
    source: 'github_release',
    sourceUrl: 'https://vendor.example/releases',
    isLatest: index === 0,
    isPrerelease: false,
    hasNotes: index === 0 || index >= 20,
    summary: null,
    sections: [],
    bodyMarkdown: index === 0 || index >= 20 ? 'Verified release notes.' : null,
    compatibility: { minimumMacOS: null, maximumMacOS: null, appleSilicon: 'unknown', requirements: [] },
    translation: { status: 'original', locale: 'en-US' }
  }));
  repairReleases[0].version = '5.0';
  let sitemapState = 'ok';
  let articleSitemapState = 'ok';
  // Changes what ordinary-app's description says, to see whether a page came from the cache.
  let revision = 1;
  let collectionState = 'empty';
  let collectionRequests = 0;
  const packageQueries = [];
  // App list total, so a cached list page can be made to run past its end.
  let packagesTotal = 50;
  let hotVersion = '';
  let hotFailure = '';
  const hotReads = new Map();
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
    if (
      [
        '/api/v1/packages',
        '/api/v1/packages/cask/ordinary-app',
        '/api/v1/packages/cask/ordinary-app/releases'
      ].includes(path)
    )
      hotReads.set(path, (hotReads.get(path) ?? 0) + 1);
    if (hotFailure && path === hotFailure) {
      status = 503;
      data = null;
    } else if (path.startsWith('/api/v1/seo/')) {
      seoApiRequests.push(path);
      status = 404;
      data = null;
    } else if (lifecycle === 'old-token') {
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
          alternatives: [
            {
              kind: 'cask',
              token: 'ordinary-app',
              displayName: 'Ordinary App',
              summary: 'Still here'
            }
          ],
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
        version: hotVersion || '11.0',
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
        categories: Array.from({ length: 8 }, (_, i) => ({
          slug: `category-${i}`,
          name: `Category ${i}`,
          icon: 'lucide:wrench',
          appliesTo: 'both',
          hiddenByDefault: false,
          packageCount: i + 1,
          children: []
        })),
        popularApps: [appSummary('ordinary-app', 'Ordinary App')],
        popularCli: [],
        recentlyUpdated: [appSummary('facts-app', 'Facts App')],
        collections: Array.from({ length: 6 }, (_, i) => ({
          slug: `collection-${i}`,
          title: `Collection ${i}`,
          subtitle: '',
          itemCount: 1,
          iconUrls: [],
          previewItems: [],
          sourceLocale: 'en-US',
          updatedAt: '2026-01-01T00:00:00Z'
        })),
        stats: { casks: 50, formulae: 0, updatedLast24h: 3 }
      };
    } else if (
      /^\/api\/v1\/packages\/cask\/(ordinary-app|dependency-content-app|reverse-dependency-app|variant-app@beta)\/dependencies$/.test(
        path
      )
    ) {
      // C2 dependency descriptions count the dependency response rather than repeating the summary.
      data = {
        dependencies: path.includes('/dependency-content-app/')
          ? [{ kind: 'cask', token: 'ordinary-app', name: 'Ordinary App', type: 'cask', children: [] }]
          : [],
        conflictsWith: { casks: [], formulae: [] },
        dependents: path.includes('/reverse-dependency-app/')
          ? { count: 1, top: [appSummary('ordinary-app', 'Ordinary App')] }
          : { count: 0, top: [] },
        dependsOn: { macos: null, arch: [], formulae: [], casks: [] }
      };
    } else if (path === '/api/v1/packages/cask/cleanmymac/releases') {
      const records =
        query.get('onlyWithNotes') === 'true' ? repairReleases.filter(release => release.hasNotes) : repairReleases;
      const size = Number(query.get('size') ?? 24);
      data = {
        indexing: pageDecisions('cleanmymac', 'en-US', ['en-US'], 5).versions,
        current: 1,
        size,
        total: records.length,
        records: records.slice(0, size),
        stats: { count30d: 0, cadence: 'irregular', brewLag: { medianMinutes: null, earlierCount: 0, compared: 0 } }
      };
    } else if (
      [
        '/api/v1/packages/cask/plus-app++/releases',
        '/api/v1/packages/cask/ordinary-app/releases',
        '/api/v1/packages/cask/variant-app@beta/releases',
        '/api/v1/packages/cask/opennavo/releases',
        '/api/v1/packages/cask/seo-override/releases'
      ].includes(path)
    ) {
      const original = query.get('original') === 'true';
      const locale = query.get('locale');
      const notesCount = original ? 2 : locale === 'zh-CN' ? 1 : 3;
      const total = query.get('onlyWithNotes') === 'true' ? notesCount : 3;
      const size = Number(query.get('size') ?? 20);
      data = {
        // ReleasePage now carries its own locale-specific decision, independently of the detail.
        indexing: pageDecisions(path.split('/')[5], locale ?? 'en-US', ['en-US', 'zh-CN'], notesCount).versions,
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
          bodyMarkdown: `Release fixture notes${hotVersion ? ` revision ${hotVersion}` : ''}`,
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
        indexing: pageDecisions('lumen-notes', query.get('locale') ?? 'en-US', ['en-US'], 1).versions,
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
        stats: {
          count30d: 1,
          cadence: 'irregular',
          brewLag: { medianMinutes: null, earlierCount: 0, compared: 0 }
        }
      };
    } else if (path === '/api/v1/desktop/releases/latest') {
      data = {
        version: '0.3.0',
        channel: 'stable',
        pubDate: '2026-09-30T08:00:00Z',
        notes: 'Improve stability.',
        minMacos: '13.0',
        downloads: [
          {
            target: 'dmg-universal',
            url: 'https://example.test/OpenNavo.dmg',
            bytes: 1000,
            sha256: 'a'.repeat(64)
          }
        ]
      };
    } else if (
      match &&
      [
        ...seoApps,
        ...qaTokens,
        'versioned-app@11',
        'plus-app++',
        'ordinary-app',
        'image-alt-app',
        'dependency-content-app',
        'reverse-dependency-app',
        'thin-app',
        'font-sample',
        'facts-app',
        'wechat',
        'notion',
        'cleanmymac',
        'repair-empty',
        'repair-disabled',
        'install-paused',
        'install-deprecated',
        'guard-app',
        'variant-app@beta',
        'opennavo'
      ].includes(match[1])
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
            version: token === 'ordinary-app' && hotVersion ? hotVersion : '11.0',
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
            screenshots:
              token === 'image-alt-app'
                ? ['  Export settings with format and quality controls  ', ' \t\u00a0\u3000 '].map(
                    (caption, index) => ({
                      url: `https://example.test/screenshots/image-alt-${index}.png`,
                      thumbUrl: `https://example.test/screenshots/image-alt-${index}-thumb.png`,
                      width: 1280,
                      height: 800,
                      caption,
                      theme: 'light'
                    })
                  )
                : [],
            releaseCount: 0,
            releaseStats: { count30d: 0, cadence: 'unknown', brewLag: null },
            sourceUrl: 'https://example.test/source',
            formulaeUrl: 'https://example.test/package',
            sourceLocale: 'en-US',
            machineTranslated: token === 'ordinary-app',
            ...(indexing[token] ? { indexing: indexing[token] } : {}),
            ...(['dependency-content-app', 'reverse-dependency-app'].includes(token)
              ? {
                  indexing: { locales: ['en-US', 'zh-CN'], notesCount: 0 },
                  dependsOn: {
                    macos: null,
                    arch: [],
                    formulae: [],
                    casks: token === 'dependency-content-app' ? ['ordinary-app'] : []
                  }
                }
              : {}),
            ...(['notion', 'cleanmymac', 'repair-empty', 'repair-disabled'].includes(token)
              ? {
                  ...factsApp,
                  displayName: 'SEO Repair Fixture',
                  names: ['SEO Repair Fixture'],
                  developer: 'Notion Labs, Inc.',
                  releaseCount: 24,
                  latestRelease: token === 'repair-empty' ? repairReleases[2] : repairReleases[0],
                  versionCommittedAt: '2026-10-01T00:00:00Z',
                  ...(token === 'repair-empty' ? { price: { ...factsApp.price, amount: null } } : {}),
                  ...(token === 'repair-disabled'
                    ? {
                        disabled: true,
                        homebrewStatus: { state: 'disabled', reason: 'discontinued', date: '2026-10-01' }
                      }
                    : {})
                }
              : {}),
            ...(token === 'ordinary-app' ? { articles: [roundupCard(false)] } : {}),
            ...(token === 'install-paused'
              ? {
                  disabled: true,
                  recommendationPaused: true,
                  homebrewStatus: { state: 'active' },
                  disable: { reason: 'stale' }
                }
              : {}),
            ...(token === 'install-deprecated'
              ? {
                  deprecated: true,
                  homebrewStatus: { state: 'deprecated', reason: 'unmaintained' },
                  deprecation: { reason: 'unmaintained' }
                }
              : {}),
            ...(token === 'qa-spec-metadata'
              ? {
                  version: '2.0.0',
                  license: 'Apache-2.0',
                  supports: { status: 'known', arm64: true, x86_64: true, requiresRosetta: true },
                  platforms: [
                    { tag: 'arm64_sonoma', arch: 'arm64', macos: '14', version: '2.0.0', minMacos: '14' },
                    { tag: 'ventura', arch: 'x86_64', macos: '13', version: '1.0.0', minMacos: '12' }
                  ],
                  repository: {
                    url: 'https://github.com/acme/example',
                    stars: 999950,
                    pushedAt: '2025-10-30T00:00:00Z',
                    signalsCheckedAt: '2026-10-02T00:00:00Z'
                  }
                }
              : {}),
            ...(token === 'qa-invalid-repository'
              ? {
                  repository: {
                    url: 'https://github.com/acme/example',
                    stars: 12345,
                    signalsCheckedAt: 'not-a-date',
                    pushedAt: 'not-a-date'
                  }
                }
              : {}),
            ...(token === 'facts-app' || token === 'seo-uninstall' ? factsApp : {}),
            ...(token.startsWith('seo-') ? { displayName: 'SEO App', names: ['SEO App'] } : {}),
            ...(token === 'seo-disabled'
              ? { disabled: true, homebrewStatus: { state: 'disabled', reason: 'discontinued', date: '2026-09-01' } }
              : {}),
            // The API returns the override of the requested language only, or null.
            ...(seoOverrides[token]
              ? { seoOverride: seoOverrides[token][req.headers['accept-language'] ?? 'en-US'] ?? null }
              : {}),
            ...(token === 'seo-free'
              ? {
                  price: {
                    type: 'free',
                    amount: 0,
                    currency: 'USD',
                    region: 'US',
                    plan: 'Personal',
                    sourceUrl: 'https://vendor.example/pricing',
                    verifiedAt: '2025-12-01T08:00:00Z',
                    stale: false
                  }
                }
              : {}),
            // A security-category app, which points to OpenNavo's own malware scanning.
            ...(token === 'guard-app'
              ? {
                  categories: [{ slug: 'security', name: 'Security & Privacy', icon: 'lucide:shield' }]
                }
              : {}),
            ...wechat,
            // A Homebrew variant named like its main app (aerial@beta is "Aerial"), with every intent page open.
            ...(token === 'variant-app@beta'
              ? {
                  displayName: 'Variant App',
                  names: ['Variant App'],
                  artifacts: factsApp.artifacts,
                  alternatives: factsApp.alternatives
                }
              : {}),
            ...(token === 'opennavo' ? { displayName: 'OpenNavo', names: ['OpenNavo'] } : {})
          };
    } else if (path === '/api/v1/sitemap/packages') {
      sitemapPackageRequests++;
      status = sitemapState === 'error' ? 503 : 200;
      const records =
        query.get('group') === 'fonts'
          ? [
              {
                kind: 'cask',
                token: 'font-sample',
                isFont: true,
                notesCount: 5,
                locales: [{ locale: 'en-US', lastmod: '2026-10-01T10:00:00Z' }]
              }
            ]
          : [
              {
                kind: 'cask',
                token: 'ordinary-app',
                updatedAt: '2026-10-07T00:00:00Z',
                isFont: false,
                notesCount: 5,
                locales: [
                  { locale: 'en-US', lastmod: '2025-03-18T12:00:00.123456+02:00' },
                  { locale: 'zh-CN', lastmod: null }
                ]
              },
              // OpenNavo's own entry answers a 301 to /download, so no sitemap lists it.
              {
                kind: 'cask',
                token: 'opennavo',
                updatedAt: '2026-10-07T00:00:00Z',
                isFont: false,
                notesCount: 5,
                locales: [
                  { locale: 'en-US', lastmod: '2026-10-01T10:00:00Z' },
                  { locale: 'zh-CN', lastmod: '2026-10-01T10:00:00Z' }
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
      const all =
        query.get('type') === 'tutorial'
          ? [tutorialCard]
          : [roundupCard(zh), tutorialCard, { ...tutorialCard, slug: 'third-article', title: 'Third article' }];
      const total = query.get('type') ? all.length : 13;
      data = {
        current,
        size: 12,
        total,
        records: current === 1 ? all : current === 2 ? [tutorialCard] : []
      };
    } else if (path === '/api/v1/articles/image-alternatives') {
      data = {
        state: 'published',
        slug: 'image-alternatives',
        article: {
          ...roundup(req.headers['accept-language'] === 'zh-CN'),
          slug: 'image-alternatives',
          cover: tutorialCard.cover,
          bodyMarkdown:
            '![Export settings with format and quality controls](https://example.test/images/export-settings.png)\n\n![   ](https://example.test/images/uncaptioned.png)',
          images: [
            { url: 'https://example.test/images/export-settings.png', width: 1280, height: 800 },
            { url: 'https://example.test/images/uncaptioned.png', width: 1280, height: 800 }
          ]
        }
      };
    } else if (path === '/api/v1/articles/choose-a-screenshot-app') {
      data = {
        state: 'published',
        slug: 'choose-a-screenshot-app',
        article: roundup((query.get('locale') ?? req.headers['accept-language']) === 'zh-CN')
      };
    } else if (path === '/api/v1/articles/provenance-check') {
      const locale = query.get('locale') ?? req.headers['accept-language'] ?? 'en-US';
      data = {
        state: 'published',
        slug: 'provenance-check',
        article: {
          ...roundup(false),
          slug: 'provenance-check',
          locale,
          availableLocales: [...SITE_LOCALES],
          machineTranslated: true
        }
      };
    } else if (path === '/api/v1/articles/old-slug') {
      data = { state: 'moved', slug: 'choose-a-screenshot-app' };
    } else if (path === '/api/v1/articles/pulled-article') {
      data = { state: 'withdrawn', slug: 'pulled-article' };
    } else if (path === '/api/v1/articles/editorial-note') {
      data = {
        state: 'published',
        slug: 'editorial-note',
        article: {
          ...roundup(false),
          slug: 'editorial-note',
          author: { ...author, slug: 'navo', name: 'Navo', bio: 'An AI-assisted editorial byline.' },
          packages: [appSummary('facts-app', 'Facts App')]
        }
      };
    } else if (path === '/api/v1/authors/navo') {
      data = {
        author: { ...author, slug: 'navo', name: 'Navo', bio: 'An AI-assisted editorial byline.' },
        articles: { current: 1, size: 12, total: 1, records: [tutorialCard] }
      };
    } else if (path === '/api/v1/authors/lin-wei') {
      data = {
        author,
        articles: { current: 1, size: 12, total: 1, records: [roundupCard(false)] }
      };
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
        body: 'Collection introduction.',
        coverUrl: null,
        itemCount: 0,
        updatedAt: '2026-10-01T00:00:00Z',
        sourceLocale: 'en-US',
        machineTranslated: true,
        items: []
      };
    } else if (path === '/api/v1/browse') {
      // T1 A–Z directory: the root lists buckets, a leaf lists its apps (BE-1 contract in be1.md).
      const apps = [
        { token: 'ordinary-app', displayName: 'Ordinary App' },
        { token: 'plus-app++', displayName: 'Plus App++' }
      ];
      const bucket = query.get('bucket');
      const inBucket = key => apps.filter(app => app.displayName.toLowerCase().startsWith(key));
      // Names sort by the language they are shown in: the German directory has no "p" bucket.
      const keys = query.get('locale') === 'de-DE' ? ['o'] : ['o', 'p'];
      if (query.get('kind') === 'fonts') data = { buckets: [], items: [] };
      else if (!bucket)
        data = {
          buckets: keys.map(key => ({ key, count: inBucket(key).length, pages: 1 })),
          items: []
        };
      else if (keys.includes(bucket) && inBucket(bucket).length && (query.get('page') ?? '1') === '1')
        data = { buckets: [], items: inBucket(bucket) };
      else status = 404;
    } else if (path === '/api/v1/background-items') {
      // The footer's encyclopedia link participates in the three-hop crawl.
      data = {
        current: Number(query.get('current') ?? 1),
        size: 24,
        total: backgroundItemsReady ? backgroundLabels.length : 0,
        records: backgroundItemsReady ? backgroundLabels.map(label => ({ label, indexable: true })) : []
      };
    } else if (path.startsWith('/api/v1/background-items/')) {
      const label = path.slice('/api/v1/background-items/'.length);
      requestedLabels.push(label);
      if (!backgroundLabels.includes(label)) status = 404;
      else
        data = {
          label,
          apps: ['Google Drive', 'Vitamin-R 4'].includes(label)
            ? [
                {
                  package: {
                    ...appSummary('ordinary-app', 'Ordinary App'),
                    iconUrl: 'https://example.test/icons/background.png',
                    iconVariants:
                      label === 'Google Drive'
                        ? [
                            { width: 64, url: 'https://example.test/icons/background-64.webp' },
                            { width: 128, url: 'https://example.test/icons/background-128.webp' }
                          ]
                        : []
                  },
                  kind: 'launchctl',
                  phase: 'uninstall',
                  evidence: evidence('https://vendor.example/helper'),
                  uninstallUrl: 'opennavo://uninstall/ordinary-app?preview=1'
                }
              ]
            : [],
          developers: ['Example Inc'],
          shared: false,
          ownership: 'declared_candidates',
          identityChecks: ['signature', 'path', 'version'],
          content: {
            locale: query.get('locale') ?? 'en-US',
            description: 'A fixture background helper.',
            itemType: 'launch_agent',
            commonPaths: [],
            evidence: evidence('https://vendor.example/helper')
          },
          indexable: true,
          startupUrl: `opennavo://startup?label=${encodeURIComponent(label)}`
        };
    } else if (path === '/api/v1/site/settings') {
      data = siteSettings;
    } else if (path === '/api/v1/config/client') {
      // The about page's client config; the editable about content is optional and left out here.
      data = {
        mirrors: [],
        announcement: null,
        minSupportedVersion: '0.1.0',
        latestVersion: '0.3.0',
        features: {},
        links: {
          web: 'https://example.test',
          download: 'https://example.test/download',
          feedback: 'https://example.test/feedback',
          privacy: 'https://example.test/about'
        }
      };
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
    if (data?.token && data.artifacts) {
      data.facts = factsFrom(data);
      if (data.token === 'qa-spec-metadata')
        data.facts.security.identities.push({
          developer: 'Verified Developer',
          teamId: 'EXAMPLE123',
          channels: [],
          openSource: 'unknown',
          evidence: evidence('https://example.test/identity')
        });
      data.collections = [];
      data.recommendationPaused ??= false;
      if (data.token.startsWith('qa-lifecycle-'))
        Object.assign(data, lifecycleCases[Number(data.token.slice('qa-lifecycle-'.length))].fields);
      // L1: current entity decisions can open all ten locales; keep the explicit legacy fixture.
      if (!data.indexing && data.token !== 'versioned-app@11')
        data.indexing = { locales: Object.keys(localePrefixes), notesCount: data.releaseCount };
      if (data.indexing)
        data.indexing.pages = pageDecisions(
          data.token,
          query.get('locale') ?? req.headers['accept-language'] ?? 'en-US',
          data.indexing.locales,
          data.indexing.notesCount,
          ['dependency-content-app', 'reverse-dependency-app'].includes(data.token),
          data.isFont,
          siteSettings.pages.fontsSitemap
        );
      if (data.token === 'variant-app@beta') {
        // Three verified known issues open the troubleshooting page; the other intent pages follow the overview.
        data.facts.issues = ['Sync stalls', 'The window opens off-screen', 'Sign-in loops'].map(title => ({
          locale: 'en-US',
          title,
          symptom: `${title} after an update.`,
          appVersions: ['11.0'],
          macosVersions: ['15.0'],
          steps: ['Quit the app and open it again.'],
          communityLinks: [],
          evidence: evidence('https://vendor.example/issues')
        }));
        for (const page of ['alternatives', 'uninstall', 'troubleshooting'])
          data.indexing.pages[page] = data.indexing.pages.overview;
      }
    }
    if (data?.token === 'facts-app' && data.facts) {
      data.facts.uninstall.push(
        ...backgroundLabels.map(value => ({
          kind: 'launchctl',
          phase: 'uninstall',
          value,
          evidence: evidence('https://vendor.example/helper')
        }))
      );
    }
    if (path === '/api/v1/sitemap/packages' && data)
      for (const record of data.records)
        for (const localized of record.locales) {
          localized.pages = pageDecisions(
            record.token,
            localized.locale,
            record.locales.map(item => item.locale),
            record.notesCount,
            false,
            record.isFont,
            siteSettings.pages.fontsSitemap
          );
          localized.pageLastmod = {
            overview: localized.lastmod,
            ...(record.token === 'ordinary-app' ? {} : { versions: localized.lastmod })
          };
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
  // Keep the internal listener out of macOS's ephemeral client-port range, and check the
  // same wildcard address the child binds; startup API traffic cannot steal this port.
  let internalReservation;
  let internalPort;
  for (let attempt = 0; attempt < 100; attempt++) {
    internalReservation = createPortReservation();
    internalPort = String(20000 + ((Number(port) + attempt) % 10000));
    internalReservation.listen(Number(internalPort), '0.0.0.0');
    try {
      await once(internalReservation, 'listening');
      break;
    } catch (error) {
      if (error.code !== 'EADDRINUSE' || attempt === 99) throw error;
    }
  }
  await new Promise(resolve => internalReservation.close(resolve));
  const internal = `http://127.0.0.1:${internalPort}`;
  const internalSecret = 'test-only-internal-secret-'.repeat(2);
  // Test-only clock: SIGUSR2 moves the server's Date.now() an hour ahead, so cached pages can expire
  // without waiting for their TTL.
  const clock = `data:text/javascript,${encodeURIComponent(
    'const now = Date.now.bind(Date); let offset = 0; process.on("SIGUSR2", () => { offset += 3600000; }); Date.now = () => now() + offset;'
  )}`;
  const rendererEnv = {
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
    NUXT_IMAGE_REDIS_URL: '',
    NUXT_LLMS_ENABLED: 'true',
    NUXT_PUBLIC_SITE_URL: 'https://example.test',
    NUXT_PUBLIC_I18N_BASE_URL: 'https://example.test',
    NUXT_PUBLIC_CDN_BASE: 'https://example.test/assets',
    NUXT_METRICS_PORT: internalPort,
    NUXT_INTERNAL_SECRET: internalSecret,
    OPENNAVO_SITEMAP_WARMUP: 'false'
  };
  const child = spawn(process.execPath, ['--import', clock, entry], {
    env: rendererEnv,
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
  // Optional build-compatibility check: both outputs render against this exact mock API and config.
  // Only their listening ports differ. No timestamps, nonces or payload fields are normalized away.
  if (process.env.ONV_COMPARE_ENTRY) {
    await t.test('cross-build cache identity follows the effective OG signing secret', async () => {
      const reserved = createPortReservation();
      reserved.listen(0, '127.0.0.1');
      await once(reserved, 'listening');
      const comparePort = String(reserved.address().port);
      await new Promise(resolve => reserved.close(resolve));
      const metricsReserved = createPortReservation();
      metricsReserved.listen(0, '127.0.0.1');
      await once(metricsReserved, 'listening');
      const compareMetricsPort = String(metricsReserved.address().port);
      await new Promise(resolve => metricsReserved.close(resolve));
      const compare = spawn(process.execPath, ['--import', clock, process.env.ONV_COMPARE_ENTRY], {
        env: {
          ...rendererEnv,
          PORT: comparePort,
          NITRO_PORT: comparePort,
          NUXT_METRICS_PORT: compareMetricsPort,
          OPENNAVO_SITEMAP_WARMUP: 'false'
        },
        stdio: ['ignore', 'pipe', 'pipe']
      });
      let compareLogs = '';
      const compareExit = once(compare, 'exit');
      compare.stderr.on('data', chunk => {
        compareLogs += chunk;
      });
      try {
        await new Promise((resolve, reject) => {
          const timeout = setTimeout(
            () => reject(new Error(`Comparison renderer did not start: ${compareLogs}`)),
            15000
          );
          compare.once('error', error => {
            clearTimeout(timeout);
            reject(error);
          });
          compare.once('exit', () => {
            clearTimeout(timeout);
            reject(new Error(compareLogs));
          });
          compare.stdout.on('data', chunk => {
            compareLogs += chunk;
            if (compareLogs.includes('Listening on')) {
              clearTimeout(timeout);
              resolve();
            }
          });
        });
        const getBuild = async base => {
          const response = await fetch(`${base}/cache/build`, {
            headers: { authorization: `Bearer ${internalSecret}` }
          });
          assert.equal(response.status, 200);
          return (await response.json()).build;
        };
        const firstBuild = await getBuild(internal);
        const secondBuild = await getBuild(`http://127.0.0.1:${compareMetricsPort}`);
        assert.match(firstBuild, /^[a-f0-9]{64}\.[a-f0-9]{12}$/);
        assert.equal(firstBuild.split('.')[0], secondBuild.split('.')[0], 'web input identity');
        if (!rendererEnv.NUXT_OG_IMAGE_SECRET) {
          assert.notEqual(firstBuild, secondBuild, 'generated signing keys must retire old HTML');
          return;
        }
        assert.equal(firstBuild, secondBuild, 'stable runtime signing key preserves the cache identity');
        const comparisons = [];
        for (const path of [
          '/',
          '/apps/ordinary-app',
          '/zh/apps/ordinary-app',
          '/apps/ordinary-app/details',
          '/apps/ordinary-app/dependencies'
        ]) {
          const first = await fetch(`${origin}${path}`, { headers: { accept: 'text/html' } });
          const second = await fetch(`http://127.0.0.1:${comparePort}${path}`, { headers: { accept: 'text/html' } });
          assert.equal(first.status, 200, `${path}: ${logs}`);
          assert.equal(second.status, 200, `${path}: ${compareLogs}`);
          const digest = html => createHash('sha256').update(html).digest('hex');
          const firstHtml = await first.text();
          const secondHtml = await second.text();
          const ogImage = html => /<meta[^>]*property="og:image"[^>]*content="([^"]*)"/.exec(html)?.[1];
          assert.match(ogImage(firstHtml) ?? '', /ch_/, `${path}: OG template hash is present`);
          comparisons.push({
            path,
            html: digest(firstHtml) === digest(secondHtml),
            ogImage: ogImage(firstHtml) === ogImage(secondHtml)
          });
          if (firstHtml !== secondHtml) {
            let offset = 0;
            while (firstHtml[offset] === secondHtml[offset] && offset < firstHtml.length) offset++;
            t.diagnostic(
              JSON.stringify({
                path,
                firstDifference: offset,
                first: firstHtml.slice(Math.max(0, offset - 50), offset + 120),
                second: secondHtml.slice(Math.max(0, offset - 50), offset + 120)
              })
            );
          }
        }
        assert.deepEqual(
          comparisons,
          comparisons.map(({ path }) => ({ path, html: true, ogImage: true })),
          'complete HTML, embedded payloads and og:image URLs must match'
        );
      } finally {
        compare.kill('SIGTERM');
        const timeout = setTimeout(() => compare.kill('SIGKILL'), 5000);
        try {
          await compareExit;
        } finally {
          clearTimeout(timeout);
        }
      }
    });
  }
  const invalidatePaths = async (paths, settings = false) => {
    const response = await fetch(`${internal}/cache/invalidate`, {
      method: 'POST',
      headers: { authorization: `Bearer ${internalSecret}`, 'content-type': 'application/json' },
      body: JSON.stringify({ paths, settings })
    });
    assert.equal(response.status, 200, `invalidate ${paths.join(', ')}\n${logs}`);
    return response.json();
  };
  const fontBucketPath = `/__sitemap__/${packageSitemapName('fonts', 'font-sample')}.xml`;
  const setFontsSitemap = async enabled => {
    siteSettings.pages.fontsSitemap = enabled;
    siteSettings.revision += 1;
    const result = await invalidatePaths(['/', '/apps/font-sample', '/sitemap_index.xml', fontBucketPath], true);
    assert.equal(result.settingsCleared, true);
  };
  await t.test(
    'SSR images keep descriptive alternatives and explicitly decorative empty alt in both languages',
    async () => {
      let contentImages = 0;
      let decorations = 0;
      for (const prefix of ['', '/zh']) {
        for (const route of [
          '/',
          '/download',
          '/discover',
          '/apps',
          '/categories/utilities',
          '/collections/editors',
          '/apps/image-alt-app',
          labelPath('Google Drive'),
          '/articles',
          '/articles/image-alternatives',
          '/authors/lin-wei'
        ]) {
          const path = route === '/' ? prefix || '/' : prefix + route;
          const response = await fetch(`${origin}${path}`, { headers: { accept: 'text/html' } });
          assert.equal(response.status, 200, `${path}\n${logs}`);
          const images = assertImageAlternatives(await response.text(), path);
          contentImages += images.filter(image => image.getAttribute('alt').trim()).length;
          decorations += images.filter(image => image.getAttribute('alt') === '').length;
          if (route === '/apps/image-alt-app') {
            const screenshots = images.filter(image => image.getAttribute('src').includes('/screenshots/'));
            assert.deepEqual(
              screenshots.map(image => image.getAttribute('alt')),
              [
                'Export settings with format and quality controls',
                prefix ? 'Example App @11 · 截图 2' : 'Example App @11 · Screenshot 2'
              ]
            );
          }
          if (route === '/articles/image-alternatives') {
            assert.ok(images.some(image => image.getAttribute('src').endsWith('/export-settings.png')));
            assert.ok(!images.some(image => image.getAttribute('src').endsWith('/uncaptioned.png')));
          }
        }
      }
      assert.ok(contentImages > 0, 'screenshots, article covers and body images are exercised');
      assert.ok(decorations > 0, 'app icons and repeated brand images are exercised');
    }
  );
  await t.test(
    'disabled installation has an explanation; deprecated and paused commands retain a warning',
    async () => {
      const disabled = JSDOM.fragment(await (await fetch(`${origin}/apps/repair-disabled`)).text());
      const blocked = disabled.querySelector('[data-testid="package-install-command"]');
      assert.ok(blocked?.textContent.includes('Homebrew has disabled this package: discontinued'));
      assert.equal(blocked.querySelector('code, button'), null, 'no command or copy control for a disabled app');
      for (const token of ['install-paused', 'install-deprecated']) {
        const doc = JSDOM.fragment(await (await fetch(`${origin}/apps/${token}`)).text());
        const card = doc.querySelector('[data-testid="package-install-command"]');
        const warning = card?.querySelector('[role="note"]');
        const command = card?.querySelector('code');
        assert.ok(
          warning?.textContent.includes(
            token === 'install-paused' ? 'Homebrew still offers this app' : 'Homebrew has deprecated'
          )
        );
        assert.equal(command?.textContent.trim(), `brew install --cask ${token}`);
        assert.ok(warning.compareDocumentPosition(command) & 4, 'warning precedes command');
        assert.ok(!card.className.includes('hidden'), 'same command is available at mobile widths');
      }
    }
  );
  for (const prefix of ['', '/zh', '/ja', '/es', '/pt', '/ru']) {
    await t.test(`${prefix || '/en (root)'} versioned detail`, async () => {
      const path = `${prefix}/apps/versioned-app@11`;
      const response = await fetch(`${origin}${path}`, { headers: { accept: 'text/html' } });
      const html = await response.text();
      assert.equal(response.status, 200, `${path}\n${logs}`);
      assert.match(html, /<title>[^<]*Example App @11[^<]*OpenNavo<\/title>/);
      // S1/C3: an absent subtitle leaves a Vue conditional comment after the exact heading.
      assert.match(html, /<h1\b[^>]*>Example App @11(?:<!--(?:v-if)?-->)?<\/h1>/);
      // The shared command card can include an empty CSS wrap marker. Its command still appears once,
      // in one text node, preserving the literal @ and the complete text copied to Terminal.
      const commands = [...JSDOM.fragment(html).querySelectorAll('main pre code')].filter(
        code => code.textContent.trim() === 'brew install --cask versioned-app@11'
      );
      assert.equal(commands.length, 1, `${path}: one complete install command in the overview`);
      assert.deepEqual(
        [...commands[0].childNodes].filter(node => node.nodeType === 3).map(node => node.textContent.trim()),
        ['brew install --cask versioned-app@11'],
        `${path}: the command remains one copyable text node`
      );
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
  await t.test('background labels render once decoded, with canonical links and no redirect loops', async () => {
    for (const prefix of ['', '/zh', '/zh-hant']) {
      for (const label of backgroundLabels) {
        const path = prefix + labelPath(label);
        const response = await fetch(`${origin}${path}`, { redirect: 'manual' });
        assert.equal(response.status, 200, `${path}\n${logs}`);
        const html = await response.text();
        assert.equal(/<link[^>]*rel="canonical"[^>]*href="([^"]+)"/.exec(html)?.[1], `https://example.test${path}`);
        assert.ok(requestedLabels.includes(label), `API received literal label ${label}`);
        if (['Google Drive', 'Vitamin-R 4'].includes(label)) {
          const icon = /<img\b[^>]*src="https:\/\/example\.test\/icons\/background\.png"[^>]*>/.exec(html)?.[0] ?? '';
          assert.ok(icon, 'the declaring app keeps its original icon as fallback');
          assert.match(html, /style="width:40px;height:40px;?"/, 'the icon renders at 40px');
          if (label === 'Google Drive') {
            assert.ok(icon.includes('sizes="40px"'), 'sizes matches the actual 40px box');
            assert.ok(
              icon.includes(
                'srcset="https://example.test/icons/background-64.webp 64w, https://example.test/icons/background-128.webp 128w"'
              )
            );
          } else {
            assert.ok(!icon.includes('srcset='), 'historical icons without variants keep working');
          }
        }
        for (const link of html.match(/<link\b[^>]*hreflang="[^"]+"[^>]*>/g) ?? [])
          assert.equal(/href="([^"]+)"/.exec(link)?.[1], `https://example.test${path}`, 'hreflang uses labelPath too');
        // A second request exercises the SWR cache with the encoded key.
        assert.equal((await fetch(`${origin}${path}`, { redirect: 'manual' })).status, 200, path);
      }
      const raw = `${prefix}/background-items/com.steinberg.dorico.*`;
      const canonical = prefix + labelPath('com.steinberg.dorico.*');
      const alias = await fetch(`${origin}${raw}`, { redirect: 'manual' });
      assert.equal(alias.status, 301);
      assert.equal(alias.headers.get('location'), canonical);
      assert.equal((await fetch(`${origin}${raw}`)).status, 200);
    }
    const before = requestedLabels.length;
    for (const segment of ['bad%ZZ', 'a%2Fb', 'a%5Cb', 'a%3Fb', 'a%23b', 'a%00b'])
      assert.equal((await fetch(`${origin}/background-items/${segment}`, { redirect: 'manual' })).status, 404, segment);
    assert.equal(requestedLabels.length, before, 'invalid labels never reach the API');
    for (const prefix of ['', '/zh']) {
      const html = await (await fetch(`${origin}${prefix}/apps/facts-app`)).text();
      for (const label of backgroundLabels)
        assert.ok(html.includes(`href="${prefix}${labelPath(label)}"`), `uninstall link for ${label}`);
    }
  });

  await t.test('a variant cask named like its main app says its variant on every page', async () => {
    // variant-app@beta's catalog name is "Variant App", like the main app's (aerial@beta is "Aerial").
    const html = async path => {
      const response = await fetch(`${origin}${path}`, { headers: { accept: 'text/html' } });
      const text = await response.text();
      assert.equal(response.status, 200, `${path}\n${logs}`);
      return text;
    };
    const title = page => /<title>([^<]*)<\/title>/.exec(page)?.[1] ?? '';
    const description = page => /<meta name="description" content="([^"]*)"/.exec(page)?.[1] ?? '';
    const heading = page => (/<h1\b[^>]*>([\s\S]*?)<\/h1>/.exec(page)?.[1] ?? '').replace(/<[^>]*>/g, '');
    const graph = page =>
      [...page.matchAll(/<script type="application\/ld\+json"[^>]*>([\s\S]*?)<\/script>/g)].flatMap(
        match => JSON.parse(match[1])['@graph'] ?? []
      );
    const node = (page, type) => graph(page).find(item => [item['@type']].flat().includes(type));
    const overview = await html('/apps/variant-app@beta');
    assert.equal(title(overview), 'Variant App Beta for Mac – Download &amp; Install · OpenNavo');
    assert.match(overview, /<h1\b[^>]*>Variant App Beta(?:<!--(?:v-if)?-->)?<\/h1>/);
    assert.ok(description(overview).includes('Variant App Beta'), description(overview));
    assert.equal(node(overview, 'SoftwareApplication')?.name, 'Variant App Beta');
    assert.equal(node(overview, 'BreadcrumbList')?.itemListElement.at(-1).name, 'Variant App Beta');
    assert.equal(title(await html('/zh/apps/variant-app@beta')), 'Variant App Beta Mac 版下载安装 · OpenNavo');
    // Tabs and intent pages read the same name, in their titles, descriptions, headings and breadcrumbs.
    for (const page of ['versions', 'details', 'dependencies', 'alternatives', 'uninstall', 'troubleshooting']) {
      const text = await html(`/apps/variant-app@beta/${page}`);
      assert.ok(title(text).includes('Variant App Beta'), `${page} title: ${title(text)}`);
      assert.ok(description(text).includes('Variant App Beta'), `${page} description: ${description(text)}`);
      assert.ok(heading(text).includes('Variant App Beta'), `${page} h1: ${heading(text)}`);
      const crumbs = node(text, 'BreadcrumbList')?.itemListElement.map(item => item.name) ?? [];
      assert.ok(crumbs.includes('Variant App Beta'), `${page} breadcrumb: ${crumbs.join(' › ')}`);
      assert.ok(!/Variant App(?! Beta)/.test(title(text) + description(text)), `${page} never says the bare name`);
    }
  });
  await t.test('lifecycle banners and commands share disabled > deprecated > paused priority', async () => {
    for (const [index, { name, expected }] of lifecycleCases.entries()) {
      const response = await fetch(`${origin}/apps/qa-lifecycle-${index}`);
      const html = await response.text();
      assert.equal(response.status, 200, name + logs);
      const page = JSDOM.fragment(html);
      const banner = page.querySelector('[data-testid="package-lifecycle-banner"]');
      const command = page.querySelector('[data-testid="package-install-command"]');
      assert.equal(Boolean(banner), expected !== 'active', name);
      assert.equal(Boolean(command?.querySelector('code')), expected !== 'disabled', name);
      assert.equal(Boolean(command?.querySelector('button')), expected !== 'disabled', name);
      if (banner) {
        assert.ok(
          banner.classList.contains(expected === 'disabled' ? 'text-status-danger' : 'text-status-warning'),
          name
        );
        assert.equal(command.querySelector('[role="note"]').textContent.trim(), banner.textContent.trim(), name);
      }
    }
  });

  await t.test(
    'deduplicated metadata keeps architectures, non-native status, older builds and identity IDs',
    async () => {
      const response = await fetch(`${origin}/apps/qa-spec-metadata`);
      const html = await response.text();
      assert.equal(response.status, 200, logs);
      const page = JSDOM.fragment(html);
      const card = page.querySelector('[data-testid="package-spec-card"]');
      for (const text of ['Apple silicon', 'Apache-2.0', 'Verified Developer', 'EXAMPLE123'])
        assert.ok(card.textContent.includes(text), text);
      const requirements = page.querySelector('[data-testid="package-requirements"]');
      for (const text of ['Apple silicon', 'Intel', '2.0.0', '1.0.0', 'macOS 12'])
        assert.ok(requirements.textContent.includes(text), text);
      assert.ok(requirements.textContent.includes('Not native on Apple silicon'), requirements.textContent);
      // Bundle IDs and link signatures are checked on the existing Lumen link fixture below.
    }
  );

  await t.test('repository SSR links and descriptions are complete and malformed dates stay hidden', async () => {
    const response = await fetch(`${origin}/apps/qa-spec-metadata`);
    const html = await response.text();
    assert.equal(response.status, 200, logs);
    const page = JSDOM.fragment(html);
    const card = page.querySelector('[data-testid="package-spec-card"]');
    assert.ok(card.textContent.includes('★1.0M'), card.textContent);
    const trigger = card.querySelector('button[aria-describedby]');
    assert.ok(trigger, 'repository tooltip trigger is associated in SSR');
    const description = page.querySelector(`[id="${trigger.getAttribute('aria-describedby')}"]`);
    assert.equal(description?.getAttribute('role'), 'tooltip');
    assert.ok(description.textContent.includes('Oct 2, 2026'), description.textContent);
    for (const link of card.querySelectorAll('a[href^="https://"]')) {
      assert.equal(link.getAttribute('target'), '_blank');
      assert.equal(link.getAttribute('rel'), 'noopener noreferrer');
      assert.ok(link.querySelector('svg'), 'external-link indicator');
    }
    const invalid = await fetch(`${origin}/apps/qa-invalid-repository`);
    const invalidPage = JSDOM.fragment(await invalid.text());
    assert.equal(invalid.status, 200, logs);
    const invalidCard = invalidPage.querySelector('[data-testid="package-spec-card"]');
    assert.ok(invalidCard.textContent.includes('★12.3K'));
    assert.equal(invalidCard.querySelector('button[aria-describedby]'), null);
  });

  await t.test('link apps share /apps and download from the developer instead of installing', async () => {
    const response = await fetch(`${origin}/apps/lumen-notes`, {
      headers: { accept: 'text/html' }
    });
    const html = await response.text();
    assert.equal(response.status, 200, logs);
    const spec = JSDOM.fragment(html).querySelector('[data-testid="package-spec-card"]');
    const valueOf = label =>
      [...spec.querySelectorAll('dt')]
        .find(term => term.textContent.trim() === label)
        ?.nextElementSibling?.textContent.trim();
    assert.equal(valueOf('Latest version'), '2.4.1', 'link quick-info version survives in the SSR spec card');
    assert.equal(
      valueOf('Download source'),
      'lumen.app',
      'link quick-info download source survives in the SSR spec card'
    );
    assert.equal(valueOf('Developer signature'), '7Q2XK4M9PL');
    assert.equal(valueOf('Bundle ID'), 'app.lumen.notes');
    // S1/C3: preserve the exact name while allowing the absent subtitle's Vue comment.
    assert.match(html, /<h1\b[^>]*>Lumen Notes(?:<!--(?:v-if)?-->)?<\/h1>/);
    assert.doesNotMatch(html, /brew install/, 'no install command');
    // Header and panel point at the developer's own addresses, in a new tab.
    assert.match(html, /<a\b[^>]*href="https:\/\/lumen\.app\/download"[^>]*target="_blank"/);
    assert.ok(html.includes('Lumen-2.4.1-arm64.dmg'), 'release files are listed');
    // The raw API value remains inert in the hydration payload; the download UI must never link it.
    assert.ok(!html.includes('javascript:alert'), 'only https files reach the page, as links or in the payload');
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
    const redirect = await fetch(`${origin}/zh/apps/plus-app%2B%2B/versions?source=a%2Bb`, {
      redirect: 'manual'
    });
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
    assert.equal(payload.headers.get('x-robots-tag'), 'noindex');
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
    const response = await fetch(`${origin}/apps/missing-app@0`, {
      headers: { accept: 'text/html' }
    });
    assert.equal(response.status, 404, logs);
    assert.match(await response.text(), /<title>[^<]+<\/title>/);
  });
  for (const path of ['/collections', '/zh/collections', '/zh/collections/']) {
    await t.test(
      `${path} retains cached collections until internal invalidation and never caches failures`,
      async () => {
        // X5: SWR may retain empty collections; the backend invalidates every changed listing.
        const canonicalPath = path.replace(/\/$/, '');
        await invalidatePaths([canonicalPath]);
        collectionState = 'empty';
        const empty = await fetch(`${origin}${path}`);
        assert.equal(empty.status, 200);
        assert.match(empty.headers.get('cache-control') ?? '', /s-maxage=300/);
        assert.ok(!(await empty.text()).includes('Collection recovery marker'));
        collectionState = 'published';
        const reads = collectionRequests;
        const cached = await fetch(`${origin}${path}`);
        assert.equal(cached.status, 200);
        assert.ok(!(await cached.text()).includes('Collection recovery marker'));
        assert.equal(collectionRequests, reads, 'the warm empty result stays cached');
        // Failed responses remain uncached: recovery after the 503 needs no additional purge.
        for (const [state, purge] of [
          ['published', true],
          ['error', true],
          ['published', false]
        ]) {
          collectionState = state;
          if (purge) await invalidatePaths([canonicalPath]);
          const before = collectionRequests;
          const response = await fetch(`${origin}${path}`);
          const html = await response.text();
          assert.equal(response.status, state === 'error' ? 503 : 200);
          if (state === 'error') {
            assert.equal(response.headers.get('retry-after'), '5');
            assert.match(response.headers.get('cache-control') ?? '', /no-(?:store|cache)/);
            assert.equal(response.headers.get('cloudflare-cdn-cache-control'), 'no-store');
          } else assert.match(response.headers.get('cache-control') ?? '', /s-maxage=300/);
          assert.ok(collectionRequests > before, 'invalidated or failed responses must recheck collection data');
          assert.equal(html.includes('Collection recovery marker'), state === 'published');
        }
      }
    );
  }
  await t.test('home directories and disabled desktop cask render in SSR', async () => {
    for (const prefix of ['', '/zh']) {
      const html = await (await fetch(`${origin}${prefix || '/'}`, { headers: { accept: 'text/html' } })).text();
      assert.ok(!html.includes('brew install --cask opennavo'));
      for (const [kind, minimum] of [
        ['categories', 6],
        ['collections', 4],
        ['articles', 3]
      ]) {
        const links = [...html.matchAll(new RegExp(`href="(${prefix}/${kind}/[a-z0-9-]+)"`, 'g'))].map(
          match => match[1]
        );
        assert.ok(new Set(links).size >= minimum, `${prefix}/${kind}: ${links}`);
      }

      const download = await (await fetch(`${origin}${prefix}/download`, { headers: { accept: 'text/html' } })).text();
      assert.ok(!download.includes('brew install --cask opennavo'));
    }
    const app = await (await fetch(`${origin}/apps/facts-app`, { headers: { accept: 'text/html' } })).text();
    assert.match(app, /<button[^>]*aria-label="Get Facts App"/);
  });
  await t.test('both layouts start with a skip link to a focusable main', async () => {
    for (const path of ['/', '/zh', '/download', '/zh/download']) {
      const html = await (await fetch(`${origin}${path}`, { headers: { accept: 'text/html' } })).text();
      assert.equal(/<a\b[^>]*href="([^"]+)"/.exec(html)?.[1], '#main-content', path);
      const main = /<main[^>]*>/.exec(html)?.[0] ?? '';
      assert.match(main, /id="main-content"/, path);
      assert.match(main, /tabindex="-1"/, path);
    }
  });
  await t.test('schema describes the editorial organization, app references and open languages', async () => {
    const graphOf = async path => {
      const html = await (await fetch(`${origin}${path}`, { headers: { accept: 'text/html' } })).text();
      return JSON.parse(/<script type="application\/ld\+json"[^>]*>([\s\S]*?)<\/script>/.exec(html)?.[1] ?? '{}')[
        '@graph'
      ];
    };
    const nodeOf = (graph, type) => graph.find(node => [node['@type']].flat().includes(type));
    const about = await graphOf('/about');
    const org = nodeOf(about, 'Organization');
    assert.ok(org.description.startsWith('OpenNavo is a free, open-source Homebrew GUI for Mac:'));
    const contact = [org.contactPoint].flat()[0];
    assert.equal(
      (about.find(node => node['@id'] === contact?.['@id']) ?? contact).url,
      'https://example.test/feedback'
    );
    assert.equal(nodeOf(about, 'AboutPage').mainEntity['@id'], org['@id']);
    for (const path of ['/authors/navo', '/articles/editorial-note']) {
      const graph = await graphOf(path);
      assert.ok(!nodeOf(graph, 'Person'));
      if (path.startsWith('/authors')) assert.equal(nodeOf(graph, 'ProfilePage').mainEntity['@id'], org['@id']);
      else {
        const post = nodeOf(graph, 'BlogPosting');
        assert.equal([post.author].flat()[0]['@id'], org['@id']);
        assert.equal(post.inLanguage, nodeOf(graph, 'WebPage').inLanguage);
        for (const ref of [post.about, ...post.mentions]) {
          const app = graph.find(node => node['@id'] === ref['@id']) ?? ref;
          assert.equal(app['@type'], 'SoftwareApplication');
        }
      }
    }
    const app = nodeOf(await graphOf('/apps/facts-app'), 'SoftwareApplication');
    assert.deepEqual([app.alternateName].flat(), ['Facts Alternate']);
    try {
      siteSettings.pages.openLocales = ['en-US', 'zh-CN', 'ja-JP', 'es-ES', 'ru-RU'];
      await invalidatePaths(['/download'], true);
      const graph = await graphOf('/download');
      const client = nodeOf(graph, 'SoftwareApplication');
      assert.equal(client.softwareRequirements, 'macOS 13.0 or later');
      assert.equal([client.image].flat()[0]['@id'], 'https://example.test/#logo');
      assert.equal(
        graph.filter(node => node['@type'] === 'ImageObject' && node.url?.endsWith('/icon-512.png')).length,
        1
      );
      for (const node of [client, nodeOf(graph, 'WebSite')])
        assert.deepEqual(node.inLanguage, ['en', 'zh-Hans', 'ja', 'es', 'ru']);
    } finally {
      delete siteSettings.pages.openLocales;
      await invalidatePaths(['/download'], true);
    }
  });
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
  await t.test('every page has one WebSite, Organization and WebPage, and every @id reference resolves', async () => {
    // T4: the graph of each page is self-contained, and its WebPage is the page's canonical URL.
    const pages = [
      '/',
      '/apps/ordinary-app',
      '/zh/apps/ordinary-app',
      '/apps/ordinary-app/versions',
      '/apps/opennavo/versions',
      '/apps/lumen-notes',
      '/categories/utilities',
      '/articles',
      '/articles/choose-a-screenshot-app',
      '/authors/lin-wei',
      '/download',
      '/browse',
      '/browse/o'
    ];
    const isWebPage = node =>
      [node['@type']]
        .flat()
        .some(type => ['WebPage', 'CollectionPage', 'ItemPage', 'ProfilePage', 'AboutPage', 'FAQPage'].includes(type));
    for (const path of pages) {
      const response = await fetch(`${origin}${path}`, { headers: { accept: 'text/html' } });
      const html = await response.text();
      assert.equal(response.status, 200, `${path}\n${logs}`);
      const graph = [...html.matchAll(/<script type="application\/ld\+json"[^>]*>([\s\S]*?)<\/script>/g)].flatMap(
        match => JSON.parse(match[1])['@graph'] ?? []
      );
      const ids = new Set();
      const references = [];
      const walk = value => {
        if (Array.isArray(value)) return value.forEach(walk);
        if (!value || typeof value !== 'object') return;
        const keys = Object.keys(value);
        if (keys.length === 1 && keys[0] === '@id') references.push(value['@id']);
        else if (value['@id']) ids.add(value['@id']);
        Object.values(value).forEach(walk);
      };
      walk(graph);
      // A subpage names its app by @id only; the app's SoftwareApplication node lives on the overview (T4).
      // OpenNavo's own entry is the desktop app, whose node lives on /download.
      const app = /^\/apps\/([^/]+)\/[a-z]+$/.exec(path)?.[1];
      const appId =
        app && (app === 'opennavo' ? 'https://example.test/#desktop-app' : `https://example.test/apps/${app}#app`);
      for (const id of references)
        assert.ok(ids.has(id) || id === appId, `${path}: ${id} is referenced but not in the graph`);
      if (app) {
        const about = graph.find(isWebPage)?.about;
        assert.deepEqual(about, { '@id': appId }, `${path} about is a reference`);
      }
      const count = type => graph.filter(node => [node['@type']].flat().includes(type)).length;
      assert.equal(count('WebSite'), 1, `${path} WebSite`);
      assert.equal(count('Organization'), 1, `${path} Organization`);
      const webPages = graph.filter(isWebPage);
      assert.equal(webPages.length, 1, `${path} WebPage: ${webPages.map(node => node['@type']).join()}`);
      const canonical = /<link[^>]*rel="canonical"[^>]*href="([^"]+)"/.exec(html)?.[1];
      assert.equal(webPages[0].url, canonical, `${path} WebPage.url`);
    }
  });
  await t.test('every indexable app is within three links of the home page', async () => {
    // T1: breadth-first over the links the server renders, from the English home page.
    const depth = new Map([['/', 0]]);
    let frontier = ['/'];
    for (let level = 1; level <= 3 && frontier.length; level++) {
      const next = [];
      for (const path of frontier) {
        const html = await (await fetch(`${origin}${path}`, { headers: { accept: 'text/html' } })).text();
        for (const [, href] of html.matchAll(/<a\b[^>]*\shref="([^"#?]+)[^"]*"/g)) {
          const target = href.replace(/^https:\/\/example\.test/, '');
          if (!target.startsWith('/') || target.startsWith('//') || depth.has(target)) continue;
          // Stay on English pages; every other language mirrors the same links.
          if (/^\/(zh|zh-hant|ja|es|pt|ru|de|fr|it)(\/|$)/.test(target)) continue;
          depth.set(target, level);
          next.push(target);
        }
      }
      frontier = next;
    }
    for (const app of ['/apps/ordinary-app', '/apps/plus-app++'])
      assert.ok(
        (depth.get(app) ?? Infinity) <= 3,
        `${app} depth ${depth.get(app)}; reached ${[...depth.keys()].join(' ')}`
      );
  });
  await t.test('dependency links follow the server decision while old URLs remain accessible', async () => {
    const get = path => fetch(`${origin}${path}`, { headers: { accept: 'text/html' } });
    const dependencyLinks = (html, path) =>
      [...html.matchAll(/<a\b[^>]*\shref="([^"]+)"/g)].filter(([, href]) => href === path);
    for (const prefix of ['', '/zh', '/ja']) {
      const path = `${prefix}/apps/ordinary-app`;
      const overview = await (await get(path)).text();
      assert.equal(dependencyLinks(overview, `${path}/dependencies`).length, 0, `${path}: empty dependency tab`);
      const response = await get(`${path}/dependencies`);
      assert.equal(response.status, 200, logs);
      assert.match(response.headers.get('x-robots-tag') ?? '', /noindex/);
      const html = await response.text();
      assert.match(html, /<meta name="robots" content="noindex, follow"/);
      assert.equal(/<link[^>]*rel="canonical"[^>]*href="([^"]+)"/.exec(html)?.[1], `https://example.test${path}`);
      assert.equal([...html.matchAll(/<link[^>]+hreflang="/g)].length, 0);
      assert.equal(dependencyLinks(html, `${path}/dependencies`).length, 0);
    }
    // Reverse relations are content too, even with no declared dependency or conflict summary. A tab with
    // content keeps its own canonical and its links, and is noindex in every language without alternates.
    for (const token of ['dependency-content-app', 'reverse-dependency-app']) {
      for (const prefix of ['', '/zh', '/ja']) {
        const path = `${prefix}/apps/${token}`;
        const overview = await (await get(path)).text();
        assert.equal(
          dependencyLinks(overview, `${path}/dependencies`).length,
          token === 'dependency-content-app' ? 2 : 1
        );
        const response = await get(`${path}/dependencies`);
        assert.equal(response.status, 200, logs);
        const html = await response.text();
        assert.equal(
          /<link[^>]*rel="canonical"[^>]*href="([^"]+)"/.exec(html)?.[1],
          `https://example.test${path}/dependencies`
        );
        assert.match(html, /<meta name="robots" content="noindex, follow"/, `${path}/dependencies`);
        assert.match(response.headers.get('x-robots-tag') ?? '', /noindex/, `${path}/dependencies`);
        assert.equal([...html.matchAll(/<link[^>]+hreflang="/g)].length, 0, `${path}/dependencies hreflang`);
      }
    }
    const legacy = await (await get('/apps/versioned-app@11')).text();
    assert.equal(dependencyLinks(legacy, '/apps/versioned-app@11/dependencies').length, 1, 'older API keeps its tab');
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
    assert.deepEqual(robots(overview), [INDEX_ROBOTS]);
    for (const path of ['/apps/ordinary-app', '/apps/ordinary-app/versions']) {
      const response = await fetch(`${origin}${path}`, { headers: { accept: 'text/html' } });
      const values = robots(await response.text());
      assert.deepEqual(values, [INDEX_ROBOTS], path);
      assert.equal(response.headers.get('x-robots-tag'), INDEX_ROBOTS, path);
      for (const directive of ['max-image-preview:large', 'max-snippet:-1', 'max-video-preview:-1'])
        assert.ok(
          values[0]
            .split(',')
            .map(value => value.trim())
            .includes(directive),
          `${path}: ${directive}`
        );
    }
    const hreflangs = [...overview.matchAll(/<link[^>]+hreflang="([^"]+)"/g)].map(m => m[1]).sort();
    assert.deepEqual(hreflangs, ['en', 'x-default', 'zh', 'zh-Hans']);
    assert.deepEqual(robots(await html('/ja/apps/ordinary-app')), ['noindex, follow']);
    // Install details are consolidated: noindex, canonical to overview, with no alternates.
    const tag = (page, pattern) => pattern.exec(page)?.[1];
    for (const [path, overviewUrl] of [
      ['/apps/ordinary-app/details', 'https://example.test/apps/ordinary-app'],
      ['/zh/apps/ordinary-app/details', 'https://example.test/zh/apps/ordinary-app']
    ]) {
      const details = await html(path);
      assert.deepEqual(robots(details), ['noindex, follow'], path);
      assert.equal(tag(details, /<link[^>]*rel="canonical"[^>]*href="([^"]+)"/), overviewUrl, `${path} canonical`);
      assert.equal(tag(details, /<meta[^>]*property="og:url"[^>]*content="([^"]+)"/), overviewUrl, `${path} og:url`);
      assert.equal([...details.matchAll(/<link[^>]+hreflang="/g)].length, 0, `${path} hreflang`);
    }
    // Consolidation is independent of the overview's locale readiness.
    const unpublished = await html('/ja/apps/ordinary-app/details');
    assert.deepEqual(robots(unpublished), ['noindex, follow']);
    assert.equal(
      tag(unpublished, /<link[^>]*rel="canonical"[^>]*href="([^"]+)"/),
      'https://example.test/ja/apps/ordinary-app'
    );
    // An older API without index decisions uses the site's default open languages.
    const legacy = await html('/apps/versioned-app@11');
    // L1: a legacy response without entity decisions falls back to the six default open locales.
    const alternates = page => [...page.matchAll(/<link[^>]+hreflang="([^"]+)"/g)].map(match => match[1]).sort();
    const defaultHreflangs = ['en', 'es', 'ja', 'pt', 'pt-BR', 'ru', 'x-default', 'zh', 'zh-Hans'].sort();
    assert.deepEqual(alternates(legacy), defaultHreflangs);
    const entityHreflangs = [
      'en',
      'zh-Hans',
      'zh',
      'ja',
      'es',
      'pt-BR',
      'pt',
      'ru',
      'de',
      'fr',
      'it',
      'zh-Hant',
      'zh-TW',
      'zh-HK',
      'x-default'
    ].sort();
    // L1: entity PageDecision can open all ten languages, including traditional-Chinese aliases.
    for (const prefix of Object.values(localePrefixes))
      assert.deepEqual(alternates(await html(`${prefix}/apps/plus-app++`)), entityHreflangs, prefix || '/');
    assert.deepEqual(alternates(await html('/apps')), defaultHreflangs);
    assert.deepEqual(robots(await html('/de/apps')), ['noindex, follow']);
    const settingsPaths = ['/apps', '/de/apps', '/zh/apps', '/zh-hant/apps'];
    try {
      // L1/S6: non-entity alternates follow openLocales after an authenticated settings invalidation.
      siteSettings.pages.openLocales = ['en-US', 'de-DE', 'zh-Hant'];
      const purged = await invalidatePaths(settingsPaths, true);
      assert.equal(purged.settingsCleared, true);
      assert.deepEqual(alternates(await html('/apps')), ['de', 'en', 'x-default', 'zh-HK', 'zh-Hant', 'zh-TW'].sort());
      assert.ok(!robots(await html('/de/apps')).some(value => value.includes('noindex')));
      assert.deepEqual(robots(await html('/zh/apps')), ['noindex, follow']);
      assert.deepEqual(alternates(await html('/apps/plus-app++')), entityHreflangs);
    } finally {
      delete siteSettings.pages.openLocales;
      await invalidatePaths(settingsPaths, true);
    }
  });
  await t.test('robots response headers match final page directives, including cached responses', async () => {
    for (const path of [
      '/apps/thin-app',
      '/ja/apps/ordinary-app',
      '/ja/apps/ordinary-app/details',
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
  await t.test('indexable article, list and author pages keep the robots meta tag the header repeats', async () => {
    // An empty page-level robots value once removed the meta tag while X-Robots-Tag kept the default,
    // which the SEO audit reports as a P0 disagreement and which stops all SEO automation.
    for (const path of ['/articles', '/articles/choose-a-screenshot-app', '/authors/lin-wei']) {
      const response = await fetch(`${origin}${path}`, { headers: { accept: 'text/html' } });
      const html = await response.text();
      const directives = [...html.matchAll(/<meta name="robots" content="([^"]*)"/g)].map(m => m[1]);
      assert.equal(directives.length, 1, path);
      assert.ok(!directives[0].includes('noindex'), path);
      assert.equal(response.headers.get('x-robots-tag'), directives[0], path);
    }
  });
  await t.test('sitemaps fail loudly and list grouped URLs with per-language lastmod', async () => {
    await invalidatePaths(['/__sitemap__/apps-bucket-0.xml']);
    sitemapState = 'error';
    const failed = await fetch(`${origin}/__sitemap__/apps-bucket-0.xml?error-check=1`);
    assert.ok(failed.status >= 500, `a failed API must not render an empty sitemap (${failed.status})`);
    assert.equal(failed.status, 503);
    assert.equal(failed.headers.get('retry-after'), '120');
    assert.match(failed.headers.get('cache-control') ?? '', /no-store/);
    sitemapState = 'ok';
    const index = await (await fetch(`${origin}/sitemap_index.xml`)).text();
    assert.ok(!index.includes('/__sitemap__/fonts-bucket-'), 'font buckets are excluded');
    for (let bucket = 0; bucket < 8; bucket++) {
      const fontMap = await fetch(`${origin}/__sitemap__/fonts-bucket-${bucket}.xml`);
      assert.equal(fontMap.status, 410);
      assert.equal(fontMap.headers.get('cache-control'), 'no-store');
      assert.ok(!(await fontMap.text()).includes('<urlset'));
    }
    const fontPage = await fetch(`${origin}/apps/font-sample`);
    assert.equal(fontPage.status, 200);
    const fontHtml = await fontPage.text();
    assert.match(fontHtml, /name="robots" content="noindex, follow/);
    assert.match(fontHtml, /rel="canonical" href="https:\/\/example.test\/apps\/font-sample"/);
    assert.ok(!index.includes('<lastmod>'), 'index cannot claim dates across independent snapshots');
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
    assert.match(urlEntry('https://example.test/apps/ordinary-app') ?? '', /<lastmod>2025-03-18T10:00:00Z<\/lastmod>/);
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
    assert.ok(!versions.includes('/apps/font-sample'), 'font versions are excluded');
    assert.ok(versions.includes('https://example.test/apps/ordinary-app/versions'));
    const undatedVersion =
      /<url>\s*<loc>https:\/\/example.test\/apps\/ordinary-app\/versions<\/loc>([\s\S]*?)<\/url>/.exec(versions)?.[1];
    assert.ok(undatedVersion !== undefined);
    assert.ok(!undatedVersion.includes('<lastmod>'), 'versions must not inherit overview lastmod');
    const dates = [...(apps + versions).matchAll(/<lastmod>([^<]+)<\/lastmod>/g)].map(match => match[1]);
    assert.ok(dates.length > 0);
    for (const date of dates) assert.match(date, /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$/);

    // OpenNavo's own entry answers a 301 to /download, so no app or versions sitemap lists any of its pages.
    assert.ok(!/\/apps\/opennavo[<"/]/.test(apps + versions), 'no sitemap lists /apps/opennavo');
    // Tokens keep their + in every loc, as page canonicals and hreflang spell them, so no loc redirects.
    assert.ok(apps.includes('<loc>https://example.test/apps/plus-app++</loc>'), apps);
    assert.ok(apps.includes('<loc>https://example.test/zh/apps/plus-app++</loc>'), apps);
    assert.ok(apps.includes('hreflang="zh-Hans" href="https://example.test/zh/apps/plus-app++"'), apps);
    assert.ok(versions.includes('<loc>https://example.test/apps/plus-app++/versions</loc>'), versions);
    assert.ok(!/<loc>[^<]*%2B/i.test(apps + versions), 'no escaped + in any loc');
    for (const loc of ['/apps/plus-app++', '/zh/apps/plus-app++', '/apps/plus-app++/versions']) {
      const response = await fetch(`${origin}${loc}`, {
        redirect: 'manual',
        headers: { accept: 'text/html' }
      });
      await response.text();
      assert.equal(response.status, 200, `${loc} answers without a redirect\n${logs}`);
    }
    const versionsBucket = `/__sitemap__/${packageSitemapName('versions', 'ordinary-app')}.xml`;
    try {
      siteSettings.pages.versionsSitemap = false;
      await invalidatePaths(['/'], true);
      const closedVersions = await fetch(`${origin}${versionsBucket}`);
      assert.equal(closedVersions.status, 410);
      for (const header of ['cache-control', 'cdn-cache-control', 'cloudflare-cdn-cache-control'])
        assert.equal(closedVersions.headers.get(header), 'no-store', header);
      assert.ok(!(await closedVersions.text()).includes('<urlset'));
      const closedIndex = await (await fetch(`${origin}/sitemap_index.xml`)).text();
      assert.ok(!closedIndex.includes('/__sitemap__/versions-bucket-'));

      siteSettings.pages.versionsSitemap = true;
      await invalidatePaths(['/'], true);
      const reopenedVersions = await fetch(`${origin}${versionsBucket}`);
      assert.equal(reopenedVersions.status, 200);
      assert.ok((await reopenedVersions.text()).includes('/apps/ordinary-app/versions'));
      for (const group of ['apps', 'versions']) {
        const unknown = await fetch(`${origin}/__sitemap__/${group}-bucket-99.xml`);
        assert.equal(unknown.status, 404, group);
        await unknown.text();
      }
    } finally {
      siteSettings.pages.versionsSitemap = true;
      await invalidatePaths(['/'], true);
    }
    // The index keeps its list of populated buckets: a warm request reads no catalog.
    const reads = sitemapPackageRequests;
    const warm = await fetch(`${origin}/sitemap_index.xml`);
    assert.equal(warm.status, 200);
    assert.ok((await warm.text()).includes('/__sitemap__/apps-bucket-'));
    assert.equal(sitemapPackageRequests, reads, 'a warm index answers from its cached result');
  });
  await t.test('font detail pages stay out of indexing when the font sitemap switch is off', async () => {
    await setFontsSitemap(false);
    const response = await fetch(`${origin}/apps/font-sample`, { headers: { accept: 'text/html' } });
    const html = await response.text();
    assert.equal(response.status, 200, logs);
    assert.match(html, /name="robots" content="noindex, follow/);
    const bucket = await fetch(`${origin}${fontBucketPath}`);
    assert.equal(bucket.status, 410, logs);
    assert.equal(bucket.headers.get('cache-control'), 'no-store');
  });
  await t.test('font detail pages and font buckets are indexable when the switch is on', async () => {
    await setFontsSitemap(true);
    const response = await fetch(`${origin}/apps/font-sample`, { headers: { accept: 'text/html' } });
    const html = await response.text();
    assert.equal(response.status, 200, logs);
    assert.match(html, /name="robots" content="index, follow/);
    const index = await (await fetch(`${origin}/sitemap_index.xml`)).text();
    assert.match(index, /\/__sitemap__\/fonts-bucket-\d+\.xml/);
    const bucket = await fetch(`${origin}${fontBucketPath}`);
    assert.equal(bucket.status, 200, logs);
    assert.match(await bucket.text(), /\/apps\/font-sample/);
    await setFontsSitemap(false);
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
  await t.test('OpenNavo’s own catalog entry sends its overview to its language’s /download', async () => {
    for (const [path, location] of [
      ['/apps/opennavo', '/download'],
      ['/zh/apps/opennavo', '/zh/download'],
      ['/zh-hant/apps/opennavo', '/zh-hant/download'],
      // Any other spelling reaches /download in the one hop every canonical redirect takes.
      ['/en/apps/opennavo', '/download'],
      ['/Apps/OpenNavo/', '/download'],
      ['/ja/apps/opennavo/', '/ja/download'],
      ['/apps/opennavo?utm_source=feed', '/download?utm_source=feed'],
      ['/apps/opennavo?returnTo=%2Frecent', '/download']
    ])
      for (const method of ['GET', 'HEAD']) {
        const response = await fetch(`${origin}${path}`, { method, redirect: 'manual' });
        await response.text();
        assert.equal(response.status, 301, `${method} ${path}\n${logs}`);
        assert.equal(response.headers.get('location'), location, `${method} ${path}`);
      }
    // The tabs stay; their overview tab and breadcrumb point at /download, never at the redirect.
    const response = await fetch(`${origin}/apps/opennavo/details`, { headers: { accept: 'text/html' } });
    const html = await response.text();
    assert.equal(response.status, 200, logs);
    const breadcrumb = /<nav aria-label="Breadcrumb">[\s\S]*?<\/nav>/.exec(html)?.[0] ?? '';
    assert.match(breadcrumb, /<a\b[^>]*href="\/download"[^>]*>\s*OpenNavo\s*<\/a>/, breadcrumb || 'no breadcrumb');
    const tabs = /<nav aria-label="Package tabs"[\s\S]*?<\/nav>/.exec(html)?.[0] ?? '';
    assert.match(tabs, /<a\b[^>]*href="\/download"[^>]*>\s*Overview\b/, tabs || 'no tabs');
    assert.ok(!/href="\/apps\/opennavo"/.test(html), 'no link to the redirected overview');
    // A tab the server consolidates into the overview canonicalizes to /download, where the overview goes.
    const canonical = page => /<link[^>]*rel="canonical"[^>]*href="([^"]+)"/.exec(page)?.[1];
    assert.equal(canonical(html), 'https://example.test/download');
    const zh = await fetch(`${origin}/zh/apps/opennavo/details`, { headers: { accept: 'text/html' } });
    assert.equal(zh.status, 200);
    assert.equal(canonical(await zh.text()), 'https://example.test/zh/download');
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
      body: JSON.stringify({
        paths: ['/apps/ordinary-app', '/apps/old-token', '/apps/missing-app@0']
      })
    });
    assert.equal(probe.status, 200);
    assert.deepEqual((await probe.json()).statuses, {
      '/apps/ordinary-app': 200,
      '/apps/old-token': 301,
      '/apps/missing-app@0': 404
    });
    const unauthorizedProbe = await fetch(`${internal}/cache/probe`, {
      method: 'POST',
      body: '{"paths":["/"]}'
    });
    assert.equal(unauthorizedProbe.status, 401);
    // Release identity combines the page's Nuxt build with its effective OG signing-key fingerprint.
    assert.equal((await fetch(`${internal}/cache/build`)).status, 401);
    const build = await fetch(`${internal}/cache/build`, {
      headers: { authorization: `Bearer ${internalSecret}` }
    });
    assert.equal(build.status, 200);
    const { build: buildId } = await build.json();
    assert.match(buildId, /^[\w-]{8,}\.[a-f0-9]{12}$/);
    assert.ok((await page()).includes(buildId.split('.')[0]), 'pages reference the reported Nuxt build');
    const metrics = await (await fetch(`${internal}/metrics`)).text();
    assert.match(
      metrics,
      /opennavo_web_requests_total\{agent="browser",route="app",status="200",verification="unknown",verification_basis="none"\} \d+/
    );
    assert.match(metrics, /^opennavo_web_in_flight \d+$/m);
    assert.match(metrics, /^opennavo_web_image_in_flight \d+$/m);
    assert.match(metrics, /^# TYPE opennavo_web_load_shed_total counter$/m);
    assert.match(metrics, /^opennavo_web_http_in_flight \d+$/m);
    for (const name of ['heap_used', 'heap_limit', 'rss', 'external'])
      assert.match(metrics, new RegExp(`^opennavo_web_${name}_bytes \\d+$`, 'm'));
  });
  await t.test('only public cached pages may be cached at the edge', async () => {
    // Purged pages keep two days and lists ten minutes; both answer from the edge while the origin sheds load.
    const LONG_EDGE = 'max-age=172800, stale-while-revalidate=604800, stale-if-error=604800';
    const SHORT_EDGE = 'max-age=600, stale-while-revalidate=86400, stale-if-error=86400';
    const edge = async path => {
      const response = await fetch(`${origin}${path}`, { headers: { accept: 'text/html' } });
      await response.text();
      return [response.status, response.headers.get('cloudflare-cdn-cache-control')];
    };
    // App pages with their tabs, and article pages, are purged on change and after each release, so
    // they stay for hours.
    assert.deepEqual(await edge('/apps/ordinary-app'), [200, LONG_EDGE]);
    assert.deepEqual(await edge('/apps/ordinary-app/details'), [200, LONG_EDGE]);
    // X5: collections now use the same short edge cache as other public lists; search stays private.
    assert.equal((await edge('/search?q=editor'))[1], 'no-store');
    assert.deepEqual(await edge('/collections'), [200, SHORT_EDGE]);
    assert.deepEqual(await edge('/apps/gone-app'), [410, 'no-store']);
    assert.deepEqual(await edge('/apps/missing-app@0'), [404, 'no-store']);
    // The category overview and the feedback form are cached at the origin like their neighbours.
    for (const path of ['/categories', '/zh/categories', '/feedback', '/ja/feedback?type=broken_link']) {
      const response = await fetch(`${origin}${path}`, { headers: { accept: 'text/html' } });
      await response.text();
      assert.equal(response.status, 200, `${path}\n${logs}`);
      assert.match(response.headers.get('cache-control') ?? '', /s-maxage=900/, path);
    }
    assert.deepEqual(await edge('/categories'), [200, SHORT_EDGE]);
    // Recently Updated is a list like /apps, paginated pages included. (Later tests need /recent itself
    // uncached by the route cache, so these are other languages' URLs.)
    for (const path of ['/pt/recent', '/ru/recent?page=2', '/es/recent?page=2&size=12'])
      assert.deepEqual(await edge(path), [200, 'max-age=60, stale-while-revalidate=300, stale-if-error=300'], path);
    // Feeds: an app's release feed is purged with the app, so it keeps long; the others expire like lists.
    for (const path of ['/apps/ordinary-app/feed.xml', '/zh/apps/ordinary-app/feed.xml']) {
      const response = await fetch(`${origin}${path}`);
      await response.text();
      assert.equal(response.status, 200, `${path}\n${logs}`);
      assert.match(response.headers.get('content-type') ?? '', /application\/rss\+xml/, path);
      assert.match(response.headers.get('cache-control') ?? '', /s-maxage=21600/, path);
      assert.equal(response.headers.get('cloudflare-cdn-cache-control'), LONG_EDGE, path);
    }
    for (const path of ['/articles/feed.xml', '/collections/editors/feed.xml'])
      assert.deepEqual(await edge(path), [200, SHORT_EDGE], path);
    // GA4 page_type and the crawler counters still call it "other".
    const metrics = await (await fetch(`${internal}/metrics`)).text();
    assert.match(
      metrics,
      /opennavo_web_requests_total\{agent="browser",route="other",status="200",verification="unknown",verification_basis="none"\} \d+/
    );
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
    // C2: the download title contains its own brand, while category titles use categories.seo.itemTitle.
    const downloadTitle = await title('/download');
    assert.equal(downloadTitle, 'Download OpenNavo for Mac – Homebrew App Store with Antivirus');
    assert.equal(downloadTitle.match(/OpenNavo/g)?.length, 1);
    assert.equal(await title('/categories/utilities'), 'Utilities – Mac Apps · OpenNavo');
    const description = async path =>
      /<meta name="description" content="([^"]*)"/.exec(
        await (await fetch(`${origin}${path}`, { headers: { accept: 'text/html' } })).text()
      )?.[1];
    // C2 (seoTitles.ts / en-US.json): each page describes its own catalog data and purpose.
    assert.equal(
      await description('/categories/utilities'),
      'Small tools for everyday Mac tasks. 50 Mac apps in Utilities, including Example App @11.'
    );
    assert.equal(
      await description('/apps/ordinary-app/dependencies'),
      'Example App @11 installs on Mac without any other Homebrew packages.'
    );
    assert.equal(
      await description('/apps/ordinary-app/details'),
      'Install details for Example App @11: where it goes, its download URL and checksum, and the Homebrew definition it comes from.'
    );
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
    // The T1 crawl already visits /apps; invalidate before asserting the origin request contract.
    await invalidatePaths(['/apps']);
    assert.ok(!(await title('/apps')).includes('Page'), 'the first page has no page number');
    // All apps mix in link apps: no kind filter, and the client says it understands them.
    assert.deepEqual(
      packageQueries.slice(before).map(({ kind }) => kind),
      [undefined]
    );
    assert.equal(listCapabilities.at(-1), 'link');
  });
  await t.test('app titles name the platform once and descriptions prioritize the complete summary', async () => {
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
    // Keep the complete summary instead of shortening it for a generic closing sentence.
    assert.equal(
      description(await page('/apps/wechat')),
      'Tencent’s Mac messaging client for text, files, voice and video calls with your WeChat contacts.'
    );
    // Chinese names join the Chinese text around them; this one fits whole.
    assert.equal(
      description(await page('/zh/apps/wechat')),
      '腾讯的 Mac 即时通信客户端，可与 WeChat 联系人收发文字消息和文件，以及进行语音和视频通话。查看微信 Mac 版在 Mac 上的安装方法和卸载方法。'
    );
  });
  await t.test('overview overrides replace the title and description where set, on the overview only', async () => {
    const page = async path => {
      const response = await fetch(`${origin}${path}`, { headers: { accept: 'text/html' } });
      assert.equal(response.status, 200, `${path}\n${logs}`);
      const html = await response.text();
      return {
        title: /<title>([^<]*)<\/title>/.exec(html)?.[1],
        description: /<meta name="description" content="([^"]*)"/.exec(html)?.[1]
      };
    };
    // Both fields come from the language's override; the title still ends with the brand, once.
    assert.deepEqual(await page('/apps/seo-override'), {
      title: 'SEO App for Mac, picked by editors · OpenNavo',
      description: 'An editor-written description of SEO App.'
    });
    assert.deepEqual(await page('/zh/apps/seo-override'), {
      title: 'SEO App 编辑精选 · OpenNavo',
      description: '编辑撰写的 SEO App 介绍。'
    });
    // Without an override in a language, the overview is exactly what the templates make for any app.
    const basic = await page('/apps/seo-basic');
    assert.equal(basic.title, 'SEO App for Mac – Download &amp; Install · OpenNavo');
    assert.deepEqual(await page('/ja/apps/seo-override'), await page('/ja/apps/seo-basic'));
    // A title alone leaves the template description in place.
    assert.deepEqual(await page('/apps/seo-override-title'), {
      title: 'Custom SEO App title · OpenNavo',
      description: basic.description
    });
    // A Homebrew-disabled app can no longer be installed, so no override may promise a download.
    const disabled = await page('/apps/seo-disabled');
    assert.equal(disabled.title, 'SEO App for Mac · OpenNavo');
    assert.match(disabled.description, /Homebrew has disabled SEO App/);
    assert.doesNotMatch(`${disabled.title} ${disabled.description}`, /Download/);
    // Other tabs keep their own templates.
    const versions = await page('/apps/seo-override/versions');
    assert.equal(versions.title, 'SEO App Release Notes (Latest 11.0) · OpenNavo');
    assert.doesNotMatch(versions.description, /editor/);
  });
  await t.test('C2 descriptions name visible modules in a fixed order', async () => {
    const page = async path => {
      const response = await fetch(`${origin}${path}`, { headers: { accept: 'text/html' } });
      assert.equal(response.status, 200, logs);
      const html = await response.text();
      return { html, description: /<meta name="description" content="([^"]*)"/.exec(html)?.[1] ?? '' };
    };
    const basic = await page('/apps/seo-basic');
    assert.ok(basic.description.includes('installation and uninstallation'));
    assert.doesNotMatch(basic.description, /pricing|system requirements|alternatives|release history/);
    const free = await page('/apps/seo-free');
    assert.ok(free.description.includes('pricing'));
    assert.ok(free.html.includes('data-testid="package-price"'));
    const full = await page('/apps/seo-uninstall');
    assert.match(
      full.description,
      /Explore installation, pricing, system requirements, uninstallation, and alternatives/
    );
    for (const id of ['package-uninstall', 'package-alternatives', 'package-requirements', 'package-price'])
      assert.ok(full.html.includes(`data-testid="${id}"`), id);
    // The eight untranslated locales fall back to English keys while retaining locale list punctuation.
    for (const prefix of Object.values(localePrefixes)) {
      const { description } = await page(`${prefix}/apps/seo-basic`);
      assert.ok(description.includes('SEO App'), prefix);
      assert.doesNotMatch(description, /package\.seo|[{}]|undefined/);
    }
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
    // The T1 crawl warms /recent, so request assertions need a cold listing.
    await invalidatePaths(['/recent']);
    const first = await page('/recent');
    assert.equal(first.status, 200, logs);
    assert.deepEqual(
      packageQueries.slice(before).map(({ sort, kind, current, size }) => ({ sort, kind, current, size })),
      [{ sort: 'updated', kind: 'cask', current: '1', size: '24' }]
    );
    assert.match(first.html, /<title>Recently Updated Mac Apps · OpenNavo<\/title>/);
    // S1/C3: the heading can retain Vue's empty subtitle comment.
    assert.match(first.html, /<h1[^>]*>Recently Updated(?:<!--(?:v-if)?-->)?<\/h1>/);
    assert.equal(canonical(first.html), 'https://example.test/recent');
    const appLink = html => {
      // S3: return position lives in history state; rendered app links have no returnTo query.
      const href = /href="([^"]*\/apps\/ordinary-app(?:\?[^"]*)?)"/.exec(html)?.[1];
      assert.ok(href, 'app cards render a crawlable app link');
      return new URL(href.replaceAll('&amp;', '&'), origin);
    };
    assert.equal(appLink(first.html).pathname, '/apps/ordinary-app');
    assert.equal(appLink(first.html).search, '');
    assert.ok(first.html.includes('href="/recent?page=2"'), 'pages are real links');
    assert.ok(first.html.includes('"ItemList"'), 'the listed apps are structured data');
    assert.ok(/aria-current="page"[^>]*href="\/recent"|href="\/recent"[^>]*aria-current="page"/.test(first.html));
    const second = await page('/zh/recent?page=2');
    assert.equal(second.status, 200);
    assert.match(second.html, /<meta[^>]*name="robots"[^>]*content="noindex, follow"/);
    assert.match(second.html, /<title>最近更新的 Mac App · 第 2 页 · OpenNavo<\/title>/);
    assert.equal(canonical(second.html), 'https://example.test/zh/recent?page=2', 'page 2 is its own canonical');
    assert.equal(appLink(second.html).pathname, '/zh/apps/ordinary-app');
    assert.equal(appLink(second.html).search, '');
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
      // S4: sort is a canonical parameter of filtered grids, and the variant stays noindex.
      assert.equal(target, `https://example.test${prefix}/apps?page=4&sort=name&disabled=1`);
      assert.equal(ogUrl(filtered.html), target);
      assert.match(filtered.html, /<meta name="robots" content="[^"]*noindex/);
      assert.equal((await get(target.replace('https://example.test', ''))).response.status, 200);
      assert.equal((await get(`${prefix}/apps?page=6&disabled=1`)).response.status, 404);
    }
  });
  await t.test('only route-contract parameters survive in canonical, hreflang and og:url', async () => {
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
    // S2/S4: size=12 page 2 redirects to the fixed grid's first page; only contracted filters remain.
    for (const [path, expected] of [
      ['/apps?page=2&size=12&disabled=1', 'https://example.test/apps?disabled=1'],
      ['/recent?page=2&size=12&disabled=1', 'https://example.test/recent'],
      ['/zh/rankings?page=2&size=12&disabled=1', 'https://example.test/zh/rankings?page=2'],
      ['/articles?page=2&size=12', 'https://example.test/articles?page=2']
    ]) {
      const { status, html } = await get(path);
      assert.equal(status, 200, `${path}\n${logs}`);
      assert.equal(canonical(html), expected, path);
      assert.equal(ogUrl(html), expected, path);
      for (const href of alternates(html)) assert.equal(new URL(href).search, new URL(expected).search, href);
    }
    // S4: accepted non-default sorts/filters keep a self-canonical and noindex in HTML and headers.
    for (const path of [
      '/apps?sort=name',
      '/categories/utilities?sort=updated',
      '/rankings?period=90d',
      '/rankings?category=utilities',
      '/articles?type=tutorial',
      '/apps/plus-app++/versions?notes=1',
      '/background-items?q=agent'
    ]) {
      const response = await fetch(`${origin}${path}`, { headers: { accept: 'text/html' } });
      const html = await response.text();
      assert.equal(response.status, 200, path);
      assert.match(html, /<meta name="robots" content="noindex, follow"/, path);
      assert.equal(response.headers.get('x-robots-tag'), 'noindex, follow', path);
      assert.equal(
        canonical(html),
        `https://example.test${path.startsWith('/background-items') ? '/background-items' : path}`,
        path
      );
    }
    // S4: unknown enum values are errors, not silently accepted fallback variants.
    for (const path of [
      '/apps?sort=unknown',
      '/categories/utilities?sort=unknown',
      '/rankings?period=unknown',
      '/rankings?category=unknown',
      '/articles?type=unknown'
    ]) {
      const response = await fetch(`${origin}${path}`, { headers: { accept: 'text/html' } });
      await response.text();
      assert.equal(response.status, 404, path);
      assert.equal(response.headers.get('cloudflare-cdn-cache-control'), 'no-store', path);
    }
  });
  await t.test('grid sizes redirect once to the fixed-size page containing the same first item', async () => {
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
    // S2: all grids have 24 items; legacy page 4 at size 12 starts in the new page 2.
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
      await invalidatePaths([path]);
      const redirect = await fetch(`${origin}${path}?page=4&size=12`, { redirect: 'manual' });
      await redirect.text();
      assert.equal(redirect.status, 301, path);
      assert.equal(redirect.headers.get('location'), `${path}?page=2`, path);
      const sized = await get(`${path}?page=4&size=12`);
      assert.equal(sized.status, 200, path);
      assert.ok(
        packageQueries.slice(before).some(query => query.current === '2' && query.size === '24'),
        path
      );
      const target = canonical(sized.html);
      assert.equal(target, `https://example.test${path}?page=2`);
      const checkIndexing = path.endsWith('/recent') ? unindexed : indexed;
      checkIndexing(sized, path);
      assert.equal((await get(target.replace('https://example.test', ''))).status, 200, target);
      checkIndexing(await get(`${path}?page=2`), `${path}?page=2`);
      assert.equal((await get(`${path}?page=4`)).status, 404, path);
      assert.equal((await get(`${path}?page=7&size=12`)).status, 404, path);
      const ogUrl = /<meta[^>]*property="og:url"[^>]*content="([^"]+)"/.exec(sized.html)?.[1]?.replaceAll('&amp;', '&');
      assert.equal(ogUrl, target, path);
      const alternates = [...sized.html.matchAll(/<link[^>]*rel="alternate"[^>]*href="([^"]+)"/g)];
      assert.ok(alternates.length > 0, path);
      for (const [, href] of alternates) {
        const url = new URL(href.replaceAll('&amp;', '&'));
        assert.equal(url.searchParams.get('size'), null, href);
        assert.equal(url.searchParams.get('page'), '2', href);
      }
    }
    const combined = await get('/apps?page=8&disabled=1&size=12');
    assert.equal(combined.status, 200);
    assert.equal(canonical(combined.html), 'https://example.test/apps?page=4&disabled=1');
    unindexed(combined, 'S4: disabled filter remains noindex after size normalization');
    for (const size of ['24', '101', 'abc']) {
      const page = await get(`/apps?page=2&size=${size}`);
      assert.equal(canonical(page.html), 'https://example.test/apps?page=2');
      // The default size, or a value that falls back to it, is the list itself.
      indexed(page, `size=${size}`);
    }
    const first = await get('/apps?page=1&size=012');
    assert.equal(canonical(first.html), 'https://example.test/apps');
    indexed(first, 'S2: normalized first page');
    const repeated = await get('/apps?page=2&size=12&size=12');
    assert.equal(canonical(repeated.html), 'https://example.test/apps');
    indexed(repeated, 'S2: legacy repeated size is removed in the redirect');
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
      ['/Zh/Apps/plus-app%2B%2B/?source=a%2Bb', '/zh/apps/plus-app++?source=a%2Bb'],
      // S3: returnTo is removed in the same hop as path and size normalization.
      ['/APPS/?page=4&size=12&returnTo=%2Frecent', '/apps?page=2'],
      ['/apps/ordinary-app?returnTo=%2Frecent', '/apps/ordinary-app']
    ]) {
      const response = await fetch(`${origin}${from}`, { redirect: 'manual' });
      assert.equal(response.status, 301, from);
      assert.equal(response.headers.get('location'), to, from);
      const target = await fetch(`${origin}${to}`, {
        redirect: 'manual',
        headers: { accept: 'text/html' }
      });
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
    assert.equal(await heading('/'), 'The Homebrew app store for Mac, with virus scanning and an app firewall.');
    assert.equal(await heading('/ru'), 'Магазин Homebrew для Mac с проверкой на вирусы и брандмауэром приложений.');
    // Chinese and Japanese join their two lines without a space.
    assert.equal(await heading('/zh'), 'Mac 上的 Homebrew 应用商店，病毒扫描与应用防火墙。');
    assert.equal(await heading('/ja'), 'MacのHomebrewアプリストア、ウイルススキャンとApp ファイアウォールも搭載。');
  });
  await t.test(
    'the home page defines OpenNavo and compares it in a server-rendered table in every language',
    async () => {
      const decode = value =>
        value
          .replace(/&#39;|&apos;/g, "'")
          .replace(/&quot;/g, '"')
          .replace(/&lt;/g, '<')
          .replace(/&gt;/g, '>')
          .replace(/&amp;/g, '&');
      for (const [locale, prefix] of Object.entries(localePrefixes)) {
        const { landing } = JSON.parse(
          await readFile(new URL(`../i18n/locales/${locale}.json`, import.meta.url), 'utf8')
        );
        const html = decode(
          await (await fetch(`${origin}${prefix || '/'}`, { headers: { accept: 'text/html' } })).text()
        );
        // G3/G4: the one-sentence definition the README, structured data and /llms.txt share.
        assert.ok(html.includes(landing.about.definition), `${locale} definition`);
        const table = /<table\b[^>]*landing-compare[\s\S]*?<\/table>/.exec(html)?.[0] ?? '';
        assert.ok(table.includes('<caption') && table.includes(landing.about.compare.caption), `${locale} caption`);
        assert.equal(table.match(/<th\b[^>]*scope="col"/g)?.length, 3, `${locale} columns`);
        assert.equal(table.match(/<th\b[^>]*scope="row"/g)?.length, 3, `${locale} rows`);
        for (const tool of ['opennavo', 'homebrew', 'appStore'])
          for (const row of ['use', 'source', 'scope'])
            assert.ok(table.includes(landing.about.compare.cells[tool][row]), `${locale} ${tool}.${row}`);
        // The staged scan says it is a demo where everyone can read it, not only in an aria-label.
        assert.ok(/<figcaption\b/.test(html) && html.includes(landing.security.demo.caption), `${locale} demo caption`);
      }
    }
  );
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
  await t.test('the security page states what it does and what it cannot do, and every entry links to it', async () => {
    const html = async path => (await fetch(`${origin}${path}`, { headers: { accept: 'text/html' } })).text();
    const heading = page => (/<h1\b[^>]*>([\s\S]*?)<\/h1>/.exec(page)?.[1] ?? '').replace(/<[^>]*>/g, '');
    const zh = await html('/zh/security');
    assert.equal(/<title>([^<]*)<\/title>/.exec(zh)?.[1], '免费 Mac 杀毒软件：ClamAV 病毒扫描与启动项检查 · OpenNavo');
    assert.equal(heading(zh), '免费开源的 Mac 杀毒，装软件前先查毒。');
    // The limits are in the server-rendered answers even while their questions are collapsed.
    assert.ok(zh.includes('实时拦截需要 Apple 的 Endpoint Security 授权'), 'real-time blocking is not promised');
    assert.ok(zh.includes('ClamAV 对 Mac 专用恶意软件的覆盖有限'), 'detection coverage is stated');
    assert.equal(heading(await html('/security')), 'Free, open-source antivirus for Mac, from the moment you install.');
    // The home announcement, the top navigation and the footer all reach the page.
    const home = await html('/zh');
    assert.ok((home.match(/href="\/zh\/security"/g) ?? []).length >= 3, 'home links to /zh/security');
    const statics = await (await fetch(`${origin}/__sitemap__/zh-Hans-static.xml`)).text();
    assert.ok(statics.includes('<loc>https://example.test/zh/security</loc>'), statics);
  });
  await t.test('security-category apps point to the security page', async () => {
    const html = async path => (await fetch(`${origin}${path}`, { headers: { accept: 'text/html' } })).text();
    const guard = await html('/zh/apps/guard-app');
    const promo = /<section[^>]*aria-labelledby="package-security-promo"[\s\S]*?<\/section>/.exec(guard)?.[0] ?? '';
    assert.ok(promo.includes('href="/zh/security"'), promo || 'no security promo');
    assert.ok(!(await html('/apps/ordinary-app')).includes('package-security-promo'), 'other apps have no promo');
  });
  await t.test('the home page, /download and app pages link the security topic pages directly', async () => {
    const html = async path => (await fetch(`${origin}${path}`, { headers: { accept: 'text/html' } })).text();
    const escape = text => text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
    // A link to the topic page under the topic's own translated title.
    const links = (page, path, label) => new RegExp(`<a\\b[^>]*href="${path}"[^>]*>\\s*${escape(label)}`).test(page);
    for (const [prefix, code] of [
      ['', 'en-US'],
      ['/zh', 'zh-CN']
    ]) {
      const { securityTopics } = JSON.parse(
        await readFile(new URL(`../i18n/locales/${code}.json`, import.meta.url), 'utf8')
      );
      const title = topic => securityTopics.links[topic].title;
      // The home page's scan and system check-up copy, beside its link to /security.
      const home = await html(prefix || '/');
      for (const topic of ['clamav', 'xprotect'])
        assert.ok(links(home, `${prefix}/security/${topic}`, title(topic)), `${prefix || '/'} → ${topic}`);
      assert.ok(home.includes(`href="${prefix}/security"`), `${prefix || '/'} still links /security`);
      const download = await html(`${prefix}/download`);
      assert.ok(links(download, `${prefix}/security/clamav`, title('clamav')), `${prefix}/download → clamav`);
      // Every app page's security facts link the engine its scan runs on.
      const app = await html(`${prefix}/apps/ordinary-app`);
      const safety = /data-testid="package-safety"[\s\S]*?<\/section>/.exec(app)?.[0] ?? '';
      assert.ok(links(safety, `${prefix}/security/clamav`, title('clamav')), safety || `${prefix} no safety facts`);
      // Security-category apps link the two features the promo names, and /security.
      const guard = await html(`${prefix}/apps/guard-app`);
      const promo = /<section[^>]*aria-labelledby="package-security-promo"[\s\S]*?<\/section>/.exec(guard)?.[0] ?? '';
      for (const topic of ['clamav', 'firewall'])
        assert.ok(links(promo, `${prefix}/security/${topic}`, title(topic)), `${prefix} promo → ${topic}`);
      assert.ok(promo.includes(`href="${prefix}/security"`), `${prefix} promo still links /security`);
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
    // The rate-limited group also skips share images, feeds, query variants and the closed languages
    // (here the settings name none, so the six app languages are open); the default group does not.
    const limited = text.split('\n\n').find(group => group.startsWith('User-agent: AhrefsBot')) ?? '';
    for (const path of ['/_og/', '/*/feed.xml', '/*?', '/de/', '/fr/', '/it/', '/zh-hant/'])
      assert.match(limited, new RegExp(`^Disallow: ${path.replace(/[*?]/g, '\\$&')}$`, 'm'), path);
    assert.doesNotMatch(text.split('\n\n')[0], /_og|feed\.xml|\/\*\?|\/de\/$/m);
    // The served file follows the open languages in the settings, like the admin preview.
    try {
      siteSettings.pages.openLocales = ['en-US', 'zh-CN', 'ja-JP', 'es-ES', 'ru-RU'];
      await invalidatePaths(['/robots.txt'], true);
      const closed = await (await fetch(`${origin}/robots.txt`)).text();
      assert.match(closed, /^Disallow: \/pt\/$/m, closed);
      assert.doesNotMatch(closed, /^Disallow: \/ja\/$/m);
    } finally {
      delete siteSettings.pages.openLocales;
      await invalidatePaths(['/robots.txt'], true);
    }
  });
  await t.test('G6 llms is plain English text with public HTML links and a fixed origin metric', async () => {
    const response = await fetch(`${origin}/llms.txt?locale=de&tracking=fixture`, {
      headers: { 'user-agent': 'OpenNavo-Test/1.0' }
    });
    const body = await response.text();
    assert.equal(response.status, 200, logs);
    assert.match(response.headers.get('content-type') ?? '', /^text\/plain; charset=utf-8/);
    assert.equal(response.headers.get('cache-control'), 'no-store');
    assert.equal(response.headers.get('x-robots-tag'), 'noindex, follow');
    assert.match(body, /^# OpenNavo\n/);
    const links = [...body.matchAll(/\]\(([^)]+)\)/g)].map(match => new URL(match[1]));
    assert.deepEqual(
      links.map(url => url.pathname),
      ['/', '/download', '/apps', '/security', '/about']
    );
    for (const url of links) {
      assert.equal(url.origin, 'https://example.test');
      const target = await fetch(`${origin}${url.pathname}`, { redirect: 'manual' });
      await target.arrayBuffer();
      assert.equal(target.status, 200, url.pathname);
    }
    assert.ok(!/\.md|tracking=|locale=|\/admin/.test(body));
    const sitemap = await (await fetch(`${origin}/__sitemap__/static.xml`)).text();
    assert.ok(!sitemap.includes('/llms.txt'));
    const metrics = await (
      await fetch(`${internal}/metrics`, {
        headers: { authorization: `Bearer ${internalSecret}` }
      })
    ).text();
    assert.match(
      metrics,
      /agent="self_test",route="llms",status="200",verification="ua_only",verification_basis="none"} [1-9]/
    );
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
    const document = JSDOM.fragment(page);
    // PR1 removes the templated lead. The title, authored description and shared spec card retain facts.
    assert.equal(document.querySelector('[data-testid="app-answer"]'), null);
    assert.equal(document.querySelector('main h1')?.textContent.trim(), 'Facts App');
    assert.equal(
      document.querySelector('main a[href="/categories/developer-tools"]')?.textContent.trim(),
      'Developer Tools'
    );
    assert.equal(
      document.querySelector('main .on-about-collapsed .on-markdown')?.textContent.trim(),
      'A synthetic package for SSR regression tests.'
    );
    const specs = document.querySelectorAll('[data-testid="package-spec-card"]');
    assert.equal(specs.length, 1, 'one shared specification card');
    assert.ok(specs[0].textContent.includes('$9.99 per month'), 'verified price remains in the spec card');
    assert.ok(specs[0].textContent.includes('macOS 13 or later'), 'verified requirement remains in the spec card');
    const section = id => new RegExp(`data-testid="${id}"[\\s\\S]*?</section>`).exec(page)?.[0] ?? '';
    // T2 splits the former guide/facts cards into install, uninstall, price and requirements sections.
    const guide = section('package-install');
    assert.ok(guide.includes('Homebrew installs the app in your Applications folder.'), guide);
    assert.ok(guide.includes('href="https://vendor.example/FactsApp.dmg"'));
    const checksumDisclaimer = 'SHA-256 only checks that the download is complete; it is not a security certification.';
    assert.ok(guide.includes(checksumDisclaimer), 'download instructions retain the checksum limitation');
    assert.equal(document.querySelector('main')?.textContent.split(checksumDisclaimer).length - 1, 1);
    assert.ok(
      section('package-safety').includes('This is not a safety certification.'),
      'the safety section retains its broader security-facts disclaimer'
    );
    assert.ok(
      section('package-uninstall').includes('data-testid="package-zap"'),
      'zap only because the definition lists files'
    );
    const facts = section('package-price');
    const requirements = section('package-requirements');
    assert.ok(!requirements.includes('Pending verification'), requirements);
    assert.ok(requirements.includes('macOS 13 or later'), requirements);
    assert.ok(facts.includes('vendor.example/pricing'));
    assert.ok(facts.includes('$9.99 per month'));
    const alternatives = section('package-alternatives');
    assert.ok(alternatives.includes('href="/apps/ordinary-app"'));
    // Unified facts retain the evidenced open-source comparison and reject the unsupported legacy limit.
    assert.ok(alternatives.includes('Open source'));
    assert.ok(!alternatives.includes('Fewer features'));
    const faq = section('package-faq');
    // Questions are headings inside collapsed details, in search order.
    assert.match(faq, /<details[\s\S]*?<summary[^>]*><h3[^>]*>Is Facts App safe\?<\/h3>/);
    // Inline code renders each word in its own element (OnCodeText), so compare the text without tags.
    const faqText = faq.replace(/<[^>]*>/g, '');
    assert.ok(faqText.includes('checked Dec 1, 2025'), 'existing FAQ source attribution retains evidence check dates');
    assert.ok(faqText.includes('Is Facts App free?') && faqText.includes('requires a subscription'), faqText);
    assert.ok(faqText.includes('brew uninstall --zap --cask facts-app'), 'collapsed answers are in the HTML');
    assert.ok(faqText.includes('Its settings are in ~/Library/Preferences/facts.plist.'), faqText);
    assert.ok(!faqText.includes('without Homebrew'), 'no boilerplate question');
    // Normalization marks the entire conflicting vendor requirement needs_review; FAQ cannot assert it.
    assert.ok(!faq.includes('The vendor states it runs natively on Apple silicon.'), faq);
    const [app] = graphOf(page).filter(node => node['@type'] === 'SoftwareApplication');
    assert.equal(app.offers.price, 9.99);
    assert.equal(app.offers.priceCurrency, 'USD');
    assert.equal(app.offers.priceValidUntil, '2026-12-01');
    // JSON-LD now uses the same localized, verified requirements sentence as the facts table.
    assert.equal(app.softwareRequirements, 'macOS 13 or later');
    assert.equal(app.downloadUrl, 'https://vendor.example/FactsApp.dmg');
    assert.ok(!('aggregateRating' in app), 'no invented ratings');
    // Other languages retain the same authored description and localize the shared spec card.
    const japanese = JSDOM.fragment(await html('/ja/apps/facts-app'));
    assert.equal(japanese.querySelector('[data-testid="app-answer"]'), null);
    assert.equal(japanese.querySelector('main h1')?.textContent.trim(), 'Facts App');
    assert.equal(
      japanese.querySelector('main a[href="/ja/categories/developer-tools"]')?.textContent.trim(),
      'Developer Tools'
    );
    assert.equal(
      japanese.querySelector('main .on-about-collapsed .on-markdown')?.textContent.trim(),
      'A synthetic package for SSR regression tests.'
    );
    assert.equal(japanese.querySelector('[data-testid="package-spec-card"] h2')?.textContent, '基本情報');
    // Apps without price evidence have no offer; fonts are not SoftwareApplication.
    const ordinary = await html('/apps/ordinary-app');
    const [plain] = graphOf(ordinary).filter(node => node['@type'] === 'SoftwareApplication');
    assert.ok(!('offers' in plain));
    // Empty price/requirement collections omit their new fact sections, as the former facts card did.
    assert.ok(!ordinary.includes('data-testid="package-price"'), 'no price section without prices');
    assert.ok(!ordinary.includes('data-testid="package-requirements"'), 'no requirements section without requirements');
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
  await t.test('package SEO repairs render facts, dates, downloads and compact releases in SSR', async () => {
    const page = async path => {
      const response = await fetch(`${origin}${path}`, { headers: { accept: 'text/html' } });
      assert.equal(response.status, 200, `${path}\n${logs}`);
      return response.text();
    };
    const visible = html =>
      html.replace(/<script\b[^>]*>[\s\S]*?<\/script>/g, '').replace(/<style\b[^>]*>[\s\S]*?<\/style>/g, '');
    const notion = visible(await page('/apps/notion'));
    const notionDocument = JSDOM.fragment(notion);
    const developer = [...notionDocument.querySelectorAll('[data-testid="package-spec-card"] dt')].find(
      term => term.textContent.trim() === 'Developer'
    );
    assert.equal(developer?.nextElementSibling?.textContent.trim(), 'Notion Labs, Inc.');
    assert.ok(
      notionDocument.querySelector('main h1')?.parentElement?.textContent.includes('Notion Labs, Inc.'),
      'the title area still names the developer after removing the templated lead'
    );
    assert.ok(!notion.includes('Inc..'));
    const empty = visible(await page('/apps/repair-empty'));
    assert.ok(!empty.includes('id="whats-new"'));
    assert.ok(!empty.includes('Pending verification'));
    assert.ok(!empty.includes('id="price"'));
    const facts = visible(await page('/apps/facts-app'));
    const header =
      /<section\b[^>]*class="[^"]*bg-component-detail-header-bg[^"]*"[\s\S]*?<\/section>/.exec(facts)?.[0] ?? '';
    assert.match(
      header,
      /<a\b[^>]*href="https:\/\/vendor\.example\/FactsApp\.dmg"[^>]*>\s*Official download · vendor\.example\s*<\/a>/
    );
    assert.match(header, /aria-label="Get Facts App"/);
    assert.ok(header.includes('SHA-256 recorded by Homebrew'));
    assert.match(header, /<time[^>]*datetime="2025-12-01T08:00:00Z"[^>]*>[^<]*2025[^<]*<\/time>/);
    for (const path of ['/apps/ordinary-app', '/apps/repair-disabled', '/apps/lumen-notes'])
      assert.ok(!visible(await page(path)).includes('Official download ·'), path);
    const details = visible(await page('/apps/facts-app/details'));
    const labels = new Set(
      [...(facts + details).matchAll(/<button\b[^>]*aria-label="([^"]+)"/g)].map(match => match[1])
    );
    for (const label of [
      'Copy install command',
      'Copy uninstall command',
      'Copy uninstall and cleanup command',
      'Copy SHA-256 checksum'
    ])
      assert.ok(labels.has(label), label);
    const versions = visible(await page('/apps/cleanmymac/versions'));
    assert.ok(!versions.includes('No separate release notes'));
    assert.ok(!versions.includes('Compatibility Unknown'));
    assert.ok(!versions.includes('5.0,501'));
    const timeline = /<ol\b[^>]*class="[^"]*on-version-timeline[^"]*"[\s\S]*?<\/ol>/.exec(versions)?.[0] ?? '';
    assert.equal([...timeline.matchAll(/<li\b/g)].length, 23, '24 API rows coalesce one build/base pair');
    assert.equal([...timeline.matchAll(/>\s*5\.0\s*</g)].length, 1, 'one visible base version');
    assert.ok(versions.includes('All 24'), 'filter count retains API total');
    assert.ok(versions.includes('With notes 5'), 'filter count retains notes total');
    for (const [name, html] of [
      ['overview', notion],
      ['facts', facts],
      ['versions', versions]
    ]) {
      const times = [...html.matchAll(/<time\b[^>]*datetime="([^"]+)"[^>]*>([\s\S]*?)<\/time>/g)];
      assert.ok(times.length > 0, name);
      for (const [, datetime, label] of times) assert.ok(label.includes(datetime.slice(0, 4)), `${name}: ${label}`);
      assert.ok(!/\b(?:\d+\s+)?(?:days?|hours?|minutes?|weeks?|months?) ago\b/i.test(html), name);
    }
    assert.ok(!/\b(?:\d+\s+)?(?:days?|hours?|minutes?|weeks?|months?) ago\b/i.test(details), 'details');
    assert.ok(notion.includes('id="whats-new"'), 'positive notes still render');
  });
  await t.test('package subpages resolve their about node without offers or ratings', async () => {
    for (const path of [
      '/apps/ordinary-app/versions',
      '/apps/facts-app/details',
      '/apps/dependency-content-app/dependencies',
      '/apps/variant-app@beta/troubleshooting'
    ]) {
      const response = await fetch(`${origin}${path}`, { headers: { accept: 'text/html' } });
      assert.equal(response.status, 200, path);
      const html = await response.text();
      const graph = JSON.parse(
        /<script type="application\/ld\+json"[^>]*>([\s\S]*?)<\/script>/.exec(html)?.[1] ?? '{}'
      )['@graph'];
      const webPage = graph.find(node => [node['@type']].flat().includes('WebPage') && node.about);
      assert.ok(webPage?.about?.['@id'], path);
      const app = graph.find(node => node['@id'] === webPage.about['@id']);
      assert.equal(app?.['@type'], 'SoftwareApplication', path);
      assert.deepEqual(
        Object.keys(app).sort(),
        ['@id', '@type', 'name', 'operatingSystem', 'softwareVersion', 'url'].sort(),
        path
      );
      assert.ok(!('offers' in app) && !('aggregateRating' in app), path);
    }
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
    assert.match(await image('/security'), /c_OgDefaultSatori,.*variant_home/);
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
  await t.test('an admitted OG image completes its internal island at a one-request limit', async () => {
    const imageReservation = createPortReservation();
    imageReservation.listen(0, '127.0.0.1');
    await once(imageReservation, 'listening');
    const imagePort = String(imageReservation.address().port);
    let metricsReservation;
    let metricsPort;
    for (let attempt = 0; attempt < 100; attempt++) {
      metricsReservation = createPortReservation();
      metricsPort = String(30000 + ((Number(imagePort) + attempt) % 10000));
      metricsReservation.listen(Number(metricsPort), '0.0.0.0');
      try {
        await once(metricsReservation, 'listening');
        break;
      } catch (error) {
        if (error.code !== 'EADDRINUSE' || attempt === 99) throw error;
      }
    }
    await new Promise(resolve => imageReservation.close(resolve));
    await new Promise(resolve => metricsReservation.close(resolve));
    const limited = spawn(process.execPath, [entry], {
      env: {
        ...rendererEnv,
        PORT: imagePort,
        NITRO_PORT: imagePort,
        NUXT_METRICS_PORT: metricsPort,
        NUXT_LOAD_SHED_IMAGE_IN_FLIGHT: '1',
        NUXT_LOAD_SHED_LOW_IN_FLIGHT: '1',
        NUXT_LOAD_SHED_MEDIUM_IN_FLIGHT: '1',
        NUXT_LOAD_SHED_EXTREME_IN_FLIGHT: '1',
        NUXT_LOAD_SHED_LOW_LAG_MS: '60000',
        NUXT_LOAD_SHED_MEDIUM_LAG_MS: '60000',
        NUXT_LOAD_SHED_EXTREME_LAG_MS: '60000',
        OPENNAVO_SITEMAP_WARMUP: 'false'
      },
      stdio: ['ignore', 'pipe', 'pipe']
    });
    const ended = once(limited, 'exit');
    let output = '';
    limited.stderr.on('data', chunk => {
      output += chunk;
    });
    try {
      await new Promise((resolve, reject) => {
        const timer = setTimeout(() => reject(new Error(output || 'image test startup timeout')), 15000);
        limited.once('error', error => {
          clearTimeout(timer);
          reject(error);
        });
        limited.once('exit', () => {
          clearTimeout(timer);
          reject(new Error(output));
        });
        limited.stdout.on('data', chunk => {
          output += chunk;
          if (output.includes('Listening on')) {
            clearTimeout(timer);
            resolve();
          }
        });
      });
      const page = await (await fetch(`http://127.0.0.1:${imagePort}/apps/ordinary-app`)).text();
      const imageUrl = /<meta property="og:image" content="([^"]+)"/.exec(page)?.[1]?.replaceAll('&amp;', '&');
      assert.ok(imageUrl, output);
      const response = await fetch(imageUrl.replace('https://example.test', `http://127.0.0.1:${imagePort}`), {
        headers: { 'user-agent': 'PerplexityBot' }
      });
      assert.equal(response.status, 200, output);
      assert.equal(response.headers.get('content-type'), 'image/png');
      assert.ok((await response.arrayBuffer()).byteLength > 1000);
      const metrics = await (await fetch(`http://127.0.0.1:${metricsPort}/metrics`)).text();
      assert.match(metrics, /opennavo_web_image_in_flight 0/);
      assert.match(metrics, /opennavo_web_in_flight 0/);
      assert.ok(!metrics.includes('level="extreme"'), metrics);
    } finally {
      limited.kill('SIGTERM');
      const timer = setTimeout(() => limited.kill('SIGKILL'), 5000);
      await ended;
      clearTimeout(timer);
    }
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
    // C2: articles.seo.title in en-US uses the shorter editorial title.
    assert.match(first.html, /<title>Mac App Picks, Reviews and Tutorials · OpenNavo<\/title>/);
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
    assert.match(second.html, /<title>Mac App Picks, Reviews and Tutorials · Page 2 · OpenNavo<\/title>/);
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
  await t.test('public pages hide translation provenance in every site locale', async () => {
    for (const locale of SITE_LOCALES) {
      const { prefix } = LOCALE_METADATA[locale];
      for (const path of ['/articles/provenance-check', '/apps/ordinary-app', '/collections/editors']) {
        const response = await fetch(`${origin}${prefix}${path}`);
        const html = await response.text();
        assert.equal(response.status, 200, `${locale} ${path}: ${logs}`);
        const document = JSDOM.fragment(html);
        for (const node of document.querySelectorAll('script, style')) node.remove();
        const visible = document.textContent;
        for (const label of translationProvenanceLabels[locale])
          assert.ok(!visible.includes(label), `${locale} ${path}: ${label}`);
        assert.ok(html.includes('machineTranslated'), 'API provenance remains in the payload');
        assert.equal(
          document.querySelector('link[rel="canonical"]')?.getAttribute('href'),
          `https://example.test${prefix}${path}`
        );
      }
    }
  });
  await t.test('article pages render the full body with honest structured data and languages', async () => {
    const response = await fetch(`${origin}/zh/articles/choose-a-screenshot-app`, {
      headers: { accept: 'text/html' }
    });
    const html = await response.text();
    assert.equal(response.status, 200, logs);
    assert.match(html, /<title>如何选择截图工具 · OpenNavo<\/title>/);
    assert.match(html, /<h1[^>]*>\s*如何选择截图工具\s*<\/h1>/);
    assert.match(html, /<h2 id="怎么选">怎么选<\/h2>/);
    assert.ok(html.includes('<table>'), 'Markdown tables render');
    assert.ok(html.includes('href="#对比"'), 'table of contents links to anchors');
    assert.equal([...html.matchAll(/data-testid="article-app-card"/g)].length, 2, 'unknown apps render no card');
    assert.ok(html.includes('当前目录数据'));
    const byline = JSDOM.fragment(html).querySelector('[data-testid="article-byline"]');
    assert.ok(byline && !/AI 翻译|查看原文/.test(byline.textContent), 'translation provenance is hidden');
    assert.ok(!byline.querySelector('a[href="/articles/choose-a-screenshot-app"]'), 'no provenance link');
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
    assert.equal(post.inLanguage, 'zh-Hans');
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
      await fetch(`${origin}/ja/articles/choose-a-screenshot-app`, {
        headers: { accept: 'text/html' }
      })
    ).text();
    assert.match(ja, /<meta name="robots" content="noindex, follow"/);
    assert.match(ja, /<article[^>]*lang="en-US"/);
    assert.ok(ja.includes('data-testid="article-source-text"'));
    // App pages link back to the articles that cover them, by stable entity IDs in structured data.
    const app = await (await fetch(`${origin}/apps/ordinary-app`, { headers: { accept: 'text/html' } })).text();
    assert.ok(/data-testid="package-articles"[\s\S]*href="\/articles\/choose-a-screenshot-app"/.test(app));
    assert.ok(app.includes('"@id":"https://example.test/apps/ordinary-app#app"'));
  });
  await t.test('article body links follow the page locale, including fallback text and cached responses', async () => {
    // Each request has the same article revision; the body payload key already separates locales.
    for (const prefix of ['/zh', '/ja', '/zh-hant', '/es', '/ru', '', '/zh']) {
      const response = await fetch(`${origin}${prefix}/articles/choose-a-screenshot-app`);
      const html = await response.text();
      assert.equal(response.status, 200, logs);
      const document = new JSDOM(html).window.document;
      const body = document.querySelector('[data-testid="article-body"]');
      const links = new Map([...body.querySelectorAll('a')].map(link => [link.textContent, link]));
      const expected = {
        'Relative app': `${prefix}/apps/ordinary-app?notes=1&original=1#install`,
        'Absolute app': `${prefix}/apps/ordinary-app#install`,
        'WWW app': `${prefix}/apps/ordinary-app`,
        'Traditional app': `${prefix}/apps/ordinary-app`,
        'Simplified app': `${prefix}/apps/ordinary-app`,
        'Dotted app': `${prefix}/apps/tool.app`,
        'Dotted label': `${prefix}/background-items/com.Example.js`,
        'Article feed': `${prefix}/articles/feed.xml`,
        'App feed': `${prefix}/apps/ordinary-app/feed.xml`,
        'Collection feed': `${prefix}/collections/picks/feed.xml`,
        API: '/api/v1/articles?raw=1#data',
        Asset: '/_og/article.png',
        'Other feed': '/articles/feed.json',
        External: 'https://vendor.example/docs',
        Anchor: '#install',
        Email: 'mailto:reader@example.test'
      };
      for (const [label, href] of Object.entries(expected)) {
        assert.equal(links.get(label)?.getAttribute('href'), href, `${prefix || 'English'}: ${label}`);
        if (href.startsWith('/') || href.startsWith('#'))
          assert.equal(links.get(label)?.hasAttribute('target'), false, `${label} stays in this tab`);
      }
      assert.equal(links.get('External').getAttribute('target'), '_blank');
      assert.equal(body.querySelector('pre code').textContent, '[Block](/zh/apps/ordinary-app)\n');
      assert.equal(body.querySelector('p code').textContent, '[Inline](/zh-hant/apps/ordinary-app)');
      assert.equal(body.querySelector('img').getAttribute('src'), '/images/article-capture.png');
      assert.ok(html.includes(`article-body:choose-a-screenshot-app:${prefix.slice(1) || 'en'}:2`));
    }
  });
  await t.test('article languages closed for indexing are noindex and absent from hreflang', async () => {
    const html = async path => (await fetch(`${origin}${path}`, { headers: { accept: 'text/html' } })).text();
    const alternates = page => [...page.matchAll(/<link[^>]+hreflang="([^"]+)"/g)].map(match => match[1]).sort();
    const robots = page => [...page.matchAll(/<meta name="robots" content="([^"]*)"/g)].map(match => match[1]);
    const paths = ['/articles/choose-a-screenshot-app', '/zh/articles/choose-a-screenshot-app'];
    try {
      // L1: the Chinese text is ready (availableLocales), but the site has the language closed for indexing.
      siteSettings.pages.openLocales = ['en-US', 'de-DE'];
      assert.equal((await invalidatePaths(paths, true)).settingsCleared, true);
      const en = await html(paths[0]);
      assert.deepEqual(alternates(en), ['en', 'x-default']);
      assert.ok(!robots(en).some(value => value.includes('noindex')), robots(en).join());
      const zh = await html(paths[1]);
      assert.deepEqual(robots(zh), ['noindex, follow']);
      assert.deepEqual(alternates(zh), ['en', 'x-default']);
      // The page still shows its ready translation, without the source-text notice.
      assert.match(zh, /<h1[^>]*>\s*如何选择截图工具\s*<\/h1>/);
      assert.ok(!zh.includes('data-testid="article-source-text"'));
    } finally {
      delete siteSettings.pages.openLocales;
      await invalidatePaths(paths, true);
    }
    const reopened = await html('/zh/articles/choose-a-screenshot-app');
    assert.ok(!robots(reopened).some(value => value.includes('noindex')), 'reopened languages index again');
    assert.deepEqual(alternates(reopened), ['en', 'x-default', 'zh', 'zh-Hans']);
  });
  await t.test('moved, withdrawn and unknown articles get 301, 410 and 404', async () => {
    const moved = await fetch(`${origin}/zh/articles/old-slug`, { redirect: 'manual' });
    assert.equal(moved.status, 301, logs);
    assert.equal(new URL(moved.headers.get('location'), origin).pathname, '/zh/articles/choose-a-screenshot-app');
    const withdrawn = await fetch(`${origin}/articles/pulled-article`, {
      headers: { accept: 'text/html' }
    });
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
    // No label page is indexable in the fixture: an empty urlset must not be listed (Search Console flags it).
    assert.ok(!xml.includes('/__sitemap__/background-items.xml'), xml);
  });
  await t.test(
    'browse leaves offer only languages with the same page; static sitemaps follow open languages',
    async () => {
      const html = async path => (await fetch(`${origin}${path}`, { headers: { accept: 'text/html' } })).text();
      const alternates = page => [...page.matchAll(/<link[^>]+hreflang="([^"]+)"/g)].map(match => match[1]).sort();
      const paths = ['/browse/o', '/browse/p', '/zh/browse/p'];
      try {
        siteSettings.pages.openLocales = ['en-US', 'de-DE', 'zh-CN', 'zh-Hant'];
        await invalidatePaths(paths, true);
        const leaf = await html('/browse/p');
        assert.deepEqual(alternates(leaf), ['en', 'x-default', 'zh', 'zh-HK', 'zh-Hans', 'zh-Hant', 'zh-TW'].sort());
        assert.ok(!leaf.includes('href="/de/browse/p"'), 'the language menu does not link a missing page');
        assert.ok(leaf.includes('href="/zh-hant/browse/p"'), 'it still links the languages that have the page');
        assert.ok(alternates(await html('/browse/o')).includes('de'));
        // Each language's static sitemap keeps open languages with the zh-Hant aliases, without the noindex list.
        const statics = await (await fetch(`${origin}/__sitemap__/en-static.xml`)).text();
        assert.ok(statics.includes('hreflang="zh-TW"'), statics);
        assert.ok(!statics.includes('hreflang="ja"'), 'closed languages are not alternates');
        assert.ok(!/<loc>[^<]*\/background-items<\/loc>/.test(statics), 'the list without a ready label is noindex');
        const index = await (await fetch(`${origin}/sitemap_index.xml`)).text();
        assert.ok(index.includes('/__sitemap__/zh-Hant-static.xml'), index);
        assert.ok(!index.includes('/__sitemap__/ja-static.xml'), 'a closed language has no static sitemap');
      } finally {
        delete siteSettings.pages.openLocales;
        await invalidatePaths(paths, true);
      }
    }
  );
  await t.test('author pages, feeds and sitemaps cover published articles', async () => {
    const response = await fetch(`${origin}/authors/lin-wei`, {
      headers: { accept: 'text/html' }
    });
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
  await t.test(
    'hot page caches isolate locales and query variants, invalidate content, and never store 503s',
    async () => {
      const paths = [
        '/recent',
        '/ja/recent?page=2',
        '/apps/ordinary-app/versions',
        '/zh/apps/ordinary-app/versions?notes=1'
      ];
      const prefixes = ['/recent', '/ja/recent', '/apps/ordinary-app', '/zh/apps/ordinary-app'];
      const request = async path => {
        const response = await fetch(origin + path, { headers: { accept: 'text/html' } });
        return { response, html: await response.text() };
      };
      const imageURL = html => /<meta property="og:image" content="([^"]+)"/.exec(html)?.[1];
      try {
        await invalidatePaths(prefixes);
        const before = [];
        for (const path of paths) {
          const first = await request(path);
          assert.equal(first.response.status, 200, path);
          assert.match(
            first.response.headers.get('cache-control'),
            path.includes('/recent') ? /s-maxage=60/ : /s-maxage=21600/
          );
          const reads = [...hotReads];
          const second = await request(path);
          assert.equal(second.html, first.html, `${path}: warm HTML`);
          assert.deepEqual([...hotReads], reads, `${path}: a cache hit must not call the API`);
          before.push(first.html);
        }
        assert.notEqual(imageURL(before[0]), imageURL(before[1]), 'localized images have separate URLs');
        assert.notEqual(imageURL(before[2]), imageURL(before[3]), 'localized app images have separate URLs');
        hotVersion = '11.99';
        assert.equal((await request(paths[2])).html, before[2], 'content is cached until the change-feed purge');
        await invalidatePaths(prefixes);
        for (const [index, path] of paths.entries()) {
          const updated = await request(path);
          assert.equal(updated.response.status, 200);
          assert.ok(updated.html.includes('11.99'), `${path}: new version after invalidation`);
          if (path.includes('/versions'))
            assert.notEqual(imageURL(updated.html), imageURL(before[index]), 'content version rotates OG URL');
        }
        for (const [path, failingAPI] of [
          ['/recent', '/api/v1/packages'],
          ['/apps/ordinary-app/versions', '/api/v1/packages/cask/ordinary-app/releases']
        ]) {
          await invalidatePaths(prefixes);
          hotFailure = failingAPI;
          for (let attempt = 0; attempt < 2; attempt++) {
            const reads = hotReads.get(failingAPI);
            const failed = await request(path);
            assert.equal(failed.response.status, 503, path);
            for (const header of ['cache-control', 'cdn-cache-control', 'cloudflare-cdn-cache-control'])
              assert.equal(failed.response.headers.get(header), 'no-store', `${path}: ${header}`);
            assert.ok(hotReads.get(failingAPI) > reads, 'an error must be retried rather than cached');
          }
          hotFailure = '';
          assert.equal((await request(path)).response.status, 200, 'recovery needs no cache purge');
        }
      } finally {
        hotFailure = '';
        hotVersion = '';
        await invalidatePaths(prefixes);
      }
    }
  );
  await t.test('OG images reuse bounded storage and advertise immutable caching only for valid images', async () => {
    const html = await (await fetch(origin + '/ja/about')).text();
    const url = /<meta property="og:image" content="([^"]+)"/.exec(html)?.[1]?.replaceAll('&amp;', '&');
    assert.ok(url);
    assert.match(url, /build_[^,]+/, 'shared template/font edits rotate the build input in the signed URL');
    const local = url.replace('https://example.test', origin);
    const first = await fetch(local);
    assert.equal(first.status, 200, logs);
    const bytes = Buffer.from(await first.arrayBuffer());
    const second = await fetch(local);
    assert.equal(second.status, 200, logs);
    assert.equal(second.headers.get('x-og-cache'), 'HIT');
    assert.deepEqual(Buffer.from(await second.arrayBuffer()), bytes);
    assert.equal(second.headers.get('etag'), first.headers.get('etag'));
    assert.match(second.headers.get('cache-control'), /max-age=2592000.*immutable/);
    assert.match(second.headers.get('vary'), /host/i);
    assert.match(second.headers.get('vary'), /accept-encoding/i);
    const invalid = await fetch(local.replace(/s_[^,/.]+/, 's_invalid'));
    assert.ok(invalid.status >= 400, 'an invalid signature is rejected');
    assert.equal(invalid.headers.get('cache-control'), 'no-store');
    await invalid.arrayBuffer();
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
      assert.equal(last, 404, `the expired 200 must not outlive the page\n${logs}`);
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
  await t.test(
    'Redis snapshot timeouts return retryable uncached sitemaps and recover on the same URL',
    async subtest => {
      const redis = await redisFixture(subtest);
      const redisReservation = createPortReservation().listen(0, '127.0.0.1');
      await once(redisReservation, 'listening');
      const redisPort = String(redisReservation.address().port);
      await new Promise(resolve => redisReservation.close(resolve));
      const instance = spawn(process.execPath, [entry], {
        env: {
          ...rendererEnv,
          PORT: redisPort,
          NITRO_PORT: redisPort,
          NUXT_REDIS_URL: redis.url,
          NUXT_METRICS_PORT: '',
          OPENNAVO_SITEMAP_WARMUP: 'false'
        },
        stdio: ['ignore', 'pipe', 'pipe']
      });
      const exit = once(instance, 'exit');
      let output = '';
      instance.stderr.on('data', chunk => {
        output += chunk;
      });
      subtest.after(async () => {
        instance.kill('SIGTERM');
        const timeout = setTimeout(() => instance.kill('SIGKILL'), 5000);
        try {
          await exit;
        } finally {
          clearTimeout(timeout);
        }
      });
      await new Promise((resolve, reject) => {
        const timeout = setTimeout(() => reject(new Error(output)), 15000);
        instance.once('error', reject);
        instance.once('exit', () => reject(new Error(output)));
        instance.stdout.on('data', chunk => {
          output += chunk;
          if (output.includes('Listening on')) {
            clearTimeout(timeout);
            resolve();
          }
        });
      });
      const redisOrigin = `http://127.0.0.1:${redisPort}`;
      const paths = [
        `/__sitemap__/${packageSitemapName('apps', 'plus-app++')}.xml`,
        `/__sitemap__/${packageSitemapName('versions', 'plus-app++')}.xml`
      ];
      for (const [index, path] of paths.entries()) {
        // Both overview and versions now read only apps while the font experiment is off.
        redis.fail(index === 0 ? 'sitemap-package-manifest' : 'sitemap-package-snapshots-v2:apps:');
        for (let attempt = 0; attempt < 2; attempt++) {
          const response = await fetch(redisOrigin + path, {
            headers: { accept: attempt === 0 ? 'application/json' : 'text/html' }
          });
          assert.equal(response.status, 503, output);
          assert.equal(response.headers.get('retry-after'), '120');
          for (const header of ['cache-control', 'cdn-cache-control', 'cloudflare-cdn-cache-control'])
            assert.equal(response.headers.get(header), 'no-store', header);
          assert.ok(!(await response.text()).includes('<urlset'), 'no successful sitemap body on failure');
        }
        // The sitemap module may coalesce consecutive requests still settling.
        // One or two apps reads each fail twice (initial read + retry).
        assert.ok([2, 4].includes(redis.failures()), `apps snapshot attempts: ${redis.failures()}`);
        assert.ok(output.includes('snapshot read failed after retry; returning 503'));
        redis.fail('');
        const recovered = await fetch(redisOrigin + path);
        assert.equal(recovered.status, 200, output);
        assert.ok(
          (await recovered.text()).includes('https://example.test/apps/plus-app++'),
          'failed XML was not cached'
        );
      }
      backgroundItemsReady = true;
      try {
        const response = await fetch(`${redisOrigin}/__sitemap__/background-items.xml`);
        assert.equal(response.status, 200, output);
        const xml = await response.text();
        for (const label of backgroundLabels) {
          const path = labelPath(label);
          assert.ok(xml.includes(`<loc>https://example.test${path}</loc>`), `sitemap loc for ${label}`);
          assert.ok(xml.includes(`href="https://example.test/zh${path}"`), `sitemap alternate for ${label}`);
        }
      } finally {
        backgroundItemsReady = false;
      }
    }
  );
  assert.deepEqual(seoApiRequests, []);
  assert.deepEqual(
    [...requestedTokens].sort(),
    [
      ...seoApps,
      ...qaTokens,
      'dependency-content-app',
      'facts-app',
      'font-sample',
      'guard-app',
      'image-alt-app',
      'install-deprecated',
      'install-paused',
      'ordinary-app',
      'plus-app++',
      'reverse-dependency-app',
      'opennavo',
      'thin-app',
      'variant-app@beta',
      'versioned-app@11',
      'wechat',
      'notion',
      'cleanmymac',
      'repair-empty',
      'repair-disabled'
    ].sort()
  );
});
