import { computed, toValue, type MaybeRefOrGetter } from 'vue';

export interface ReleaseFeedTarget {
  section: 'apps' | 'collections';
  /** App token or collection slug. */
  key: string;
  /** The feed's title, as its channel names it (feeds.appTitle, feeds.collectionTitle). */
  title: string;
}

/**
 * Declares an app's or a collection's update feed (F6: server/handlers/packageFeed.ts, collectionFeed.ts) with
 * a link tag in this language, and returns its path for a visible subscribe link. The target is null until
 * the page knows what it shows.
 */
export function useReleaseFeed(target: MaybeRefOrGetter<ReleaseFeedTarget | null | undefined>) {
  const localePath = useLocalePath();
  const siteUrl = useRuntimeConfig().public.siteUrl.replace(/\/$/, '');
  const path = computed(() => {
    const value = toValue(target);
    return value ? `${localePath(`/${value.section}/${value.key}`)}/feed.xml` : null;
  });
  useHead(() => {
    const value = toValue(target);
    return value && path.value
      ? {
          link: [
            {
              key: 'release-feed',
              rel: 'alternate',
              type: 'application/rss+xml',
              title: value.title,
              href: `${siteUrl}${path.value}`
            }
          ]
        }
      : {};
  });
  return path;
}
