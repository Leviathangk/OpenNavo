import { monitorEventLoopDelay } from 'node:perf_hooks';
import { getHeader, send, setHeaders, setResponseStatus, type H3Event } from 'h3';
import { agentLabel, type AgentLabel } from './crawlerMetrics';
import { isPagePath } from './canonicalUrl';

export const LOAD_SHED_DEFAULTS = {
  enabled: true,
  lowInFlight: 8,
  mediumInFlight: 16,
  extremeInFlight: 64,
  lowLagMs: 50,
  mediumLagMs: 200,
  extremeLagMs: 1000
};
export type LoadShedConfig = typeof LOAD_SHED_DEFAULTS;
export type LoadShedTier = 'low' | 'medium' | 'protected';
export type LoadShedLevel = 'normal' | 'mild' | 'heavy' | 'extreme';

// Exhaustive mapping: new crawler labels require an explicit priority decision.
const TIERS: Record<AgentLabel, LoadShedTier> = {
  browser: 'protected',
  self_test: 'protected',
  googlebot: 'protected',
  bingbot: 'protected',
  bing_preview: 'protected',
  applebot: 'protected',
  duckduckbot: 'protected',
  yandexbot: 'protected',
  baiduspider: 'protected',
  chatgpt_user: 'protected',
  claude_user: 'protected',
  perplexity_user: 'protected',
  mistral_user: 'protected',
  meta_externalfetcher: 'protected',
  duckassistbot: 'protected',
  meta_preview: 'protected',
  link_preview: 'protected',
  perplexitybot: 'medium',
  oai_searchbot: 'medium',
  claude_searchbot: 'medium',
  meta_webindexer: 'medium',
  mistral_index: 'medium',
  google_other: 'medium',
  gptbot: 'low',
  claudebot: 'low',
  ccbot: 'low',
  bytespider: 'low',
  meta_externalagent: 'low',
  mistral_training: 'low',
  amazonbot: 'low',
  seo_tool: 'low',
  other_bot: 'low',
  unknown: 'low'
};

export const loadShedTier = (agent: AgentLabel): LoadShedTier => TIERS[agent];

export function loadShedConfig(input: Record<string, unknown> = {}): LoadShedConfig {
  const config = { ...LOAD_SHED_DEFAULTS };
  config.enabled = input.enabled !== false && input.enabled !== 'false';
  for (const key of [
    'lowInFlight',
    'mediumInFlight',
    'extremeInFlight',
    'lowLagMs',
    'mediumLagMs',
    'extremeLagMs'
  ] as const) {
    const value = Number(input[key]);
    if (Number.isFinite(value) && value >= 1) config[key] = Math.floor(value);
  }
  // A bad override must never make protected traffic shed before lower priorities.
  config.mediumInFlight = Math.max(config.mediumInFlight, config.lowInFlight);
  config.extremeInFlight = Math.max(config.extremeInFlight, config.mediumInFlight);
  config.mediumLagMs = Math.max(config.mediumLagMs, config.lowLagMs);
  config.extremeLagMs = Math.max(config.extremeLagMs, config.mediumLagMs);
  return config;
}

export function loadShedPath(url: string): boolean {
  const path = url.split('?', 1)[0] ?? '/';
  if (
    path.startsWith('/_nuxt/') ||
    path.startsWith('/api/') ||
    path.startsWith('/__sitemap__/') ||
    path.startsWith('/_sitemap/') ||
    /^\/(?:sitemap[^/]*\.xml(?:\.gz)?|robots\.txt|llms\.txt|healthz?|readyz?|livez|metrics)\/?$/.test(path)
  )
    return false;
  if (
    path.startsWith('/__nuxt_island/') ||
    path.startsWith('/_og/') ||
    path.endsWith('/_payload.json') ||
    path.endsWith('.xml')
  )
    return true;
  // Include dotted package tokens and unknown extensionless pages (Nuxt renders their 404 too).
  return isPagePath(path) || !/\.[^/]+$/.test(path) || /\.html?$/.test(path);
}

export function loadShedLevel(inFlight: number, lagMs: number, config: LoadShedConfig): LoadShedLevel {
  if (inFlight >= config.extremeInFlight || lagMs >= config.extremeLagMs) return 'extreme';
  if (inFlight >= config.mediumInFlight || lagMs >= config.mediumLagMs) return 'heavy';
  if (inFlight >= config.lowInFlight || lagMs >= config.lowLagMs) return 'mild';
  return 'normal';
}

