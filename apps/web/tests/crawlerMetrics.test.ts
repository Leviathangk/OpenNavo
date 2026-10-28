import { describe, expect, it } from 'vitest';
import { createEvent } from 'h3';
import { IncomingMessage, ServerResponse } from 'node:http';
import { Socket } from 'node:net';
import {
  MAX_REQUEST_COUNTER_SERIES,
  RequestCounter,
  agentLabel,
  routeLabel,
  statusLabel,
  crawlerIdentity,
  strictBooleanHeader,
  captureCrawler,
  countResponse
} from '../server/utils/crawlerMetrics';

/** A made-up user agent in the format Amazon documents for its crawlers. */
const AMAZON = (product: string, link: string) =>
  `Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; ${product}; +${link}) Chrome/120.0.0.0 Safari/537.36`;

describe('crawler request counters', () => {
  it('Separates search, user-requested and training crawlers', () => {
    const cases: [string | undefined, string][] = [
      ['Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)', 'googlebot'],
      ['Mozilla/5.0 (compatible; bingbot/2.0; +http://www.bing.com/bingbot.htm)', 'bingbot'],
      ['Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; OAI-SearchBot/1.0)', 'oai_searchbot'],
      ['Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko); compatible; ChatGPT-User/1.0', 'chatgpt_user'],
      ['Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko); compatible; GPTBot/1.2', 'gptbot'],
      ['Mozilla/5.0 (compatible; Claude-User/1.0; +Claude-User@anthropic.com)', 'claude_user'],
      ['Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; ClaudeBot/1.0)', 'claudebot'],
      ['Claude-SearchBot/1.0', 'claude_searchbot'],
      ['PerplexityBot/1.0', 'perplexitybot'],
      ['Perplexity-User/1.0', 'perplexity_user'],
      ['DuckAssistBot/1.0', 'duckassistbot'],
      ['DuckDuckBot/1.0', 'duckduckbot'],
      ['Meta-WebIndexer/1.0', 'meta_webindexer'],
      ['Meta-ExternalFetcher/1.0', 'meta_externalfetcher'],
      ['Meta-ExternalAgent/1.0', 'meta_externalagent'],
      ['facebookexternalhit/1.1', 'meta_preview'],
      ['MistralAI-Index/1.0', 'mistral_index'],
      ['MistralAI-User/1.0', 'mistral_user'],
      ['MistralAI-Training/1.0', 'mistral_training'],
      // Amazon's documented format: the search and user-triggered fetchers link a page under /amazonbot.
      [AMAZON('Amzn-SearchBot/0.1', 'https://developer.amazon.com/amazonbot'), 'amzn_searchbot'],
      [AMAZON('Amzn-User/0.1', 'https://developer.amazon.com/amazonbot'), 'amzn_user'],
      [AMAZON('Amazonbot/0.1', 'https://developer.amazon.com/support/amazonbot'), 'amazonbot'],
      ['BingPreview/1.0', 'bing_preview'],
      ['msnbot/1.0', 'bingbot'],
      ['Google-InspectionTool/1.0', 'google_other'],
      ['Applebot/0.1', 'applebot'],
      ['Applebot-Extended', 'unknown'],
      ['Google-Extended', 'unknown'],
      ['OpenNavo-Probe/1.0', 'self_test'],
      ['OpenNavo-Probe/1.0 IndexNow', 'indexnow_probe'],
      ['Mozilla/5.0 (compatible; MJ12bot/v1.4.8; http://mj12bot.com/)', 'seo_tool'],
      ['Mozilla/5.0 (compatible; ExampleCrawler/1.0)', 'other_bot'],
      ['Mozilla/5.0 (Macintosh; Intel Mac OS X 14_6) AppleWebKit/605.1.15 Version/18.0 Safari/605.1.15', 'browser'],
      [undefined, 'unknown']
    ];
    for (const [ua, label] of cases) expect(agentLabel(ua), ua).toBe(label);
  });

  it('Uses the shared page types for routes', () => {
    expect(routeLabel('/zh/apps/visual-studio-code/versions')).toBe('app_versions');
  });

  it('Buckets unusual statuses and renders Prometheus text', () => {
    expect(statusLabel(404)).toBe('404');
    expect(statusLabel(418)).toBe('4xx');
    const counter = new RequestCounter();
    counter.add('googlebot', 'app', '200');
    counter.add('googlebot', 'app', '200');
    counter.add('gptbot', 'app', '404');
    const text = counter.render();
    expect(text).toContain('# TYPE opennavo_web_requests_total counter');
    expect(text).toContain(
      'opennavo_web_requests_total{agent="googlebot",route="app",status="200",verification="ua_only",verification_basis="none"} 2'
    );
    expect(text).toContain(
      'opennavo_web_requests_total{agent="gptbot",route="app",status="404",verification="ua_only",verification_basis="none"} 1'
    );
    expect(text.endsWith('\n')).toBe(true);
  });

  it('caps unexpected route cardinality and preserves an overflow count', () => {
    const counter = new RequestCounter();
    for (let index = 0; index < MAX_REQUEST_COUNTER_SERIES + 100; index += 1)
      counter.add('browser', `untrusted-route-${index}`, '200');
    const series = counter
      .render()
      .split('\n')
      .filter(line => line.startsWith('opennavo_web_requests_total{'));
    expect(series).toHaveLength(MAX_REQUEST_COUNTER_SERIES + 1);
    expect(counter.render()).toContain('route="overflow"');
  });
});

