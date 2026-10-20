// BreadcrumbList structured data built from the breadcrumb the page shows (WP7), so the two
// cannot drift. The last item has no link: it is the current page. The page's WebPage points at it (T4).
export function useBreadcrumbSchema(items: MaybeRefOrGetter<{ label: string; href?: string }[]>) {
  const canonical = useCanonicalUrl();
  const siteUrl = useRuntimeConfig().public.siteUrl.replace(/\/$/, '');
  useSchemaOrg(
    computed(() => [
      defineBreadcrumb({
        '@id': `${canonical.value}#breadcrumb`,
        itemListElement: toValue(items).map(item => ({
          name: item.label,
          ...(item.href ? { item: new URL(item.href, `${siteUrl}/`).href } : {})
        }))
      }),
      defineWebPage({ '@id': `${canonical.value}#webpage`, breadcrumb: { '@id': `${canonical.value}#breadcrumb` } })
    ])
  );
}
