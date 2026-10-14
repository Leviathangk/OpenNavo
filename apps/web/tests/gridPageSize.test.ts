import { afterEach, describe, expect, it, vi } from 'vitest';
import { computed, ref } from 'vue';
import { useGridPageSize } from '../app/composables/useGridPageSize';

afterEach(() => vi.unstubAllGlobals());

describe('Responsive page size', () => {
  it('Preserves the previously visible first app when resizing and writes a reloadable URL', () => {
    const query = ref({ page: '3', size: '24' });
    const navigate = vi.fn();
    vi.stubGlobal('computed', computed);
    vi.stubGlobal('useCatalogQuery', () => ({
      query,
      hrefWith: (patch: Record<string, number>) => `/recent?size=${patch.size}&page=${patch.page}`
    }));
    vi.stubGlobal('navigateTo', navigate);
    const { pageSize, fitPage } = useGridPageSize();
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
});
