import type { OnSidebarGroup, OnSidebarItem } from '@opennavo/ui';

/**
 * Web sidebar (05 §11.2): Browse and Lists match desktop. The Mac app group repeats every desktop
 * destination the browser cannot run itself (Installed / Updates / Clean Up / Monitor / Security /
 * Firewall). The first four open explanation pages with a download and handoff; Security and Firewall
 * reuse their landing pages. Shared by the desktop-width sidebar and the mobile menu.
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
    if (under('/developer-map')) return 'developerMap';
    if (path.startsWith(localePath('/security/firewall'))) return 'firewall';
    if (under('/installed')) return 'installed';
    if (under('/updates')) return 'updates';
    if (under('/cleanup')) return 'cleanup';
    if (under('/monitor')) return 'monitor';
    if (under('/webapps')) return 'webapps';
    if (under('/security')) return 'security';
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

  const item = (key: string, path: string, icon: string, extra: Partial<OnSidebarItem> = {}): OnSidebarItem => {
    const { label, ...rest } = extra;
    return {
      key,
      label: label ?? t(`nav.${key}`),
      icon,
      href: localePath(path),
      active: section.value === key,
      ...rest
    };
  };

  // Desktop-only destinations keep their desktop names and get one shared Mac tag, so the group
  // reads as "these pages describe what the app does" before anyone clicks.
  const clientItem = (key: string, path: string, icon: string, label: string): OnSidebarItem =>
    item(key, path, icon, {
      label,
      tag: t('client.tag'),
      tagLabel: t('client.requires')
    });

  const groups = computed<OnSidebarGroup[]>(() => [
    {
      key: 'browse',
      label: t('nav.browse'),
      items: [
        item('discover', '/discover', 'lucide:compass'),
        item('developerMap', '/developer-map', 'globe'),
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
    },
    {
      key: 'mac',
      label: t('nav.clientGroup'),
      hint: t('nav.clientHint'),
      items: [
        clientItem('installed', '/installed', 'lucide:package', t('landing.window.installed')),
        clientItem('updates', '/updates', 'lucide:circle-arrow-down', t('landing.window.updates')),
        clientItem('cleanup', '/cleanup', 'lucide:eraser', t('landing.window.cleanup')),
        clientItem('webapps', '/webapps', 'lucide:app-window', t('landing.window.webapps')),
        clientItem('monitor', '/monitor', 'lucide:activity', t('landing.window.monitor')),
        clientItem('security', '/security', 'lucide:shield-check', t('nav.security')),
        clientItem('firewall', '/security/firewall', 'lucide:brick-wall-fire', t('nav.firewall'))
      ]
    }
  ]);

  const footer = computed<OnSidebarItem[]>(() => [
    item('about', '/about', 'lucide:info'),
    item('feedback', '/feedback', 'lucide:message-circle')
  ]);

  return { groups, footer };
}
