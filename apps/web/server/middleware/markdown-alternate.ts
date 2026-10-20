import { markdownAlternatesEnabled, markdownTarget } from '../../shared/utils/markdownAlternate';
import { agentLabel } from '../utils/crawlerMetrics';
import { packageMarkdown } from '../utils/packageMarkdown';

// X4 experiment (off by default): <prefix>/apps/{token}.md answers with the overview's facts as Markdown
// (noindex), but only while runtimeConfig.public.markdownAlternates is on; otherwise the path falls through
// to the page route, which has no such app and answers 404. A suffix cannot be a Nitro route pattern, so
// this is middleware. Every fetch is logged with its user agent, and the response is never cached on the
// way (no-cache here, no-store at Cloudflare), so the log counts every fetch: the counts decide whether
// the experiment stays. Uppercase or escaped paths fall through to package-canonical's one 301 first.
export default defineEventHandler(async event => {
  if (event.method !== 'GET' && event.method !== 'HEAD') return;
  // Every request passes here: the path test comes first and costs one regular expression.
  const path = event.path.split('?', 1)[0] ?? '';
  const target = markdownTarget(path);
  if (!target || !markdownAlternatesEnabled(useRuntimeConfig(event).public.markdownAlternates)) return;
  const userAgent = getHeader(event, 'user-agent') ?? '';
  console.info(
    `[x4] markdown fetch ${JSON.stringify({
      agent: agentLabel(userAgent || undefined),
      path,
      locale: target.locale,
      userAgent: userAgent.slice(0, 300)
    })}`
  );
  const body = await packageMarkdown(event, target.locale, target.token);
  // An alias redirect already ended the response; do not try to replace its headers with Markdown headers.
  if (event.handled) return;
  setHeader(event, 'Content-Type', 'text/markdown; charset=utf-8');
  setHeader(event, 'X-Robots-Tag', 'noindex');
  setHeader(event, 'Cache-Control', 'no-cache');
  setHeader(event, 'Cloudflare-CDN-Cache-Control', 'no-store');
  return body;
});
import { defineEventHandler, getHeader, setHeader } from 'h3';
