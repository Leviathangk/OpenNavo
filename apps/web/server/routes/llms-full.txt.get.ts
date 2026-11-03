import { createHash } from 'node:crypto';
import { defineEventHandler, getRequestHeader, setHeaders, setResponseStatus } from 'h3';
import {
  EMPTY_LLMS_FULL_DATA,
  renderLlmsFull,
  type LlmsFullApp,
  type LlmsFullArticle,
  type LlmsFullCategory,
  type LlmsFullCollection,
  type LlmsFullData
} from '../utils/llmsFull';
import { publicApi } from '../utils/publicApi';
import { siteMessage } from '../utils/siteCopy';

interface Page<T> {
  total: number;
  records: T[];
}

const CACHE_TTL = 6 * 60 * 60 * 1000;
let cached: { key: string; at: number; ttl: number; body: string } | undefined;

/** Tests call this between runtime stubs; production keeps one snapshot per API origin. */
export function resetLlmsFullCache() {
  cached = undefined;
}

async function loadCatalog(event: Parameters<typeof publicApi>[0]): Promise<LlmsFullData> {
  const api = publicApi(event);
  // Required counts and the app index reject on outage so the caller can keep the last snapshot;
  // optional sections degrade to empty rather than hiding the whole catalog.
  const [casks, links, categories, collections, articles, apps] = await Promise.all([
    api.get<Page<LlmsFullApp>>('/packages', { kind: 'cask', size: 1, locale: 'en-US' }),
    api.get<Page<LlmsFullApp>>('/packages', { kind: 'link', size: 1, locale: 'en-US' }),
    api.get<LlmsFullCategory[]>('/categories', { locale: 'en-US' }).catch(() => []),
    api
      .get<Page<LlmsFullCollection>>('/collections', { size: 100, locale: 'en-US' })
      .catch(() => ({ total: 0, records: [] as LlmsFullCollection[] })),
    api
      .get<Page<LlmsFullArticle>>('/articles', { size: 20, locale: 'en-US' })
      .catch(() => ({ total: 0, records: [] as LlmsFullArticle[] })),
    api.get<Page<LlmsFullApp>>('/packages', { size: 100, sort: 'popular', locale: 'en-US' })
  ]);
  return {
    total: casks.total + links.total,
    linkTotal: links.total,
    categories,
    collections: collections.records,
    articles: articles.records,
    apps: apps.records,
    generatedAt: new Date().toISOString()
  };
}

export default defineEventHandler(async event => {
  const config = useRuntimeConfig(event);
  const enabled: unknown = config.llmsEnabled;
  if (enabled !== true && enabled !== 'true') {
    setHeaders(event, { 'Content-Type': 'text/plain; charset=utf-8', 'Cache-Control': 'no-store' });
    setResponseStatus(event, 404);
    return siteMessage('en-US', 'error.notFoundTitle');
  }
  const key = `${config.public.siteUrl}|${config.apiBaseInternal || config.public.apiBase}`;
  const now = Date.now();
  let document: string | undefined;
  let generatedAt = now;
  if (cached?.key === key && now - cached.at < cached.ttl) {
    document = cached.body;
    generatedAt = cached.at;
  } else {
    let data = EMPTY_LLMS_FULL_DATA;
    try {
      data = await loadCatalog(event);
    } catch {
      // A catalog outage keeps the last good snapshot; without one, the stable site sections still answer.
      if (cached?.key === key) {
        document = cached.body;
        generatedAt = cached.at;
      }
    }
    if (document === undefined) {
      document = renderLlmsFull(config.public.siteUrl, config.public.apiBase, data);
      generatedAt = now;
      // A cold-start or outage fallback retries within a minute, so recovery does not wait six hours.
      cached = { key, at: now, ttl: data.total > 0 ? CACHE_TTL : 60_000, body: document };
    }
  }
  const body = document;
  const etag = `W/"${createHash('sha1').update(body).digest('base64url')}"`;
  setHeaders(event, {
    'Content-Type': 'text/plain; charset=utf-8',
    'Cache-Control': 'public, max-age=300, s-maxage=3600',
    ETag: etag,
    'Last-Modified': new Date(generatedAt).toUTCString(),
    'X-Robots-Tag': 'noindex, follow'
  });
  if (getRequestHeader(event, 'if-none-match') === etag) {
    setResponseStatus(event, 304);
    return '';
  }
  return body;
});
