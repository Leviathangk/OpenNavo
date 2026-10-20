import { canonicalPageUrl } from '~/utils/catalogQuery';
import { canonicalPath } from '~/utils/canonicalPath';

/**
 * The page's canonical URL, from the one place app.vue (link rel=canonical, og:url) and the structured
 * data (@id of WebPage and its parts) both read, so they cannot drift (T4). A page may name another path
 * as its canonical (page:canonicalPath, from the server's decision); otherwise it is the page itself with
 * only the query its route contract keeps (S4). The site root is the bare origin, like its hreflang.
 */
export function useCanonicalUrl() {
  const route = useRoute();
  const siteUrl = useRuntimeConfig().public.siteUrl.replace(/\/$/, '');
  const decided = useState<string | null>('page:canonicalPath', () => null);
  return computed(() => {
    if (decided.value) return new URL(decided.value, `${siteUrl}/`).href;
    const [path = '/', query] = (route.fullPath.split('#', 1)[0] ?? '/').split('?');
    const clean = canonicalPath(path);
    return canonicalPageUrl(`${siteUrl}${clean === '/' ? '' : clean}${query ? `?${query}` : ''}`);
  });
}
