import { describe, expect, it } from 'vitest';
import {
  DEFAULT_CRAWLER_POLICY,
  LOCKED_CRAWLERS,
  ROBOTS_LIMITED_PATHS,
  ROBOTS_PRIVATE_PATHS,
  ROBOTS_CONTENT_SIGNAL,
  buildRobotsTxt,
  closedLocalePaths,
  type CrawlerPolicy
} from '../src/robots';
import { LOCALES, SITE_LOCALES } from '../src/locales.gen';

// Every site language open, so the closed-language rules stay out of the way unless a test names them.
const open = { openLocales: SITE_LOCALES };

const privateRules = [
  '/search',
  '/brewfile',
  '/zh/search',
  '/zh/brewfile',
  '/ja/search',
  '/ja/brewfile',
  '/es/search',
  '/es/brewfile',
  '/pt/search',
  '/pt/brewfile',
  '/ru/search',
  '/ru/brewfile',
  '/de/search',
  '/de/brewfile',
  '/fr/search',
  '/fr/brewfile',
  '/it/search',
  '/it/brewfile',
  '/zh-hant/search',
  '/zh-hant/brewfile'
].map(path => `Disallow: ${path}`);
const defaultRules = [ROBOTS_CONTENT_SIGNAL, ...privateRules];

describe('robots.txt generation', () => {
  it('signals search, AI input and AI training in the default group', () => {
    const text = buildRobotsTxt({ crawlers: DEFAULT_CRAWLER_POLICY, indexable: true, ...open });
    const [everyone] = text.split('\n\n');
    expect(everyone).toContain(`User-agent: *\n${ROBOTS_CONTENT_SIGNAL}\n`);
    expect(text.match(new RegExp(ROBOTS_CONTENT_SIGNAL, 'g'))).toHaveLength(1);
  });

  it('uses only known directives and keeps the exact Content-Signal in the default group', () => {
    const text = buildRobotsTxt({
      crawlers: DEFAULT_CRAWLER_POLICY,
      indexable: true,
      ...open,
      sitemap: 'https://opennavo.example/sitemap_index.xml'
    });
    const knownDirectives = new Set(['user-agent', 'allow', 'disallow', 'sitemap', 'content-signal', 'crawl-delay']);

    for (const line of text.split('\n')) {
      const trimmed = line.trim();
      if (!trimmed || trimmed.startsWith('#')) continue;
      const colon = trimmed.indexOf(':');
      expect(colon, trimmed).toBeGreaterThan(0);
      expect(knownDirectives, trimmed).toContain(trimmed.slice(0, colon).toLowerCase());
    }

    const [everyone] = text.split('\n\n');
    expect(everyone).toContain('User-agent: *\nContent-Signal: search=yes, ai-input=yes, ai-train=yes\n');
    expect(text.match(/Content-Signal: .*/g)).toEqual(['Content-Signal: search=yes, ai-input=yes, ai-train=yes']);
  });

  it('Search, user-fetch and existing diagnostic tokens cannot be blocked or limited', () => {
    const expected = [
      'Googlebot',
      'Googlebot-Image',
      'Googlebot-News',
      'Googlebot-Video',
      'Storebot-Google',
      'Google-InspectionTool',
      'AdsBot-Google',
      'Bingbot',
      'BingPreview',
      'msnbot',
      'OAI-SearchBot',
      'ChatGPT-User',
      'Claude-SearchBot',
      'Claude-User',
      'PerplexityBot',
      'Perplexity-User',
      'DuckDuckBot',
      'DuckAssistBot',
      'Applebot',
      'YandexBot',
      'Baiduspider',
      'Meta-WebIndexer',
      'Meta-ExternalFetcher',
      'Amzn-SearchBot',
      'Amzn-User',
      'MistralAI-Index',
      'MistralAI-User',
      'Yeti'
    ];
    expect([...LOCKED_CRAWLERS].sort()).toEqual([...expected].sort());
    for (const category of ['training', 'seoTools'] as const) {
      for (const mode of ['block', 'limit'] as const) {
        const rule = { mode: 'allow' as const, delaySeconds: 10, bots: [] };
        const policy: CrawlerPolicy = { training: rule, seoTools: rule };
        policy[category] = { mode, delaySeconds: 10, bots: expected.map(bot => bot.toUpperCase()) };
        expect(buildRobotsTxt({ crawlers: policy, indexable: true, ...open })).toBe(
          ['User-agent: *', ...defaultRules, ''].join('\n')
        );
      }
    }
  });

  it('Slows the heavy training crawlers by default and leaves use-control tokens configurable', () => {
    const slowed = [
      'ClaudeBot',
      'GPTBot',
      'Amazonbot',
      'Bytespider',
      'CCBot',
      'Meta-ExternalAgent',
      // Anthropic's older user agent for ClaudeBot.
      'anthropic-ai',
      'MistralAI-Training'
    ];
    expect(DEFAULT_CRAWLER_POLICY.training).toEqual({ mode: 'limit', delaySeconds: 10, bots: slowed });
    const defaults = buildRobotsTxt({ crawlers: DEFAULT_CRAWLER_POLICY, indexable: true, ...open });
    expect(defaults).toContain(`${slowed.map(bot => `User-agent: ${bot}`).join('\n')}\nCrawl-delay: 10\n`);
    for (const bot of ['Google-Extended', 'Applebot-Extended']) {
      expect(defaults).not.toContain(`User-agent: ${bot}\n`);
      const crawlers: CrawlerPolicy = {
        ...DEFAULT_CRAWLER_POLICY,
        training: { mode: 'block', delaySeconds: 10, bots: [bot] }
      };
      expect(buildRobotsTxt({ crawlers, indexable: true, ...open })).toContain(`User-agent: ${bot}\nDisallow: /\n`);
    }
  });

  it('Default policy keeps search engines on the default group, blocks SEO tools and slows training crawlers', () => {
    expect(ROBOTS_PRIVATE_PATHS).toHaveLength(20);
    expect(
      buildRobotsTxt({
        crawlers: DEFAULT_CRAWLER_POLICY,
        indexable: true,
        // The six app languages, as before the site opened the others.
        openLocales: LOCALES,
        sitemap: 'https://opennavo.example/sitemap_index.xml'
      })
    ).toBe(
      [
        'User-agent: *',
        ...defaultRules,
        '',
        'User-agent: MJ12bot',
        'User-agent: SerpstatBot',
        'Disallow: /',
        '',
        'User-agent: ClaudeBot',
        'User-agent: GPTBot',
        'User-agent: Amazonbot',
        'User-agent: Bytespider',
        'User-agent: CCBot',
        'User-agent: Meta-ExternalAgent',
        'User-agent: anthropic-ai',
        'User-agent: MistralAI-Training',
        'Crawl-delay: 10',
        ...privateRules,
        'Disallow: /*_payload.json',
        'Disallow: /apps/*/dependencies',
        'Disallow: /*/apps/*/dependencies',
        'Disallow: /apps/*/details',
        'Disallow: /*/apps/*/details',
        'Disallow: /_og/',
        'Disallow: /*/feed.xml',
        'Disallow: /*?',
        'Disallow: /de/',
        'Disallow: /fr/',
        'Disallow: /it/',
        'Disallow: /zh-hant/',
        '',
        'Sitemap: https://opennavo.example/sitemap_index.xml',
        ''
      ].join('\n')
    );
  });

  it('Rate-limited crawlers skip query variants, share images, feeds and closed languages; no one else does', () => {
    // Production indexes five languages: Portuguese and the four newer ones are closed.
    const openLocales = ['en-US', 'zh-CN', 'ja-JP', 'es-ES', 'ru-RU'] as const;
    expect(closedLocalePaths(openLocales)).toEqual(['/pt/', '/de/', '/fr/', '/it/', '/zh-hant/']);
    expect(closedLocalePaths(SITE_LOCALES)).toEqual([]);
    // The default language has no prefix and is never closed, even when settings leave it out.
    expect(closedLocalePaths(['zh-CN'])).not.toContain('/');
    const policy: CrawlerPolicy = {
      training: { mode: 'limit', delaySeconds: 10, bots: ['GPTBot', 'Googlebot', 'Amazonbot'] },
      seoTools: { mode: 'block', delaySeconds: 10, bots: ['MJ12bot'] }
    };
    const [everyone, blocked, limited] = buildRobotsTxt({ crawlers: policy, indexable: true, openLocales }).split(
      '\n\n'
    );
    const extra = ['/_og/', '/*/feed.xml', '/*?', '/pt/', '/de/', '/fr/', '/it/', '/zh-hant/'].map(
      path => `Disallow: ${path}`
    );
    for (const rule of extra) expect(limited, rule).toContain(`${rule}\n`);
    for (const prefix of ['/zh/', '/ja/', '/es/', '/ru/']) expect(limited).not.toContain(`Disallow: ${prefix}\n`);
    // The default group (search engines, link previews such as Twitterbot) and blocked crawlers stay as they were.
    expect(everyone).toBe(['User-agent: *', ...defaultRules].join('\n'));
    expect(blocked).toBe('User-agent: MJ12bot\nDisallow: /');
    // A locked crawler named in the limited group still follows the default group.
    expect(limited).toMatch(/^User-agent: GPTBot\nUser-agent: Amazonbot\nCrawl-delay: 10\n/);
    expect(limited).not.toContain('Googlebot');
  });

  it('Limits only package subpages, without blocking tokens named details or dependencies', () => {
    // robots wildcards match slashes too; the /apps/ and following token segment are both required.
    const limited = (path: string) =>
      ROBOTS_LIMITED_PATHS.some(rule =>
        new RegExp(
          `^${rule
            .split('*')
            .map(part => part.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'))
            .join('.*')}`
        ).test(path)
      );
    for (const prefix of ['', '/zh', '/zh-hant', '/de']) {
      for (const tab of ['details', 'dependencies']) {
        expect(limited(`${prefix}/apps/editor@11/${tab}`)).toBe(true);
        expect(limited(`${prefix}/apps/${tab}`)).toBe(false);
        expect(limited(`${prefix}/apps/${tab}-tool`)).toBe(false);
        expect(limited(`${prefix}/apps/${tab}-tool/versions`)).toBe(false);
      }
      expect(limited(`${prefix}/apps/editor/_payload.json`)).toBe(true);
    }
    expect(limited('/articles/dependencies')).toBe(false);
    // Share images, every feed and any query variant; pages themselves stay reachable.
    for (const path of [
      '/_og/s/c_OgPackageSatori,token_vlc.png',
      '/apps/vlc/feed.xml',
      '/zh/apps/vlc/feed.xml',
      '/articles/feed.xml',
      '/collections/editors/feed.xml',
      '/apps?page=2',
      '/zh/rankings?sort=new'
    ])
      expect(limited(path), path).toBe(true);
    for (const path of ['/apps/vlc', '/zh/apps/vlc/versions', '/articles', '/collections/editors'])
      expect(limited(path), path).toBe(false);
    const [defaults] = buildRobotsTxt({ crawlers: DEFAULT_CRAWLER_POLICY, indexable: true, ...open }).split('\n\n');
    expect(defaults).toBe(['User-agent: *', ...defaultRules].join('\n'));
  });

  it('Non-production sites disallow everything', () => {
    expect(
      buildRobotsTxt({
        crawlers: DEFAULT_CRAWLER_POLICY,
        indexable: false,
        ...open,
        sitemap: 'https://x.example/s.xml'
      })
    ).toBe('User-agent: *\nDisallow: /\n');
  });

  it('Allow mode, empty classes and locked crawlers add no group', () => {
    const policy: CrawlerPolicy = {
      training: { mode: 'block', delaySeconds: 5, bots: ['Googlebot', 'claude-user', '*'] },
      seoTools: { mode: 'allow', delaySeconds: 5, bots: ['MJ12bot'] }
    };
    expect(buildRobotsTxt({ crawlers: policy, indexable: true, ...open })).toBe(
      ['User-agent: *', ...defaultRules, ''].join('\n')
    );
    const empty: CrawlerPolicy = { ...policy, training: { mode: 'limit', delaySeconds: 5, bots: [] } };
    expect(buildRobotsTxt({ crawlers: empty, indexable: true, ...open })).not.toContain('Crawl-delay');
  });

  it('Blocked training crawlers keep only real names, once each', () => {
    const policy: CrawlerPolicy = {
      training: { mode: 'block', delaySeconds: 5, bots: ['GPTBot', 'OAI-SearchBot', 'GPTBot', 'CCBot'] },
      seoTools: { mode: 'limit', delaySeconds: 30, bots: ['AhrefsBot'] }
    };
    const text = buildRobotsTxt({ crawlers: policy, indexable: true, ...open });
    expect(text).toContain('User-agent: GPTBot\nUser-agent: CCBot\nDisallow: /\n');
    expect(text).toContain('User-agent: AhrefsBot\nCrawl-delay: 30\n');
    expect(text.indexOf('AhrefsBot')).toBeLessThan(text.indexOf('GPTBot'));
    for (const bot of LOCKED_CRAWLERS) expect(text).not.toContain(`User-agent: ${bot}\n`);
    expect(text).not.toContain('Sitemap:');
  });
});
