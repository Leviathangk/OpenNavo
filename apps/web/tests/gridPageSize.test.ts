import { afterEach, describe, expect, it, vi } from 'vitest';
import { computed, ref } from 'vue';
import { useGridPageSize } from '../app/composables/useGridPageSize';

afterEach(() => vi.unstubAllGlobals());

function setup(initial: Record<string, string>) {
  const query = ref(initial);
  const navigate = vi.fn();
  vi.stubGlobal('computed', computed);
  vi.stubGlobal('useCatalogQuery', () => ({
    query,
    hrefWith: (patch: Record<string, number>) => `/recent?size=${patch.size}&page=${patch.page}`
  }));
  vi.stubGlobal('navigateTo', navigate);
  return { query, navigate, ...useGridPageSize() };
}

describe('Responsive page size', () => {
  it('Preserves the previously visible first app when resizing and writes a reloadable URL', () => {
    const { query, navigate, pageSize, fitPage } = setup({ page: '3', size: '24' });
    expect(pageSize.value).toBe(24);
    fitPage(28);
    expect(navigate).toHaveBeenCalledWith('/recent?size=28&page=2', { replace: true });
    query.value = { page: '2', size: '28' };
    expect(pageSize.value).toBe(28);
    fitPage(28);
    expect(navigate).toHaveBeenCalledTimes(1);
    query.value = { page: '2', size: '999999' };
    expect(pageSize.value).toBe(24);
  });

  it('Flags other page sizes, which lists keep out of the index; the default size is the list itself', () => {
    const { query, customSize } = setup({ page: '2' });
    expect(customSize.value).toBe(false);
    for (const size of ['12', '28', '100', '012']) {
      query.value = { page: '2', size };
      expect(customSize.value, size).toBe(true);
    }
    for (const size of ['24', '0', '101', 'abc']) {
      query.value = { page: '2', size };
      expect(customSize.value, size).toBe(false);
    }
  });
});
