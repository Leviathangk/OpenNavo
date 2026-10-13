import { webRouteType } from '@opennavo/shared';

// Origin request counters by user-agent class, route type and status (WP8). Every label comes from
// a fixed list, so no token, URL, IP or query ever becomes a label. Cloudflare answers cached pages
// without reaching the origin: these counts show what reached Nitro, not all crawler traffic, and a
// user-agent can be forged (trusted identification needs the vendors' published IP ranges).

const AGENTS: [string, RegExp][] = [
  // User-requested and search fetchers before the training crawlers whose names they contain.
  ['oai_searchbot', /OAI-SearchBot/i],
  ['chatgpt_user', /ChatGPT-User/i],
  ['gptbot', /GPTBot/i],
  ['claude_searchbot', /Claude-SearchBot/i],
  ['claude_user', /Claude-User/i],
  ['claudebot', /ClaudeBot|anthropic-ai/i],
  ['perplexity_user', /Perplexity-User/i],
  ['perplexitybot', /PerplexityBot/i],
  ['googlebot', /Googlebot/i],
  ['google_other', /GoogleOther|Google-InspectionTool|Storebot-Google|AdsBot-Google/i],
  ['bingbot', /bingbot|BingPreview|msnbot/i],
  ['applebot', /Applebot/i],
  ['duckduckbot', /DuckDuckBot|DuckAssistBot/i],
  ['yandexbot', /YandexBot|YandexImages/i],
  ['baiduspider', /Baiduspider/i],
  ['ccbot', /CCBot/i],
  ['bytespider', /Bytespider/i],
  ['meta', /meta-externalagent|meta-externalfetcher|facebookexternalhit/i],
  ['amazonbot', /Amazonbot/i],
  ['seo_tool', /AhrefsBot|SemrushBot|MJ12bot|SerpstatBot|DotBot|BLEXBot|DataForSeoBot/i],
  ['other_bot', /bot\b|crawler|spider|slurp/i]
];

export function agentLabel(userAgent: string | undefined): string {
  if (!userAgent) return 'unknown';
  return AGENTS.find(([, pattern]) => pattern.test(userAgent))?.[0] ?? 'browser';
}

const STATUSES = new Set([200, 301, 302, 304, 400, 403, 404, 410, 429, 500, 502, 503]);

export function statusLabel(status: number): string {
  return STATUSES.has(status) ? String(status) : `${Math.floor(status / 100)}xx`;
}

// Route types are shared with the GA4 page_type parameter.
export const routeLabel = webRouteType;

export class RequestCounter {
  private readonly counts = new Map<string, number>();

  add(agent: string, route: string, status: string) {
    const key = `${agent}\u0000${route}\u0000${status}`;
    this.counts.set(key, (this.counts.get(key) ?? 0) + 1);
  }

  /** Prometheus text exposition format. */
  render(): string {
    const lines = [
      '# HELP opennavo_web_requests_total Requests that reached the web origin, by user-agent class, route type and status.',
      '# TYPE opennavo_web_requests_total counter'
    ];
    for (const [key, value] of [...this.counts].sort(([a], [b]) => a.localeCompare(b))) {
      const [agent, route, status] = key.split('\u0000');
      lines.push(`opennavo_web_requests_total{agent="${agent}",route="${route}",status="${status}"} ${value}`);
    }
    return `${lines.join('\n')}\n`;
  }
}
