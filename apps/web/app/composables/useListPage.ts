import { computed, toValue, watch, type MaybeRefOrGetter } from 'vue';
import { pageCount, parsePage } from '../utils/catalogQuery';

function pageNotFound() {
  return createError({ statusCode: 404, statusMessage: 'Page not found', fatal: true });
}

/**
 * Current page of a paginated list (05 §6.1), shared by every list so they answer alike: a ?page=
 * that is not a page number is a 404 before anything loads, and a page past the end is a 404 once
 * the list total is known. Neither renders an empty list that could be indexed; route caches never
 * keep error responses (server/plugins/route-cache-errors.ts drops a page that turns into one).
 *
 * Which parameters canonical URLs keep comes from the route's query contract (shared/utils/routeQuery.ts,
 * S4), not from the list. A list with nothing in it is noindex (S4): there is no page worth indexing.
 */
/**
 * A sort, period, type or filter value outside the known ones is a 404 (S4), like an invalid page number:
 * no URL with a made-up filter renders a list that could be cached or indexed. `allowed` may resolve to
 * null while it is unknown (a decorative list failed to load); the value is then accepted.
 */
export function useKnownQueryValue(name: string, allowed: MaybeRefOrGetter<readonly string[] | null>) {
  const route = useRoute();
  const invalid = () => {
    const value = route.query[name];
    const known = toValue(allowed);
    if (value === undefined || !known) return false;
    return typeof value !== 'string' || !known.includes(value);
  };
  if (invalid()) throw pageNotFound();
  watch(invalid, bad => {
    if (bad) showError(pageNotFound());
  });
}

export function useListPage() {
  const route = useRoute();
  if (parsePage(route.query.page) === undefined) throw pageNotFound();
  // A route update reuses this setup. Surface a fatal Nuxt error rather than silently showing page 1.
  watch(
    () => route.query.page,
    value => {
      if (parsePage(value) === undefined) showError(pageNotFound());
    },
    { flush: 'sync' }
  );
  const page = computed(() => parsePage(route.query.page) ?? 1);
  return {
    page,
    /** Register reactive bounds after loading. Pending/failed requests must not use the previous result's total. */
    assertExists(
      total: MaybeRefOrGetter<number | null | undefined>,
      size: MaybeRefOrGetter<number>,
      status: MaybeRefOrGetter<string>
    ) {
      const outOfRange = () => {
        const count = toValue(total);
        return (
          parsePage(route.query.page) !== undefined &&
          toValue(status) === 'success' &&
          typeof count === 'number' &&
          page.value > pageCount(count, toValue(size))
        );
      };
      if (outOfRange()) throw pageNotFound();
      // An empty list (page 1 of nothing) stays out of the index; tagPriority wins over a page's own robots.
      const empty = computed(() => toValue(status) === 'success' && toValue(total) === 0);
      useHead(() =>
        empty.value ? { meta: [{ name: 'robots', content: 'noindex, follow', tagPriority: 'high' as const }] } : {}
      );
      // Post-flush lets useAsyncData switch its key/status before checking the new route's bounds.
      watch(
        outOfRange,
        invalid => {
          if (invalid) showError(pageNotFound());
        },
        { flush: 'post' }
      );
    }
  };
}
