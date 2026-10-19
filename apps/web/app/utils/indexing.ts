// Page-level index decisions (WP11), pure for testing. The API decides whether an app's pages are
// indexable and in which languages; sub-pages need their own value on top of that.
import type { PackageDetail } from '@opennavo/api';
import { LOCALE_METADATA, VERSION_PAGE_MIN_NOTES, type Locale } from '@opennavo/shared';
import { currentReleaseDate } from './seoTitles';

export type PackageTab = 'overview' | 'versions' | 'dependencies' | 'details';

type IndexFacts = Pick<PackageDetail, 'indexing' | 'dependsOn' | 'conflictsWith'>;

function hasRelations(pkg: IndexFacts): boolean {
  const lists = [
    ...Object.values(pkg.dependsOn ?? {}),
    pkg.conflictsWith?.casks ?? [],
    pkg.conflictsWith?.formulae ?? []
  ];
  return lists.some(value => Array.isArray(value) && value.length > 0);
}

/**
 * robots value for one tab; undefined keeps the default (index). While the policy does not apply to
 * the app (enforced false) every page keeps today's behaviour. Install details repeat the overview,
 * so they are not noindexed but point their canonical at it (consolidatesToOverview).
 */
export function packageRobots(pkg: IndexFacts, tab: PackageTab, locale: Locale): string | undefined {
  const indexing = pkg.indexing;
  if (!indexing) return undefined;
  const pageHasValue =
    !indexing.enforced ||
    tab === 'overview' ||
    tab === 'details' ||
    (tab === 'versions' && indexing.notesCount >= VERSION_PAGE_MIN_NOTES) ||
    (tab === 'dependencies' && hasRelations(pkg));
  return indexing.indexable && indexing.locales.includes(locale) && pageHasValue ? undefined : 'noindex, follow';
}

/**
 * Whether a tab hands its ranking to the app's overview through its canonical. Google prefers a
 * canonical over noindex for pages that repeat another one, and install details repeat the
 * overview. Only while the policy applies and the overview is indexed in this language; anywhere
 * else the tab keeps its own canonical, and packageRobots decides whether it is indexed.
 */
export function consolidatesToOverview(pkg: IndexFacts, tab: PackageTab, locale: Locale): boolean {
  const indexing = pkg.indexing;
  return tab === 'details' && !!indexing?.enforced && indexing.indexable && indexing.locales.includes(locale);
}

/**
 * hreflang values to keep for an enforced app: its published languages (both the region and the
 * bare language form that @nuxtjs/i18n emits) and x-default while English is among them. null keeps
 * every alternate.
 */
export function allowedHreflangs(indexing: PackageDetail['indexing']): string[] | null {
  if (!indexing?.enforced) return null;
  return hreflangsFor(indexing.locales);
}

/** hreflang values for some languages (region and bare language forms), plus x-default with English. */
export function hreflangsFor(locales: readonly Locale[]): string[] {
  const out = new Set<string>();
  for (const locale of locales) {
    const hreflang = LOCALE_METADATA[locale].hreflang;
    out.add(hreflang);
    out.add(hreflang.split('-')[0] ?? hreflang);
  }
  if (locales.includes('en-US')) out.add('x-default');
  return [...out];
}

/**
 * Release date for the current version: release data for this exact version, else the Homebrew
 * commit. versionChangedAt is when OpenNavo noticed the change and is never shown as a date.
 */
export function latestVersionDate(
  pkg: Pick<PackageDetail, 'displayName' | 'version' | 'latestRelease' | 'versionCommittedAt'>
): string | undefined {
  return currentReleaseDate(pkg) ?? pkg.versionCommittedAt ?? undefined;
}
