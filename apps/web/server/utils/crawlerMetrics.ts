import { webRouteType } from '@opennavo/shared';
import { getHeader, getResponseStatus, type H3Event } from 'h3';
import { isTrustedProxy } from './client-ip';

// Origin request counters by user-agent class, route type and status (WP8). Every label comes from
// a fixed list, so no token, URL, IP or query ever becomes a label. Cloudflare answers cached pages
// without reaching the origin: these counts show what reached Nitro, not all crawler traffic, and a
// user-agent can be forged. cf.client.bot attests a known good bot, never the claimed vendor identity.

const AGENTS = [
  ['indexnow_probe', /\bOpenNavo-Probe\/[^ ]+\s+IndexNow\b/i],
  ['self_test', /\bOpenNavo-(?:Probe|Test)\b/i],
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
  ['bing_preview', /BingPreview/i],
  ['bingbot', /bingbot|msnbot/i],
  ['applebot', /Applebot\b(?!-Extended)/i],
  ['duckassistbot', /DuckAssistBot/i],
  ['duckduckbot', /DuckDuckBot/i],
  ['yandexbot', /YandexBot|YandexImages/i],
  ['baiduspider', /Baiduspider/i],
  ['ccbot', /CCBot/i],
  ['bytespider', /Bytespider/i],
  ['meta_webindexer', /Meta-WebIndexer/i],
  ['meta_externalfetcher', /Meta-ExternalFetcher/i],
  ['meta_externalagent', /Meta-ExternalAgent/i],
  ['meta_preview', /facebookexternalhit|Facebot/i],
  ['link_preview', /Twitterbot|Slackbot|LinkedInBot|Discordbot|TelegramBot|WhatsApp/i],
  ['mistral_index', /MistralAI-Index/i],
  ['mistral_user', /MistralAI-User/i],
  ['mistral_training', /MistralAI-Training/i],
  // These are robots policy tokens, not documented independent HTTP fetchers.
  ['unknown', /Google-Extended|Applebot-Extended/i],
  // Amazon's search and user-triggered fetchers come before Amazonbot: their user agents may link a page
  // whose address contains "amazonbot", which must not make them training traffic.
  ['amzn_searchbot', /Amzn-SearchBot/i],
  ['amzn_user', /Amzn-User/i],
  ['amazonbot', /Amazonbot/i],
  ['seo_tool', /AhrefsBot|SemrushBot|MJ12bot|SerpstatBot|DotBot|BLEXBot|DataForSeoBot/i],
  ['other_bot', /bot\b|crawler|spider|slurp/i]
] as const;

export type AgentLabel = (typeof AGENTS)[number][0] | 'browser';

export function agentLabel(userAgent: string | undefined): AgentLabel {
  if (!userAgent) return 'unknown';
  return AGENTS.find(([, pattern]) => pattern.test(userAgent))?.[0] ?? 'browser';
}

const STATUSES = new Set([200, 301, 302, 304, 400, 403, 404, 410, 429, 500, 502, 503]);
/** Keep a malformed/future route label from turning the private metrics endpoint into a growing cache. */
export const MAX_REQUEST_COUNTER_SERIES = 4096;

export function statusLabel(status: number): string {
  return STATUSES.has(status) ? String(status) : `${Math.floor(status / 100)}xx`;
}

// Route types are shared with the GA4 page_type parameter.
export function routeLabel(path: string): string {
  return path.split('?', 1)[0] === '/llms.txt' ? 'llms' : webRouteType(path);
}

export interface CrawlerIdentity {
  agent: AgentLabel;
  verification: 'verified' | 'ua_only' | 'unknown';
  verificationBasis: 'cf_client_bot' | 'none';
}

export interface BotVerificationConfig {
  /** Deployment opt-in, only after ingress overwrites/strips this header on every path. */
  cfVerifiedBotEnabled?: unknown;
  verifiedBotHeader?: string;
  trustedProxies: string;
}

