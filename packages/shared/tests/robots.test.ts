import { describe, expect, it } from 'vitest';
import {
  DEFAULT_CRAWLER_POLICY,
  LOCKED_CRAWLERS,
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
  '/ru/brewfile'
].map(path => `Disallow: ${path}`);

describe('robots.txt generation', () => {
  it('Default policy keeps search engines on the default group, blocks SEO tools and slows ClaudeBot', () => {
    expect(ROBOTS_PRIVATE_PATHS).toHaveLength(12);
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
        'Disallow: /*/dependencies',
        '',
        'Sitemap: https://opennavo.example/sitemap_index.xml',
        ''
      ].join('\n')
    );
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
