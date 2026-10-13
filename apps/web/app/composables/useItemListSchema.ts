import { listItems } from '~/utils/structuredData';

// CollectionPage and ItemList built from the entries the page lists (WP10), in the same order and
// positions, so structured data cannot name items the page does not show. Not a carousel promise.
export function useItemListSchema(
  entries: MaybeRefOrGetter<{ name: string; path: string }[] | null | undefined>,
  start: MaybeRefOrGetter<number> = 1
) {
  const siteUrl = useRuntimeConfig().public.siteUrl.replace(/\/$/, '');
  useSchemaOrg(
    computed(() => {
      const list = toValue(entries);
      if (!list?.length) return [];
      return [
        defineWebPage({ '@type': 'CollectionPage' }),
        defineItemList({
          itemListElement: listItems(
            list.map(entry => ({ name: entry.name, url: `${siteUrl}${entry.path}` })),
            toValue(start)
          )
        })
      ];
    })
  );
}
