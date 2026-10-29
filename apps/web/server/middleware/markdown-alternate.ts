import {
  markdownAlternatesEnabled,
  markdownPageTarget,
  markdownTarget,
  prefersMarkdown,
  supportsMarkdownNegotiation
} from '../../shared/utils/markdownAlternate';
import { brandDownloadPath } from '../../app/utils/packageView';
import { agentLabel } from '../utils/crawlerMetrics';
import { renderHomeMarkdown } from '../utils/homeMarkdown';
import { packageMarkdown } from '../utils/packageMarkdown';

// X4: <prefix>/apps/{token}.md answers with the overview's facts as Markdown, and the home page can
// negotiate the same representation through Accept. A suffix cannot be a Nitro route pattern, so this is
// middleware. Every fetch is logged with its user agent, and Markdown is never cached on the way. Uppercase
// or escaped paths fall through to package-canonical's one 301 first.
export default defineEventHandler(async event => {
  if (event.method !== 'GET' && event.method !== 'HEAD') return;
  // Every request passes here: the path test comes first and costs only a couple of regular expressions.
  const path = event.path.split('?', 1)[0] ?? '';
  const explicit = markdownTarget(path);
  const page = !explicit ? markdownPageTarget(path) : undefined;
  // The site's own catalog token is a canonical redirect to /download, so leave it to package-canonical.
  if (page?.kind === 'app' && brandDownloadPath(path)) return;
  const negotiated = !explicit && prefersMarkdown(getHeader(event, 'accept')) ? page : undefined;
  const target = explicit ? { kind: 'app' as const, ...explicit } : negotiated;
  if (supportsMarkdownNegotiation(path)) setHeader(event, 'Vary', 'Accept');
  if (!target || !markdownAlternatesEnabled(useRuntimeConfig(event).public.markdownAlternates)) return;
  const userAgent = getHeader(event, 'user-agent') ?? '';
  console.info(
    `[x4] markdown fetch ${JSON.stringify({
      agent: agentLabel(userAgent || undefined),
      path,
      locale: target.locale,
      kind: target.kind,
      userAgent: userAgent.slice(0, 300)
    })}`
  );
  const body =
    target.kind === 'home'
      ? renderHomeMarkdown(useRuntimeConfig(event).public.siteUrl, target.locale)
      : await packageMarkdown(event, target.locale, target.token);
  // An alias redirect already ended the response; do not try to replace its headers with Markdown headers.
  if (event.handled) return;
  setHeader(event, 'Content-Type', 'text/markdown; charset=utf-8');
  setHeader(event, 'X-Robots-Tag', 'noindex');
  setHeader(event, 'Cache-Control', 'no-store');
  setHeader(event, 'Cloudflare-CDN-Cache-Control', 'no-store');
  return body;
});
import { defineEventHandler, getHeader, setHeader } from 'h3';
