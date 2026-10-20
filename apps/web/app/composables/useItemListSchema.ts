import { listItems } from '~/utils/structuredData';

// CollectionPage and ItemList built from the entries the page lists (WP10), in the same order and
// positions, so structured data cannot name items the page does not show. Not a carousel promise.
// The page is a CollectionPage whose mainEntity is the list (T4); numberOfItems is the whole list.
export function useItemListSchema(
  entries: MaybeRefOrGetter<{ name: string; path: string }[] | null | undefined>,
  start: MaybeRefOrGetter<number> = 1,
  total?: MaybeRefOrGetter<number | null | undefined>,
  /** false when the list is secondary on its page (an author's ProfilePage lists their articles). */
  { asPage = true }: { asPage?: boolean } = {}
) {
  const siteUrl = useRuntimeConfig().public.siteUrl.replace(/\/$/, '');
  const canonical = useCanonicalUrl();
  if (asPage)
    usePageSchema(() => ({
      type: 'CollectionPage',
      mainEntity: toValue(entries)?.length ? `${canonical.value}#list` : undefined
    }));
  useSchemaOrg(
    computed(() => {
      const list = toValue(entries);
      if (!list?.length) return [];
      const count = toValue(total);
      return [
        defineItemList({
          '@id': `${canonical.value}#list`,
          ...(typeof count === 'number' ? { numberOfItems: count } : {}),
          itemListElement: listItems(
            list.map(entry => ({ name: entry.name, url: `${siteUrl}${entry.path}` })),
            toValue(start)
          )
        })
      ];
    })
  );
}