describe('page cache invalidation', () => {
  it('Matches pages, descendants and payloads, allowing harmless prefix collisions', async () => {
    const { pageCacheMatcher, authorized } = await import('../server/utils/pageCache');
    const matches = pageCacheMatcher(['/apps/vlc', '/zh/apps/visual-studio-code']);
    expect(matches('nitro:routes:_:appsvlc.abc123.json')).toBe(true);
    expect(matches('appsvlc.def456.json')).toBe(true);
    expect(matches('nitro:routes:_:appsvlcplayer.abc123.json')).toBe(true);
    for (const path of ['/apps/vlc/versions', '/apps/vlc/_payload.json', '/apps/vlc/versions/_payload.json']) {
      const key = path.replace(/\W/g, '').slice(0, 16);
      expect(matches(`nitro:routes:_:${key}.hash.json`), path).toBe(true);
    }
    expect(matches('nitro:routes:_:zhappsvisualstud.x1.json')).toBe(true);
    expect(matches('nitro:routes:_:appsother.x1.json')).toBe(false);
    expect(authorized('Bearer secret-value', 'secret-value')).toBe(true);
    expect(authorized('Bearer wrong', 'secret-value')).toBe(false);
    expect(authorized(undefined, 'secret-value')).toBe(false);
    expect(authorized('Bearer ', '')).toBe(false);
  });
});

describe('G1 trusted ingress diagnostics', () => {
  const header = 'X-OpenNavo-Verified-Bot';
  const config = { cfVerifiedBotEnabled: true as unknown, verifiedBotHeader: header, trustedProxies: '192.0.2.10' };
  const classify = (raw: string[], peer = '192.0.2.10', options = config, ua: string | undefined = 'Googlebot/2.1') =>
    crawlerIdentity(ua, peer, raw, options);
  it('is disabled by default and never trusts a client address header to establish a peer', () => {
    for (const cfVerifiedBotEnabled of [undefined, false, 'false', 'TRUE', '1', 1, ' true'])
      expect(classify([header, 'true'], '192.0.2.10', { ...config, cfVerifiedBotEnabled }).verification).toBe(
        'ua_only'
      );
    expect(classify([header, 'true'], '192.0.2.10', { ...config, cfVerifiedBotEnabled: 'true' }).verification).toBe(
      'verified'
    );
    expect(classify([header, 'true'], '192.0.2.10', { ...config, verifiedBotHeader: '' })).toMatchObject({
      verification: 'ua_only',
      verificationBasis: 'none'
    });
    for (const peer of ['198.51.100.1', '', 'invalid'])
      expect(
        classify([header, 'true', 'CF-Connecting-IP', '192.0.2.10', 'X-Forwarded-For', '192.0.2.10'], peer)
      ).toMatchObject({ verification: 'ua_only', verificationBasis: 'none' });
    expect(classify([header, 'true'], '192.0.2.10', { ...config, trustedProxies: '' }).verification).toBe('ua_only');
  });
  it('accepts a single sanitized CF true only on the opted-in ingress', () => {
    expect(classify([header, 'true'], '::ffff:192.0.2.10')).toEqual({
      agent: 'googlebot',
      verification: 'verified',
      verificationBasis: 'cf_client_bot'
    });
    // Attestation is known-good-bot only: even an unknown UA stays unknown as a claimed family.
    expect(crawlerIdentity(undefined, '192.0.2.10', [header, 'true'], config)).toEqual({
      agent: 'unknown',
      verification: 'verified',
      verificationBasis: 'cf_client_bot'
    });
    expect(classify([header, 'false']).verification).toBe('ua_only');
    expect(crawlerIdentity('Mozilla/5.0', '192.0.2.10', [header, 'false'], config)).toMatchObject({
      agent: 'browser',
      verification: 'unknown',
      verificationBasis: 'none'
    });
  });
  it('never promotes missing, malformed or duplicate headers, including equal duplicates', () => {
    for (const raw of [
      [],
      [header, 'TRUE'],
      [header, '1'],
      [header, ' true'],
      [header, 'true '],
      [header, 'true,false'],
      [header, 'true, true'],
      [header, 'true', header.toLowerCase(), 'true'],
      [header, 'false', header, 'true']
    ]) {
      expect(strictBooleanHeader(raw, header), JSON.stringify(raw)).toBeUndefined();
      expect(classify(raw).verification).toBe('ua_only');
    }
    // Direct-origin requests forwarded by Caddy have the client value stripped, even though the hop is trusted.
    expect(classify(['CF-Connecting-IP', '203.0.113.1', 'X-Forwarded-For', '203.0.113.1']).verification).toBe(
      'ua_only'
    );
  });
  it('uses captured per-request identity and the final status after a cached response', () => {
    const counter = new RequestCounter();
    for (const status of [200, 301, 304, 403, 404, 410, 429, 503]) {
      const socket = new Socket();
      Object.defineProperty(socket, 'remoteAddress', { value: '192.0.2.10' });
      const request = new IncomingMessage(socket);
      request.url = '/apps/example?tracking=not-a-label';
      request.headers = { 'user-agent': 'Googlebot/2.1' };
      request.rawHeaders = ['User-Agent', 'Googlebot/2.1', header, 'true'];
      const response = new ServerResponse(request);
      const event = createEvent(request, response);
      captureCrawler(event, config);
      // Nitro may filter headers and initially have a 200 before delivering a cached redirect/error.
      request.headers = {};
      request.rawHeaders = [];
      event.context.cache = { hit: true };
      response.statusCode = status;
      countResponse(counter, event);
      expect(counter.render()).toContain(
        `agent="googlebot",route="app",status="${status}",verification="verified",verification_basis="cf_client_bot"} 1`
      );
      socket.destroy();
    }
    expect(counter.render()).not.toMatch(/192\.0|example|tracking|User-Agent/);
  });
});