/** Raw headers preserve duplicate fields which Node's normalized headers may join or discard. */
export function strictBooleanHeader(raw: readonly string[], name: string): boolean | undefined {
  const values: string[] = [];
  for (let index = 0; index < raw.length; index += 2)
    if (raw[index]?.toLowerCase() === name.toLowerCase()) values.push(raw[index + 1] ?? '');
  if (values.length !== 1) return undefined;
  return values[0] === 'true' ? true : values[0] === 'false' ? false : undefined;
}

export function crawlerIdentity(
  userAgent: string | undefined,
  peer: string | undefined,
  rawHeaders: readonly string[],
  config: BotVerificationConfig
): CrawlerIdentity {
  const agent = agentLabel(userAgent);
  const knownGood = Boolean(
    (config.cfVerifiedBotEnabled === true || config.cfVerifiedBotEnabled === 'true') &&
    config.verifiedBotHeader &&
    isTrustedProxy(peer, config.trustedProxies) &&
    strictBooleanHeader(rawHeaders, config.verifiedBotHeader) === true
  );
  return {
    agent,
    verification: knownGood ? 'verified' : agent === 'browser' || agent === 'unknown' ? 'unknown' : 'ua_only',
    verificationBasis: knownGood ? 'cf_client_bot' : 'none'
  };
}

/** Snapshot before Nitro's shared cache filters headers. Nothing here changes access or robots rules. */
export function captureCrawler(event: H3Event, config: BotVerificationConfig): void {
  event.context.crawlerIdentity = crawlerIdentity(
    getHeader(event, 'user-agent'),
    event.node.req.socket.remoteAddress,
    event.node.req.rawHeaders,
    config
  );
}

/** Called only from afterResponse: cached responses and redirects count their final HTTP status. */
export function countResponse(counter: RequestCounter, event: H3Event): void {
  if (!event.context.loadShed && (event.path.startsWith('/__nuxt_island/') || getHeader(event, 'x-nuxt-og-image')))
    return;
  const identity: CrawlerIdentity =
    event.context.crawlerIdentity ??
    crawlerIdentity(getHeader(event, 'user-agent'), undefined, [], { trustedProxies: '' });
  counter.add(identity.agent, routeLabel(event.path), statusLabel(getResponseStatus(event)), identity);
}

export class RequestCounter {
  private readonly counts = new Map<string, number>();
  private overflow = 0;

  add(agent: AgentLabel, route: string, status: string, identity?: CrawlerIdentity) {
    const { verification, verificationBasis } = identity ?? {
      verification: agent === 'browser' || agent === 'unknown' ? 'unknown' : 'ua_only',
      verificationBasis: 'none'
    };
    const key = `${agent}\u0000${route}\u0000${status}\u0000${verification}\u0000${verificationBasis}`;
    if (!this.counts.has(key) && this.counts.size >= MAX_REQUEST_COUNTER_SERIES) {
      this.overflow += 1;
      return;
    }
    this.counts.set(key, (this.counts.get(key) ?? 0) + 1);
  }

  /** Prometheus text exposition format. */
  render(): string {
    const lines = [
      '# HELP opennavo_web_requests_total Requests that reached the web origin, by claimed user-agent class, route type, final status and verification basis.',
      '# TYPE opennavo_web_requests_total counter'
    ];
    for (const [key, value] of [...this.counts].sort(([a], [b]) => a.localeCompare(b))) {
      const [agent, route, status, verification, basis] = key.split('\u0000');
      lines.push(
        `opennavo_web_requests_total{agent="${agent}",route="${route}",status="${status}",verification="${verification}",verification_basis="${basis}"} ${value}`
      );
    }
    if (this.overflow)
      lines.push(
        `opennavo_web_requests_total{agent="unknown",route="overflow",status="other",verification="unknown",verification_basis="none"} ${this.overflow}`
      );
    return `${lines.join('\n')}\n`;
  }
}
