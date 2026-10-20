import { SITE_LOCALES, LOCALE_METADATA } from './locales.gen';

// Fixed page types for web paths, shared by the server's crawler counters and the GA4 page_type
// parameter (WP8). Locale prefixes are ignored; unknown shapes fall into "other".
/** A versions page stands on its own once this many releases carry notes (robots and the versions sitemap). */
export const VERSION_PAGE_MIN_NOTES = 3;

const LOCALE_PREFIXES = SITE_LOCALES.map(locale => LOCALE_METADATA[locale].prefix).filter(Boolean);
const APP_TABS = new Set(['versions', 'dependencies', 'details', 'troubleshooting']);
/** Intent pages beside an app (X2): their own page types, so their traffic is not counted as tabs. */
const APP_INTENTS = new Set(['alternatives', 'uninstall']);
const LISTS = new Set(['apps', 'categories', 'collections', 'rankings', 'discover', 'browse', 'background-items']);
const STATIC_PAGES = new Set(['download', 'security', 'about', 'feedback', 'brewfile', 'search']);

export function webRouteType(path: string): string {
  let clean = path.split('?', 1)[0] || '/';
  const prefix = LOCALE_PREFIXES.find(value => clean === value || clean.startsWith(`${value}/`));
  if (prefix) clean = clean.slice(prefix.length) || '/';
  if (clean.startsWith('/_nuxt/')) return 'asset';
  if (clean.endsWith('/_payload.json')) return 'payload';
  if (clean.startsWith('/__og-image__/') || clean.startsWith('/_og/')) return 'og_image';
  if (clean === '/robots.txt') return 'robots';
  if (clean.startsWith('/__sitemap__/') || /^\/sitemap[^/]*\.xml$/.test(clean)) return 'sitemap';
  if (clean === '/') return 'home';
  const [, section = '', item, tab, extra] = clean.replace(/\/$/, '').split('/');
  if (section === 'apps' && item && tab === 'feed.xml' && !extra) return 'feed';
  if (section === 'apps' && item && !tab && item.endsWith('.md')) return 'markdown';
  if (section === 'apps' && item && !extra)
    return tab ? (APP_TABS.has(tab) ? `app_${tab}` : APP_INTENTS.has(tab) ? `intent_${tab}` : 'other') : 'app';
  if (section === 'categories' && item && !tab) return 'category';
  if (section === 'collections' && item && tab === 'feed.xml' && !extra) return 'feed';
  if (section === 'collections' && item && !tab) return 'collection';
  // A–Z leaves (/browse/a, /browse/fonts/a) are lists like their index.
  if (section === 'browse' && (!tab || (item === 'fonts' && !extra))) return 'list';
  if (section === 'background-items' && item && !tab) return 'background_item';
  if (section === 'security' && item && !tab) return 'security_topic';
  if (section === 'articles' && !tab) return item === 'feed.xml' ? 'feed' : item ? 'article' : 'article_list';
  if (section === 'authors' && item && !tab) return 'author';
  if (!item && LISTS.has(section)) return 'list';
  if (!item && STATIC_PAGES.has(section)) return section;
  return 'other';
}
