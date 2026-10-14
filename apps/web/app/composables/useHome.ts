import { unwrap } from '@opennavo/api';

/** Homepage data: Discover and rail share a cache key and one SSR request. */
export function useHome() {
  const { locale } = useI18n();
  const api = useApi();
  return useAsyncData(
    () => `home:${locale.value}`,
    () => unwrap(api.GET('/home'))
  );
}

/** Match desktop discovery: enough popular apps for three responsive rows, with home data as fallback. */
export function usePopularApps() {
  const { locale } = useI18n();
  const api = useApi();
  return useAsyncData(
    () => `home:popular:${locale.value}`,
    () =>
      unwrap(
        api.GET('/packages', {
          params: { query: { kind: 'cask', sort: 'popular', size: 30, includeFonts: true } }
        })
      )
        .then(page => page.records)
        .catch(() => null)
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
