/**
 * Declares this language's article feed (WP17) with a link tag, and returns its path for the visible
 * subscribe link. Every language has its own feed next to its article list.
 */
export function useArticleFeed() {
  const { t } = useI18n();
  const localePath = useLocalePath();
  const siteUrl = useRuntimeConfig().public.siteUrl.replace(/\/$/, '');
  const path = computed(() => `${localePath('/articles').replace(/\/$/, '')}/feed.xml`);
  useHead(() => ({
    link: [
      { rel: 'alternate', type: 'application/rss+xml', title: t('articles.feedTitle'), href: `${siteUrl}${path.value}` }
    ]
  }));
  return path;
}
