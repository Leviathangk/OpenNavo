// BreadcrumbList structured data built from the breadcrumb the page shows (WP7), so the two
// cannot drift. The last item has no link: it is the current page.
export function useBreadcrumbSchema(items: MaybeRefOrGetter<{ label: string; href?: string }[]>) {
  useSchemaOrg(
    computed(() => [
      defineBreadcrumb({
        itemListElement: toValue(items).map(item => ({ name: item.label, ...(item.href ? { item: item.href } : {}) }))
      })
    ])
  );
}
