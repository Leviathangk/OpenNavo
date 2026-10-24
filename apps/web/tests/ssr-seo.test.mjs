import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { createHash } from 'node:crypto';
import { once } from 'node:events';
import { access, readdir, readFile } from 'node:fs/promises';
import { createServer } from 'node:http';
import { createServer as createPortReservation } from 'node:net';
import { fileURLToPath } from 'node:url';
import { test } from 'node:test';

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
function pageDecisions(token, locale, locales, notesCount = 0, hasDependencyContent = false) {
  const indexable = locales.includes(locale);
  const decision = (allowed, hreflang = [], canonicalPath = '', reason = '') => ({
    robots: allowed ? 'index, follow' : 'noindex, follow',
    canonicalPath,
    hreflang,
    inSitemap: allowed && !canonicalPath,
    announce: allowed && !canonicalPath,
    reason
  });
  const overview = decision(indexable, indexable ? locales : [], '', indexable ? '' : 'locale_not_ready');
  return {
    overview,
    versions: decision(indexable && notesCount >= 3, indexable && notesCount >= 3 ? locales : []),
    // Mirror seo.DecidePage: empty dependencies and details consolidate even in an unpublished locale.
    dependencies: hasDependencyContent
      ? decision(indexable, indexable ? locales : [], '', indexable ? '' : 'locale_not_ready')
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
  const experimentUrls = [];
  const hasPayloadKey = (html, key) => {
    const json = /<script[^>]+id="__NUXT_DATA__"[^>]*>([\s\S]*?)<\/script>/.exec(html)?.[1];
    return json && JSON.parse(json).some(value => value && typeof value === 'object' && Object.hasOwn(value, key));
  };
  const experimentFixtures = {
    'seo-free': { variant: 'verified_free' },
    'seo-version': { variant: 'current_version' },
    'seo-uninstall': { variant: 'uninstall_alternatives' },
    'seo-control': { variant: 'current_version', arm: 'control' },
    'seo-ineligible': { variant: 'current_version', eligible: false },
    'seo-unverified': { variant: 'verified_free' },
    'seo-error': {},
    'seo-timeout': {}
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
    names: ['Facts App'],
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
    if (path === '/api/v1/seo/experiment') {
      const canonical = query.get('url');
      experimentUrls.push(canonical);
      const token = new URL(canonical).pathname.split('/').at(-1);
      // A hung upstream never answers. The page must still finish with its original metadata.
      if (token === 'seo-timeout') return;
      if (token === 'seo-error') status = 503;
      data = {
        experimentId: 42,
        arm: 'treatment',
        variant: null,
        templateVersion: 'snippet-v1',
        eligible: true,
        expiresAt: '2026-12-01T00:00:00Z',
        ...(experimentFixtures[token] ?? { experimentId: null, arm: 'none', eligible: false })
      };
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
    } else if (
      /^\/api\/v1\/packages\/cask\/(ordinary-app|dependency-content-app|reverse-dependency-app)\/dependencies$/.test(
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
    } else if (
      ['/api/v1/packages/cask/plus-app++/releases', '/api/v1/packages/cask/ordinary-app/releases'].includes(path)
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
        ...Object.keys(experimentFixtures),
        'versioned-app@11',
        'plus-app++',
        'ordinary-app',
        'dependency-content-app',
        'reverse-dependency-app',
        'thin-app',
        'font-sample',
        'facts-app',
        'wechat',
        'guard-app'
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
            ...(token === 'ordinary-app' ? { articles: [roundupCard(false)] } : {}),
            ...(token === 'facts-app' || token === 'seo-uninstall' ? factsApp : {}),
            ...(token.startsWith('seo-') ? { displayName: 'SEO App', names: ['SEO App'] } : {}),
            ...(['seo-free', 'seo-unverified'].includes(token)
              ? {
                  price: {
                    type: 'free',
                    amount: 0,
                    currency: 'USD',
                    region: 'US',
                    plan: 'Personal',
                    sourceUrl: 'https://vendor.example/pricing',
                    verifiedAt: '2025-12-01T08:00:00Z',
                    stale: token === 'seo-unverified'
                  }
                }
              : {}),
            // A security-category app, which points to OpenNavo's own malware scanning.
            ...(token === 'guard-app'
              ? {
                  categories: [{ slug: 'security', name: 'Security & Privacy', icon: 'lucide:shield' }]
                }
              : {}),
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
      data = {
        current,
        size: 12,
        total,
        records: current === 1 ? all : current === 2 ? [tutorialCard] : []
      };
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
        body: null,
        coverUrl: null,
        itemCount: 0,
        updatedAt: '2026-10-01T00:00:00Z',
        sourceLocale: 'en-US',
        machineTranslated: false,
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
      if (query.get('kind') === 'fonts') data = { buckets: [], items: [] };
      else if (!bucket)
        data = {
          buckets: ['o', 'p'].map(key => ({ key, count: inBucket(key).length, pages: 1 })),
          items: []
        };
      else if (inBucket(bucket).length && (query.get('page') ?? '1') === '1')
        data = { buckets: [], items: inBucket(bucket) };
      else status = 404;
    } else if (path === '/api/v1/background-items') {
      // The footer's encyclopedia link participates in the three-hop crawl.
      data = { current: Number(query.get('current') ?? 1), size: 24, total: 0, records: [] };
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
      data.collections = [];
      data.recommendationPaused = false;
      // L1: current entity decisions can open all ten locales; keep the explicit legacy fixture.
      if (!data.indexing && data.token !== 'versioned-app@11')
        data.indexing = { locales: Object.keys(localePrefixes), notesCount: data.releaseCount };
      if (data.indexing)
        data.indexing.pages = pageDecisions(
          data.token,
          query.get('locale') ?? req.headers['accept-language'] ?? 'en-US',
          data.indexing.locales,
          data.indexing.notesCount,
          ['dependency-content-app', 'reverse-dependency-app'].includes(data.token)
        );
    }
    if (path === '/api/v1/sitemap/packages' && data)
      for (const record of data.records)
        for (const localized of record.locales) {
          localized.pages = pageDecisions(
            record.token,
            localized.locale,
            record.locales.map(item => item.locale),
            record.notesCount
          );
          localized.pageLastmod = { overview: localized.lastmod, versions: localized.lastmod };
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
    NUXT_INTERNAL_SECRET: internalSecret
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
        env: { ...rendererEnv, PORT: comparePort, NITRO_PORT: comparePort, NUXT_METRICS_PORT: compareMetricsPort },
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
  for (const prefix of ['', '/zh', '/ja', '/es', '/pt', '/ru']) {
    await t.test(`${prefix || '/en (root)'} versioned detail`, async () => {
      const path = `${prefix}/apps/versioned-app@11`;
      const response = await fetch(`${origin}${path}`, { headers: { accept: 'text/html' } });
      const html = await response.text();
      assert.equal(response.status, 200, `${path}\n${logs}`);
      assert.match(html, /<title>[^<]*Example App @11[^<]*OpenNavo<\/title>/);
      // S1/C3: an absent subtitle leaves a Vue conditional comment after the exact heading.
      assert.match(html, /<h1\b[^>]*>Example App @11(?:<!--(?:v-if)?-->)?<\/h1>/);
      // T2: the facts/install blocks render the complete copyable command in a single code element.
      assert.match(html, /<code\b[^>]*>brew install --cask versioned-app@11<\/code>/);
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
    const response = await fetch(`${origin}/apps/lumen-notes`, {
      headers: { accept: 'text/html' }
    });
    const html = await response.text();
    assert.equal(response.status, 200, logs);
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
      for (const id of references) assert.ok(ids.has(id), `${path}: ${id} is referenced but not in the graph`);
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
    // Reverse relations are content too, even with no declared dependency or conflict summary.
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
        assert.equal(/<meta name="robots" content="[^"]*noindex/.test(html), prefix === '/ja');
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
    assert.ok(!robots(overview).some(value => value.includes('noindex')), robots(overview).join());
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
      const response = await fetch(`${origin}${loc}`, {
        redirect: 'manual',
        headers: { accept: 'text/html' }
      });
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
  });
  await t.test('only public cached pages may be cached at the edge', async () => {
    const edge = async path => {
      const response = await fetch(`${origin}${path}`, { headers: { accept: 'text/html' } });
      await response.text();
      return [response.status, response.headers.get('cloudflare-cdn-cache-control')];
    };
    // App pages with their tabs, and article pages, are purged on change and after each release, so
    // they stay for hours.
    assert.deepEqual(await edge('/apps/ordinary-app'), [200, 'max-age=21600, stale-while-revalidate=86400']);
    assert.deepEqual(await edge('/apps/ordinary-app/details'), [200, 'max-age=21600, stale-while-revalidate=86400']);
    // X5: collections now use the same short edge cache as other public lists; search stays private.
    assert.equal((await edge('/search?q=editor'))[1], 'no-store');
    assert.deepEqual(await edge('/collections'), [200, 'max-age=600, stale-while-revalidate=3600']);
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
  await t.test('H3 renders stable canonical assignments, with locally verified facts', async () => {
    for (const [path, expected] of [
      ['/apps/seo-free', 'SEO App Free for Mac – Download &amp; Install · OpenNavo'],
      ['/zh/apps/seo-free', 'SEO App 免费 Mac 版下载安装 · OpenNavo'],
      ['/ja/apps/seo-free', 'SEO App 無料 for Mac ダウンロード・インストール · OpenNavo'],
      ['/apps/seo-version?utm_source=fixture', 'SEO App 11.0 for Mac – Download &amp; Install · OpenNavo'],
      ['/apps/seo-control', 'SEO App for Mac – Download &amp; Install · OpenNavo'],
      ['/apps/seo-ineligible', 'SEO App for Mac – Download &amp; Install · OpenNavo'],
      ['/apps/seo-unverified', 'SEO App for Mac – Download &amp; Install · OpenNavo']
    ]) {
      const response = await fetch(`${origin}${path}`, { headers: { accept: 'text/html' } });
      assert.equal(response.status, 200, `${path}\n${logs}`);
      const html = await response.text();
      assert.equal(/<title>([^<]*)<\/title>/.exec(html)?.[1], expected, path);
      const canonical = /rel="canonical" href="([^"]*)"/.exec(html)?.[1];
      assert.equal(canonical, `https://example.test${path.split('?')[0]}`);
      assert.ok(experimentUrls.includes(canonical), 'assignment uses the actual canonical, including its language');
      assert.ok(
        hasPayloadKey(html, `seo-experiment:${canonical}`),
        'canonical key and assignment travel in the SSR payload'
      );
      assert.equal(
        experimentUrls.filter(url => url === canonical).length,
        1,
        'one assignment request per rendered page'
      );
    }
    assert.ok(!experimentUrls.some(url => url.includes('utm_source')));
  });
  await t.test('H3 experiment errors and timeouts preserve successful SSR and the control title', async () => {
    for (const token of ['seo-error', 'seo-timeout']) {
      const response = await fetch(`${origin}/apps/${token}`, {
        headers: { accept: 'text/html' },
        signal: AbortSignal.timeout(3000)
      });
      assert.equal(response.status, 200, logs);
      const html = await response.text();
      assert.equal(/<title>([^<]*)<\/title>/.exec(html)?.[1], 'SEO App for Mac – Download &amp; Install · OpenNavo');
      assert.ok(html.includes('data-testid="package-install"'));
      assert.ok(hasPayloadKey(html, `seo-experiment:https://example.test/apps/${token}`));
      assert.equal(experimentUrls.filter(url => url.endsWith(`/apps/${token}`)).length, 1, 'failures are not retried');
    }
  });
  await t.test('C2 descriptions name visible modules and H3 prioritizes uninstall and alternatives', async () => {
    const page = async path => {
      const response = await fetch(`${origin}${path}`, { headers: { accept: 'text/html' } });
      assert.equal(response.status, 200, logs);
      const html = await response.text();
      return { html, description: /<meta name="description" content="([^"]*)"/.exec(html)?.[1] ?? '' };
    };
    const basic = await page('/apps/seo-control');
    assert.ok(basic.description.includes('installation and uninstallation'));
    assert.doesNotMatch(basic.description, /pricing|system requirements|alternatives|release history/);
    const free = await page('/apps/seo-free');
    assert.ok(free.description.includes('pricing'));
    assert.ok(free.html.includes('data-testid="package-price"'));
    const full = await page('/apps/seo-uninstall');
    assert.match(
      full.description,
      /Explore uninstallation, alternatives, installation, pricing, and system requirements/
    );
    for (const id of ['package-uninstall', 'package-alternatives', 'package-requirements', 'package-price'])
      assert.ok(full.html.includes(`data-testid="${id}"`), id);
    // The eight untranslated locales fall back to English keys while retaining locale list punctuation.
    for (const prefix of Object.values(localePrefixes)) {
      const { description } = await page(`${prefix}/apps/seo-control`);
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
      indexed(sized, path);
      assert.equal((await get(target.replace('https://example.test', ''))).status, 200, target);
      indexed(await get(`${path}?page=2`), `${path}?page=2`);
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
    const lead = /data-testid="app-answer"[^>]*>([^<]*)</.exec(page)?.[1];
    // T2 (appAnswer.ts): the lead identifies the app; sourced price and requirements live in their blocks.
    assert.equal(lead, 'Facts App is a Developer Tools app for Mac.');
    const section = id => new RegExp(`data-testid="${id}"[\\s\\S]*?</section>`).exec(page)?.[0] ?? '';
    // T2 splits the former guide/facts cards into install, uninstall, price and requirements sections.
    const guide = section('package-install');
    assert.ok(guide.includes('Homebrew installs the app in your Applications folder.'), guide);
    assert.ok(guide.includes('href="https://vendor.example/FactsApp.dmg"'));
    assert.ok(guide.includes('it is not a security certification'));
    assert.ok(
      section('package-uninstall').includes('data-testid="package-zap"'),
      'zap only because the definition lists files'
    );
    const facts = section('package-price');
    const requirements = section('package-requirements');
    assert.ok(requirements.includes('Pending verification'), requirements);
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
    // Other languages render the same answer in their own words.
    assert.match(
      await html('/ja/apps/facts-app'),
      /data-testid="app-answer"[^>]*>Facts App は「Developer Tools」カテゴリ/
    );
    // Apps without evidence keep a plain lead and no offer; fonts are not SoftwareApplication.
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
        NUXT_LOAD_SHED_EXTREME_LAG_MS: '60000'
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
  });
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
  assert.deepEqual(
    [...requestedTokens].sort(),
    [
      ...Object.keys(experimentFixtures).sort(),
      'dependency-content-app',
      'facts-app',
      'font-sample',
      'guard-app',
      'ordinary-app',
      'plus-app++',
      'reverse-dependency-app',
      'thin-app',
      'versioned-app@11',
      'wechat'
    ].sort()
  );
});
