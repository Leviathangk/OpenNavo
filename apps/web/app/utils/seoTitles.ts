// Page titles and descriptions (WP1), pure for testing. Titles keep the full software name and state
// the intent the page serves; they never add "best", years or "free", and only claim a download
// when Homebrew can still install the package. Descriptions say what the page answers instead of
// stacking summary, command, version and install counts.
import { formatVersion } from '@opennavo/shared';

export type Translate = (key: string, named?: Record<string, unknown>) => string;

/** Every page title ends with the brand; paginated titles put the page label before it. */
export const TITLE_SUFFIX = ' · OpenNavo';

export function pagedTitle(title: string, page: number | undefined, label: string): string {
  if (!page || page <= 1) return title;
  return title.endsWith(TITLE_SUFFIX)
    ? `${title.slice(0, -TITLE_SUFFIX.length)} · ${label}${TITLE_SUFFIX}`
    : `${title} · ${label}`;
}

/** Homebrew writes "latest" for unversioned casks; only real version numbers go into titles. */
export function reliableVersion(version: string | null | undefined): string {
  const value = formatVersion(version ?? '');
  return value && value.toLowerCase() !== 'latest' ? value : '';
}

/** Summary without closing punctuation; a summary that only repeats the name counts as missing. */
export function usableSummary(summary: string | null | undefined, name: string): string {
  const value = (summary ?? '').trim().replace(/[。．.!！?？]+$/u, '');
  return value.toLowerCase() === name.trim().toLowerCase() ? '' : value;
}

export interface OverviewFacts {
  displayName: string;
  summary?: string | null;
  isFont: boolean;
  disabled: boolean;
}

export function overviewTitle(pkg: OverviewFacts, t: Translate): string {
  const name = pkg.displayName;
  if (pkg.disabled) return t('package.seo.titleUnavailable', { name });
  // Names such as "FiraCode Nerd Font" already say they are fonts.
  if (pkg.isFont && !/\bfonts?\b|字体/iu.test(name)) return t('package.seo.titleFont', { name });
  return t('package.seo.titleApp', { name });
}

export function overviewDescription(pkg: OverviewFacts, t: Translate): string {
  const name = pkg.displayName;
  const summary = usableSummary(pkg.summary, name);
  const key = pkg.disabled ? 'package.seo.descriptionUnavailable' : 'package.seo.description';
  return summary ? t(key, { name, summary }) : t(`${key}NoSummary`, { name });
}

export interface VersionFacts {
  displayName: string;
  version: string;
  latestRelease?: { version: string; publishedAt?: string | null; brewCommittedAt?: string | null } | null;
}

export function versionsTitle(pkg: VersionFacts, t: Translate): string {
  const version = reliableVersion(pkg.version);
  return version
    ? t('package.seo.versionsTitleLatest', { name: pkg.displayName, version })
    : t('package.seo.versionsTitle', { name: pkg.displayName });
}

/**
 * Release date of the current version: the vendor date, else the Homebrew commit time, and only when
 * the latest release entry is the current version. version_changed_at is when OpenNavo noticed the
 * change (bulk reset on 2026-10-07), so it is never shown as a release date.
 */
export function currentReleaseDate(pkg: VersionFacts): string | undefined {
  const latest = pkg.latestRelease;
  if (!latest || formatVersion(latest.version) !== formatVersion(pkg.version)) return undefined;
  return latest.publishedAt ?? latest.brewCommittedAt ?? undefined;
}

export function versionsDescription(pkg: VersionFacts, t: Translate, formatDate: (iso: string) => string): string {
  const name = pkg.displayName;
  const version = reliableVersion(pkg.version);
  if (!version) return t('package.seo.versionsDescriptionNoVersion', { name });
  const date = currentReleaseDate(pkg);
  return date
    ? t('package.versionsMetaDescription', { name, version, date: formatDate(date) })
    : t('package.seo.versionsDescriptionNoDate', { name, version });
}
