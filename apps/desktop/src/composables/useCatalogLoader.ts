import type { WatchSource } from 'vue';
import { useCatalogStore } from '@/stores/catalog';
import { useLinksStore } from '@/stores/links';
import { useNamesStore } from '@/stores/names';
import type { LocalItem } from '@/ipc/client';
import { useLoader } from './useLoader';

/**
 * Reread the local catalog after sync, even when language-pack updates leave the catalog cursor unchanged.
 * Changes to identitySources (all sources by default) clear the shown data; pass [] to keep it until the next read lands.
 */
export function useCatalogLoader<T>(
  fetcher: () => Promise<T>,
  sources: WatchSource[],
  itemsOf: (value: T) => readonly LocalItem[],
  identitySources: WatchSource[] = sources
) {
  const catalog = useCatalogStore();
  const links = useLinksStore();
  const names = useNamesStore();
  return useLoader(
    fetcher,
    [...sources, () => catalog.revision],
    value => {
      // Shared caches must obey the same query, catalog revision and scope lifetime as the shown results.
      const items = itemsOf(value);
      names.remember(items);
      links.remember(items);
    },
    identitySources
  );
}
