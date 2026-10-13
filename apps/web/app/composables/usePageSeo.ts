import { pagedTitle } from '~/utils/seoTitles';

// Central page metadata (05 §6.1); M2-08 adds canonical, structured data, share images here.
export function usePageSeo(options: {
  title: MaybeRefOrGetter<string>;
  description?: MaybeRefOrGetter<string | undefined>;
  /** Current page of a paginated list; page 2 and later get the page number in the title. */
  page?: MaybeRefOrGetter<number | undefined>;
  /** Search and Brewfile pages: noindex, follow. */
  noindex?: boolean;
}) {
  const { t } = useI18n();
  useSeoMeta({
    title: () => {
      const page = toValue(options.page);
      return pagedTitle(toValue(options.title), page, t('catalog.pageTitle', { page }));
    },
    description: () => toValue(options.description),
    // Only set robots when asked: an empty value would replace a noindex set by the page shell.
    ...(options.noindex ? { robots: 'noindex, follow' } : {})
  });
}
