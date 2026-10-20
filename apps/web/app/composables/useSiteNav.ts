import type { OnSidebarGroup, OnSidebarItem } from '@opennavo/ui';

/**
 * Web sidebar (05 §11.2): Browse matches desktop; desktop My items (Installed / Updates / History)
 * become Collections / My list (Brewfile) on web. Shared by desktop-width sidebar and mobile menu.
 */
export function useSiteNav() {
  const { t } = useI18n();
  const route = useRoute();
  const localePath = useLocalePath();
  const brewfile = useBrewfileList();

  // App details/search belong under Discover, matching desktop detail navigation. The full app list and
  // the A–Z directory (T1) are All Apps; an app's own pages stay under Discover.
  const section = computed(() => {
    const path = route.path;
    const under = (target: string) => {
      const href = localePath(target);
      return path === href || path.startsWith(`${href}/`);
    };
    if (path === localePath('/apps') || under('/browse')) return 'apps';
    for (const key of [
      'categories',
      'rankings',
      'recent',
      'articles',
      'collections',
      'brewfile',
      'about',
      'feedback'
    ] as const)
      if (under(`/${key}`)) return key;
    // Author pages belong to the articles they list. There is no /authors page to resolve, so the
    // prefix comes from the article list.
    if (path.startsWith(`${localePath('/articles').replace(/\/articles$/, '')}/authors/`)) return 'articles';
    return under('/download') ? 'download' : 'discover';
  });

  const item = (key: string, path: string, icon: string, extra: Partial<OnSidebarItem> = {}): OnSidebarItem => ({
    key,
    label: t(`nav.${key}`),
    icon,
    href: localePath(path),
    active: section.value === key,
    ...extra
  });

  const groups = computed<OnSidebarGroup[]>(() => [
    {
      key: 'browse',
      label: t('nav.browse'),
      items: [
        item('discover', '/discover', 'lucide:compass'),
        item('apps', '/apps', 'lucide:layers'),
        item('categories', '/categories', 'lucide:layout-grid'),
        item('rankings', '/rankings', 'lucide:chart-no-axes-column'),
        item('recent', '/recent', 'lucide:clock'),
        item('articles', '/articles', 'lucide:newspaper')
      ]
    },
    {
      key: 'lists',
      label: t('nav.lists'),
      items: [
        item('collections', '/collections', 'lucide:library'),
        item('brewfile', '/brewfile', 'lucide:file-text', { count: brewfile.count.value || undefined })
      ]
    }
  ]);

  const footer = computed<OnSidebarItem[]>(() => [
    item('about', '/about', 'lucide:info'),
    item('feedback', '/feedback', 'lucide:message-circle')
  ]);

  return { groups, footer };
}
