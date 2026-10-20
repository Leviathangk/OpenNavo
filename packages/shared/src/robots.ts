import { SITE_LOCALES, LOCALE_METADATA } from './locales.gen';

// robots.txt is generated at runtime from the admin SEO settings (WP6/WP24). The web server
// and the admin preview both call buildRobotsTxt, so the preview matches the served file.

export type CrawlerMode = 'allow' | 'limit' | 'block';

export interface CrawlerRule {
  mode: CrawlerMode;
  delaySeconds: number;
  bots: string[];
}

export interface CrawlerPolicy {
  training: CrawlerRule;
  seoTools: CrawlerRule;
}

// Search/index, user-triggered fetchers and existing preview/inspection tools use the default
// group. Locking is a settings guard, not a claim about bot identity or a WAF exemption.
// Keep this list aligned with management.lockedCrawlers; the builder also drops locked overrides.
export const LOCKED_CRAWLERS = [
  'Googlebot',
  'Googlebot-Image',
  'Googlebot-News',
  'Googlebot-Video',
  'Storebot-Google',
  'Google-InspectionTool', // Inspection, not an AI answer request.
  'AdsBot-Google', // Advertising checks.
  'Bingbot',
  'BingPreview', // Preview, not proof of a Copilot request.
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
  'Meta-WebIndexer', // Search indexing.
  'Meta-ExternalFetcher', // User-triggered retrieval.
  'MistralAI-Index',
  'MistralAI-User',
  'Yeti'
] as const;

// Site search and Brewfile lists stay disallowed for every crawler: both open an unbounded URL
// space and are not meant to be indexed (05 §6.4).
export const ROBOTS_PRIVATE_PATHS = SITE_LOCALES.flatMap(locale =>
  ['/search', '/brewfile'].map(path => `${LOCALE_METADATA[locale].prefix}${path}`)
);

// Heavy, low-value responses that rate-limited crawlers also skip.
export const ROBOTS_LIMITED_PATHS = ['/*_payload.json', '/*/dependencies'];

// Same values as store.DefaultSEOSettings: the policy in place before the settings page.
// GPTBot, Google-Extended, Applebot-Extended, Meta-ExternalAgent (mixed training/indexing) and
// MistralAI-Training remain unlocked and follow *. Extended tokens control uses, not separate UAs.
// Crawl-delay is advisory robots policy; it does not enforce a request rate limit.
export const DEFAULT_CRAWLER_POLICY: CrawlerPolicy = {
  training: { mode: 'limit', delaySeconds: 10, bots: ['ClaudeBot'] },
  seoTools: { mode: 'block', delaySeconds: 10, bots: ['MJ12bot', 'SerpstatBot'] }
};

export interface RobotsTxtOptions {
  crawlers: CrawlerPolicy;
  /** Non-production sites disallow everything. */
  indexable: boolean;
  /** Absolute sitemap index URL. */
  sitemap?: string;
}

const LOCKED = new Set<string>([...LOCKED_CRAWLERS.map(bot => bot.toLowerCase()), '*']);

function group(agents: string[], rules: string[]) {
  return [...agents.map(agent => `User-agent: ${agent}`), ...rules].join('\n');
}

export function buildRobotsTxt({ crawlers, indexable, sitemap }: RobotsTxtOptions): string {
  if (!indexable) return 'User-agent: *\nDisallow: /\n';
  const privateRules = ROBOTS_PRIVATE_PATHS.map(path => `Disallow: ${path}`);
  const groups = [group(['*'], privateRules)];
  // SEO tools first, then training crawlers: the order used before runtime generation.
  for (const rule of [crawlers.seoTools, crawlers.training]) {
    const bots = [...new Set(rule.bots.filter(bot => !LOCKED.has(bot.toLowerCase())))];
    if (rule.mode === 'allow' || !bots.length) continue;
    // A crawler that matches its own group ignores the default group, so limit repeats the private paths.
    const rules =
      rule.mode === 'block'
        ? ['Disallow: /']
        : [
            `Crawl-delay: ${rule.delaySeconds}`,
            ...privateRules,
            ...ROBOTS_LIMITED_PATHS.map(path => `Disallow: ${path}`)
          ];
    groups.push(group(bots, rules));
  }
  if (sitemap) groups.push(`Sitemap: ${sitemap}`);
  return `${groups.join('\n\n')}\n`;
}
