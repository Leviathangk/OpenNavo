import { GRID_PAGE_SIZE, readGridPageSize, readPage } from '../utils/catalogQuery';

/**
 * Keep page size in links so SSR, reloads, pagination and structured-data positions agree. A list
 * at another size (wide screens write one into the URL) splits the same items into other pages: its
 * canonical keeps the size, because without it the page number can lie past the end, and the page
 * stays out of the index (customSize; lists fold it into their one robots rule), so only the default
 * size is indexed.
 */
export function useGridPageSize() {
  const { query, hrefWith } = useCatalogQuery({ page: 1, size: GRID_PAGE_SIZE });
  const pageSize = computed(() => readGridPageSize(query.value.size));
  const customSize = computed(() => pageSize.value !== GRID_PAGE_SIZE);
  function fitPage(size: number) {
    if (size === pageSize.value) return;
    const first = (readPage(query.value.page) - 1) * pageSize.value;
    void navigateTo(hrefWith({ size, page: Math.floor(first / size) + 1 }), { replace: true });
  }
  return { pageSize, customSize, fitPage };
}
