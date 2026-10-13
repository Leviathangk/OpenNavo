import { describe, expect, it } from 'vitest';
import { RequestCounter, agentLabel, routeLabel, statusLabel } from '../server/utils/crawlerMetrics';

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
    expect(text).toContain('opennavo_web_requests_total{agent="googlebot",route="app",status="200"} 2');
    expect(text).toContain('opennavo_web_requests_total{agent="gptbot",route="app",status="404"} 1');
    expect(text.endsWith('\n')).toBe(true);
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
