import { getResponseHeader, type H3Event } from 'h3';
import { markdownPageTarget, supportsMarkdownNegotiation } from '../../shared/utils/markdownAlternate';

function appendHeaderValue(event: H3Event, name: string, value: string) {
  const previous = getResponseHeader(event, name);
  const values = (Array.isArray(previous) ? previous : previous ? [previous] : []).map(String);
  if (!values.some(item => item.toLowerCase() === value.toLowerCase())) values.push(value);
  setHeader(event, name, values.join(', '));
}

// Apply on every response, including Nitro SWR cache hits. HTML only: assets and
// API clients do not need a browsing-context policy. Keep OAuth/payment popups usable.
export default defineNitroPlugin(nitroApp => {
  nitroApp.hooks.hook('beforeResponse', event => {
    const contentType = String(event.node.res.getHeader('content-type') ?? '');
    if (!contentType.includes('text/html')) return;
    setHeader(event, 'Cross-Origin-Opener-Policy', 'same-origin-allow-popups');
    appendHeaderValue(event, 'Link', '</llms.txt>; rel="describedby"');

    const path = String(event.path ?? '').split('?', 1)[0] ?? '';
    const target = markdownPageTarget(path);
    if (!supportsMarkdownNegotiation(path)) return;
    appendHeaderValue(event, 'Vary', 'Accept');
    if (target?.kind === 'app') {
      const prefix = target.locale === 'en-US' ? '' : path.slice(0, path.indexOf('/apps/'));
      appendHeaderValue(event, 'Link', `<${prefix}/apps/${target.token}.md>; rel="alternate"; type="text/markdown"`);
    }
  });
});
