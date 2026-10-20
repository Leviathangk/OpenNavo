import { unwrap } from '@opennavo/api';

/**
 * Apps and collections per Discover group (T1): the server renders exactly this many in every group, and
 * the API returns no more, so the HTML and the payload hold the same set on every screen.
 */
export const HOME_SECTION_SIZE = 12;

/** Homepage data: Discover and rail share a cache key and one SSR request. */
export function useHome() {
  const { locale } = useI18n();
  const api = useApi();
  return useAsyncData(
    () => `home:${locale.value}`,
    () => unwrap(api.GET('/home', { params: { query: { sectionSize: HOME_SECTION_SIZE } } }))
  );
}

/** Weekly popular apps (top four over 30 days) are supplementary: null on failure hides the section without affecting the homepage. */
export function useWeeklyTop() {
  const { locale } = useI18n();
  const api = useApi();
  return useAsyncData(
    () => `home:weekly:${locale.value}`,
    () =>
      unwrap(
        api.GET('/rankings', { params: { query: { kind: 'cask', includeFonts: true, period: '30d', size: 4 } } })
      ).catch(() => null)
  );
}
