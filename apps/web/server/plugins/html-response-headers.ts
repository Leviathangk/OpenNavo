import { LOCALES } from '@opennavo/shared';
import { getQuery, setHeader } from 'h3';
import {
  markdownAlternatesEnabled,
  markdownPageTarget,
  supportsMarkdownNegotiation
} from '../../shared/utils/markdownAlternate';
import { appendHeaderValue } from '../utils/responseHeaders';
import { getSiteSettings } from '../utils/siteSettings';

// Apply on every response, including Nitro SWR cache hits. HTML only: assets and
// API clients do not need a browsing-context policy. Keep OAuth/payment popups usable.
export default defineNitroPlugin(nitroApp => {
  nitroApp.hooks.hook('beforeResponse', async event => {
    const contentType = String(event.node.res.getHeader('content-type') ?? '');
    if (!contentType.includes('text/html')) return;
    setHeader(event, 'Cross-Origin-Opener-Policy', 'same-origin-allow-popups');
    // Nuxt renders errors through an internal request whose node status can still be 200.
    // Its query carries the final status that is copied back to the original response.
    const status = event.path.startsWith('/__nuxt_error')
      ? Number(getQuery(event).status ?? getQuery(event).statusCode ?? 500)
      : event.node.res.statusCode;
    if (status !== 200) return;
    appendHeaderValue(event, 'Link', '</llms.txt>; rel="describedby"');
    if (!markdownAlternatesEnabled(useRuntimeConfig(event).public.markdownAlternates)) return;

    const path = String(event.path ?? '').split('?', 1)[0] ?? '';
    const target = markdownPageTarget(path);
    if (!target || !supportsMarkdownNegotiation(path)) return;
    const settings =
      (event.context.siteSettings as Awaited<ReturnType<typeof getSiteSettings>> | undefined) ??
      (await getSiteSettings());
    if (!(settings.pages.openLocales ?? LOCALES).includes(target.locale)) return;
    appendHeaderValue(event, 'Vary', 'Accept');
    if (target?.kind === 'app') {
      const prefix = target.locale === 'en-US' ? '' : path.slice(0, path.indexOf('/apps/'));
      appendHeaderValue(event, 'Link', `<${prefix}/apps/${target.token}.md>; rel="alternate"; type="text/markdown"`);
    }
  });
});
