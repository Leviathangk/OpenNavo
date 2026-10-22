import { describe, expect, it } from 'vitest';
import {
  DEFAULT_CRAWLER_POLICY,
  LOCKED_CRAWLERS,
  ROBOTS_LIMITED_PATHS,
  ROBOTS_PRIVATE_PATHS,
  buildRobotsTxt,
  type CrawlerPolicy
} from '../src/robots';

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

describe('robots.txt generation', () => {
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
        expect(buildRobotsTxt({ crawlers: policy, indexable: true })).toBe(
          ['User-agent: *', ...privateRules, ''].join('\n')
        );
      }
    }
  });

  it('Preserves training and use-control defaults while leaving them configurable', () => {
    const unlocked = ['GPTBot', 'Google-Extended', 'Applebot-Extended', 'Meta-ExternalAgent', 'MistralAI-Training'];
    const defaults = buildRobotsTxt({ crawlers: DEFAULT_CRAWLER_POLICY, indexable: true });
    expect(DEFAULT_CRAWLER_POLICY.training).toEqual({ mode: 'limit', delaySeconds: 10, bots: ['ClaudeBot'] });
    for (const bot of unlocked) {
      expect(defaults).not.toContain(`User-agent: ${bot}\n`);
      const crawlers: CrawlerPolicy = {
        ...DEFAULT_CRAWLER_POLICY,
        training: { mode: 'block', delaySeconds: 10, bots: [bot] }
      };
      expect(buildRobotsTxt({ crawlers, indexable: true })).toContain(`User-agent: ${bot}\nDisallow: /\n`);
    }
  });

  it('Default policy keeps search engines on the default group, blocks SEO tools and slows ClaudeBot', () => {
    expect(ROBOTS_PRIVATE_PATHS).toHaveLength(20);
    expect(
      buildRobotsTxt({
        crawlers: DEFAULT_CRAWLER_POLICY,
        indexable: true,
        sitemap: 'https://opennavo.example/sitemap_index.xml'
      })
    ).toBe(
      [
        'User-agent: *',
        ...privateRules,
        '',
        'User-agent: MJ12bot',
        'User-agent: SerpstatBot',
        'Disallow: /',
        '',
        'User-agent: ClaudeBot',
        'Crawl-delay: 10',
        ...privateRules,
        'Disallow: /*_payload.json',
        'Disallow: /apps/*/dependencies',
        'Disallow: /*/apps/*/dependencies',
        'Disallow: /apps/*/details',
        'Disallow: /*/apps/*/details',
        '',
        'Sitemap: https://opennavo.example/sitemap_index.xml',
        ''
      ].join('\n')
    );
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
    const [defaults] = buildRobotsTxt({ crawlers: DEFAULT_CRAWLER_POLICY, indexable: true }).split('\n\n');
    expect(defaults).toBe(['User-agent: *', ...privateRules].join('\n'));
  });

  it('Non-production sites disallow everything', () => {
    expect(
      buildRobotsTxt({ crawlers: DEFAULT_CRAWLER_POLICY, indexable: false, sitemap: 'https://x.example/s.xml' })
    ).toBe('User-agent: *\nDisallow: /\n');
  });

  it('Allow mode, empty classes and locked crawlers add no group', () => {
    const policy: CrawlerPolicy = {
      training: { mode: 'block', delaySeconds: 5, bots: ['Googlebot', 'claude-user', '*'] },
      seoTools: { mode: 'allow', delaySeconds: 5, bots: ['MJ12bot'] }
    };
    expect(buildRobotsTxt({ crawlers: policy, indexable: true })).toBe(
      ['User-agent: *', ...privateRules, ''].join('\n')
    );
    const empty: CrawlerPolicy = { ...policy, training: { mode: 'limit', delaySeconds: 5, bots: [] } };
    expect(buildRobotsTxt({ crawlers: empty, indexable: true })).not.toContain('Crawl-delay');
  });

  it('Blocked training crawlers keep only real names, once each', () => {
    const policy: CrawlerPolicy = {
      training: { mode: 'block', delaySeconds: 5, bots: ['GPTBot', 'OAI-SearchBot', 'GPTBot', 'CCBot'] },
      seoTools: { mode: 'limit', delaySeconds: 30, bots: ['AhrefsBot'] }
    };
    const text = buildRobotsTxt({ crawlers: policy, indexable: true });
    expect(text).toContain('User-agent: GPTBot\nUser-agent: CCBot\nDisallow: /\n');
    expect(text).toContain('User-agent: AhrefsBot\nCrawl-delay: 30\n');
    expect(text.indexOf('AhrefsBot')).toBeLessThan(text.indexOf('GPTBot'));
    for (const bot of LOCKED_CRAWLERS) expect(text).not.toContain(`User-agent: ${bot}\n`);
    expect(text).not.toContain('Sitemap:');
  });
});
