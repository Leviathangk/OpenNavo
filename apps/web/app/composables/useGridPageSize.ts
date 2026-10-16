import { readGridPageSize, readPage } from '../utils/catalogQuery';

/** Keep page size in links so SSR, reloads, pagination and structured-data positions agree. */
export function useGridPageSize() {
  const { query, hrefWith } = useCatalogQuery({ page: 1, size: 24 });
  const pageSize = computed(() => readGridPageSize(query.value.size));
  function fitPage(size: number) {
    if (size === pageSize.value) return;
    const first = (readPage(query.value.page) - 1) * pageSize.value;
    void navigateTo(hrefWith({ size, page: Math.floor(first / size) + 1 }), { replace: true });
  }
  return { pageSize, fitPage };
}
