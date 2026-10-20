import type { PublicComponents } from '@opennavo/api';
import type { OnCommandGroup, OnCommandItem } from '@opennavo/ui';

export type SuggestItem = PublicComponents['schemas']['SuggestItem'];

type Translate = (key: string, params?: Record<string, unknown>) => string;

/** The pages Command-K can jump to, by their i18n key under `palette.pages`. */
export const PALETTE_PAGES = [
  { key: 'discover', path: '/discover', icon: 'compass' },
  { key: 'categories', path: '/categories', icon: 'layout-grid' },
  { key: 'rankings', path: '/rankings', icon: 'chart-no-axes-column' },
  { key: 'recent', path: '/recent', icon: 'clock' },
  { key: 'collections', path: '/collections', icon: 'boxes' },
  { key: 'download', path: '/download', icon: 'download' },
  { key: 'brewfile', path: '/brewfile', icon: 'file-text' }
] as const;

/**
 * The rows of the web Command-K for what was typed (05 §7). The "Go to" group comes first and starts with all the results
 * for the text, so Enter goes to them; the suggested apps follow. Without text only the pages are listed.
 */
export function paletteGroups(query: string, suggestions: readonly SuggestItem[], t: Translate): OnCommandGroup[] {
  const q = query.trim();
  const lower = q.toLowerCase();
  const packages: OnCommandItem[] = suggestions.flatMap(item =>
    // Casks and link apps share /apps (store/security plan §4); command-line tools have no web page.
    item.type === 'package' && (item.kind === 'cask' || item.kind === 'link') && item.token
      ? [
          {
            key: `package:${item.kind}:${item.token}`,
            label: item.name,
            description: item.summary ?? undefined,
            app: { kind: item.kind, token: item.token, name: item.name, src: item.iconUrl }
          }
        ]
      : []
  );
  const jumps: OnCommandItem[] = [
    ...(q ? [{ key: `search:${q}`, label: t('palette.searchAll', { q }), icon: 'search' }] : []),
    ...suggestions
      .filter(item => item.type === 'category' && item.slug)
      .map(item => ({
        key: `category:${item.slug}`,
        label: t('palette.category', { name: item.name }),
        icon: 'layout-grid'
      })),
    // Match page names or English keys; rank can find Rankings even in the Chinese UI.
    ...PALETTE_PAGES.filter(
      page => !lower || page.key.includes(lower) || t(`palette.pages.${page.key}`).toLowerCase().includes(lower)
    ).map(page => ({ key: `page:${page.path}`, label: t(`palette.pages.${page.key}`), icon: page.icon }))
  ];
  return [
    { key: 'jump', label: t('palette.jump'), items: jumps },
    { key: 'packages', label: t('palette.packages'), items: packages }
  ];
}
