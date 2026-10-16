import { pagedDescription, pagedTitle } from '~/utils/seoTitles';

// Central page metadata (05 §6.1); M2-08 adds canonical, structured data, share images here.
export function usePageSeo(options: {
  title: MaybeRefOrGetter<string>;
  description?: MaybeRefOrGetter<string | undefined>;
  /** Current page of a paginated list; page 2 and later get the page number in title and description. */
  page?: MaybeRefOrGetter<number | undefined>;
  /** Search and Brewfile pages: noindex, follow. */
  noindex?: boolean;
}) {
  const { t } = useI18n();
  const pageLabel = () => {
    const page = toValue(options.page);
    return { page, label: t('catalog.pageTitle', { page }) };
  };
  useSeoMeta({
    title: () => {
      const { page, label } = pageLabel();
      return pagedTitle(toValue(options.title), page, label);
    },
    description: () => {
      const { page, label } = pageLabel();
      return pagedDescription(toValue(options.description), page, label);
    },
    // Only set robots when asked: an empty value would replace a noindex set by the page shell.
    ...(options.noindex ? { robots: 'noindex, follow' } : {})
  });
}
