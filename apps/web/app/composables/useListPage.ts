import { computed, toValue, watch, type MaybeRefOrGetter } from 'vue';
import { pageCount, parsePage, type ListQueryParam } from '../utils/catalogQuery';

function pageNotFound() {
  return createError({ statusCode: 404, statusMessage: 'Page not found', fatal: true });
}

/** The paginated list on a route path, and the query parameters its canonical URLs keep. */
export interface ListCanonical {
  path: string;
  query: readonly ListQueryParam[];
}

const LIST_CANONICAL = 'page:list-canonical';

/**
 * Query parameters the current route's canonical and alternate URLs keep (app.vue): a list's own on
 * the route it was set up for, none on any other page. The state names the route path, so a list
 * left behind by client-side navigation never lends its parameters to the next page.
 */
export function useCanonicalQuery() {
  const router = useRouter();
  const list = useState<ListCanonical | null>(LIST_CANONICAL, () => null);
  return computed(() => (list.value?.path === router.currentRoute.value.path ? list.value.query : []));
}

/**
 * Current page of a paginated list (05 §6.1), shared by every list so they answer alike: a ?page=
 * that is not a page number is a 404 before anything loads, and a page past the end is a 404 once
 * the list total is known. Neither renders an empty list that could be indexed; route caches never
 * keep error responses (server/plugins/route-cache-errors.ts drops a page that turns into one).
 *
 * Only these lists keep ?page= in canonical URLs, plus the parameters in `canonical` that change
 * their bounds (grid page size, the disabled-apps filter); every other route drops them.
 */
export function useListPage(options: { canonical?: readonly Exclude<ListQueryParam, 'page'>[] } = {}) {
  const route = useRoute();
  if (parsePage(route.query.page) === undefined) throw pageNotFound();
  const list = useState<ListCanonical | null>(LIST_CANONICAL, () => null);
  const query: readonly ListQueryParam[] = ['page', ...(options.canonical ?? [])];
  watch(
    () => route.path,
    path => {
      list.value = { path, query };
    },
    { immediate: true }
  );
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
