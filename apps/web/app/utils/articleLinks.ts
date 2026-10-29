import { LOCALE_METADATA, SITE_LOCALES } from '@opennavo/shared';
import type { Router } from 'vue-router';
import { LOCALIZED_FEEDS } from '#shared/utils/localizedFeeds';
import { toAppLocale } from './locale';

const PREFIXES = SITE_LOCALES.map(locale => LOCALE_METADATA[locale].prefix).filter(Boolean);
const FEEDS = LOCALIZED_FEEDS.map(
  ({ route }) => new RegExp(`^${route.replace('.', '\\.').replace(/:\w+/g, '[^/]+')}$`, 'i')
);

/** Resolve against Nuxt's generated i18n routes, rather than guessing localized URL sections. */
export function localizeArticlePath(path: string, locale: string, router: Pick<Router, 'resolve'>): string {
  // Vue Router matches paths without case sensitivity by default. Compare a whole segment so
  // /zh-hant cannot be mistaken for /zh, and preserve the spelling of the rest of the path.
  const lower = path.toLowerCase();
  const prefix = PREFIXES.find(value => lower === value || lower.startsWith(`${value}/`));
  const bare = prefix ? path.slice(prefix.length) || '/' : path;
  const targetPrefix = LOCALE_METADATA[toAppLocale(locale)].prefix;
  const target = `${targetPrefix}${bare === '/' ? (targetPrefix ? '' : '/') : bare}`;
  if (FEEDS.some(pattern => pattern.test(bare))) return target;
  const page = router.resolve(bare);
  // Slug routes can also match filenames. App tokens and background labels legitimately contain
  // dots; other files only localize when explicitly configured above.
  const dottedEntity = /\/:(?:token|label)(?:\(\))?\/?$/.test(page.matched.at(-1)?.path ?? '');
  if ((!dottedEntity && /\.[^/]+\/?$/.test(bare)) || !page.matched.length || !router.resolve(target).matched.length)
    return path;
  return target;
}
