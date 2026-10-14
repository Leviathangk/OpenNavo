// Footer link groups shared by the landing and store footers (WP7), so every page links the core
// entry points: browsing lists plus download, Brewfile, about, feedback and the source repository.
export function useFooterLinks() {
  const { t } = useI18n();
  const localePath = useLocalePath();
  const links = <K extends string>(keys: readonly K[]) =>
    keys.map(key => ({ key, label: t(`nav.${key}`), href: localePath(`/${key}`), external: false }));
  return computed(() => [
    {
      key: 'browse',
      title: t('nav.browse'),
      links: links(['discover', 'categories', 'rankings', 'recent', 'articles', 'collections'])
    },
    {
      key: 'site',
      title: t('site.name'),
      // GitHub is a brand name in every locale, so it has no message key.
      links: [
        ...links(['download', 'brewfile', 'about', 'feedback']),
        { key: 'github', label: 'GitHub', href: 'https://github.com/opennavo/OpenNavo', external: true }
      ]
    }
  ]);
}
