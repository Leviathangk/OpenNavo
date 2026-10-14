import { readPage } from '../utils/catalogQuery';

/** Keep page size in links so SSR, reloads, pagination and structured-data positions agree. */
export function useGridPageSize() {
  const { query, hrefWith } = useCatalogQuery({ page: 1, size: 24 });
  const pageSize = computed(() => {
    const size = Number(query.value.size);
    return Number.isInteger(size) && size >= 1 && size <= 100 ? size : 24;
  });
  function fitPage(size: number) {
    if (size === pageSize.value) return;
    const first = (readPage(query.value.page) - 1) * pageSize.value;
    void navigateTo(hrefWith({ size, page: Math.floor(first / size) + 1 }), { replace: true });
  }
  return { pageSize, fitPage };
}
