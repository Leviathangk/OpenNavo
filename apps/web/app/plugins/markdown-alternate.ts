import { LOCALE_METADATA } from '@opennavo/shared';
import { markdownAlternatesEnabled, markdownTarget } from '~~/shared/utils/markdownAlternate';

// X4: while runtimeConfig.public.markdownAlternates is on, every app overview links its Markdown version
// (server/middleware/markdown-alternate.ts) with rel=alternate type=text/markdown. Registered once for the
// app and following the route, so no page has to opt in.
export default defineNuxtPlugin(() => {
  const config = useRuntimeConfig();
  if (!markdownAlternatesEnabled(config.public.markdownAlternates)) return;
  const siteUrl = config.public.siteUrl.replace(/\/$/, '');
  const route = useRouter().currentRoute;
  const hreflangs = useState<string[] | null>('page:hreflangs', () => null);
  useHead(() => {
    const target = markdownTarget(`${route.value.path}.md`);
    return target && hreflangs.value?.includes(LOCALE_METADATA[target.locale].hreflang)
      ? {
          link: [
            {
              key: 'markdown-alternate',
              rel: 'alternate',
              type: 'text/markdown',
              href: `${siteUrl}${route.value.path}.md`
            }
          ]
        }
      : {};
  });
});