export function shouldShed(tier: LoadShedTier, level: LoadShedLevel): boolean {
  return level === 'extreme' || (level === 'heavy' && tier !== 'protected') || (level === 'mild' && tier === 'low');
}

export class LoadShedState {
  inFlight = 0;
  lagMs = 0;
  private readonly counts = new Map<string, number>();
  private stopMonitor?: () => void;

  start() {
    if (this.stopMonitor) return;
    const histogram = monitorEventLoopDelay({ resolution: 20 });
    histogram.enable();
    let lastSample = performance.now();
    const timer = setInterval(() => {
      const now = performance.now();
      // A 500ms mean tolerates isolated 100ms SSR renders at the normal 2–3 requests/s.
      // Timer lateness also catches a stall before the histogram's own timer gets a turn.
      this.lagMs = Math.max(Number.isFinite(histogram.mean) ? histogram.mean / 1e6 : 0, now - lastSample - 500);
      lastSample = now;
      histogram.reset();
    }, 500);
    timer.unref();
    this.stopMonitor = () => {
      clearInterval(timer);
      histogram.disable();
    };
  }

  stop() {
    this.stopMonitor?.();
    this.stopMonitor = undefined;
    this.lagMs = 0;
  }

  track(event: H3Event) {
    const { req, res } = event.node;
    this.inFlight += 1;
    let released = false;
    const release = () => {
      if (released) return;
      released = true;
      this.inFlight -= 1;
      res.removeListener('finish', release);
      res.removeListener('close', release);
      res.removeListener('error', release);
      req.removeListener('aborted', release);
      req.removeListener('error', release);
    };
    res.once('finish', release);
    res.once('close', release);
    res.once('error', release);
    req.once('aborted', release);
    req.once('error', release);
    // Nitro localFetch uses a mock response; afterResponse provides its completion fallback.
    event.context.releaseLoadShed = release;
    if (req.aborted || res.destroyed || res.writableEnded) release();
  }

  reject(agent: AgentLabel, level: Exclude<LoadShedLevel, 'normal'>) {
    const key = `${agent}\u0000${loadShedTier(agent)}\u0000${level}`;
    this.counts.set(key, (this.counts.get(key) ?? 0) + 1);
  }

  render(): string {
    const lines = [
      '# HELP opennavo_web_in_flight Admitted in-flight origin page/render requests.',
      '# TYPE opennavo_web_in_flight gauge',
      `opennavo_web_in_flight ${this.inFlight}`,
      '# HELP opennavo_web_event_loop_delay_seconds Recent event-loop delay used for load shedding.',
      '# TYPE opennavo_web_event_loop_delay_seconds gauge',
      `opennavo_web_event_loop_delay_seconds ${this.lagMs / 1000}`,
      '# HELP opennavo_web_load_shed_total Origin requests refused temporarily to preserve SSR capacity.',
      '# TYPE opennavo_web_load_shed_total counter'
    ];
    for (const [key, value] of [...this.counts].sort(([a], [b]) => a.localeCompare(b))) {
      const [agent, tier, level] = key.split('\u0000');
      lines.push(`opennavo_web_load_shed_total{agent="${agent}",tier="${tier}",level="${level}"} ${value}`);
    }
    return `${lines.join('\n')}\n`;
  }
}

export const loadShedState = new LoadShedState();

export function shedRequest(event: H3Event, config: LoadShedConfig, state = loadShedState): Promise<void> | undefined {
  if (!config.enabled || !loadShedPath(event.path)) return;
  state.start();
  const agent = agentLabel(getHeader(event, 'user-agent'));
  const level = loadShedLevel(state.inFlight, state.lagMs, config);
  if (level !== 'normal' && shouldShed(loadShedTier(agent), level)) {
    state.reject(agent, level);
    event.context.loadShed = true;
    setResponseStatus(event, 503);
    setHeaders(event, {
      'retry-after': 60 + Math.floor(Math.random() * 121),
      'cache-control': 'no-store',
      'cdn-cache-control': 'no-store',
      'cloudflare-cdn-cache-control': 'no-store',
      'content-type': 'text/plain; charset=utf-8'
    });
    // End immediately: H3 still calls afterResponse (metrics), but skips beforeResponse hooks,
    // including the generic error hook that would replace Retry-After with 5 seconds.
    return send(event, 'Service temporarily unavailable. Please retry later.\n');
  }
  state.track(event);
}
