// Release dates shared by titles and FAQs. Page index decisions come from the API (S5, usePageDecision).
import type { PackageDetail } from '@opennavo/api';
import { currentReleaseDate } from './seoTitles';

/**
 * Release date for the current version: release data for this exact version, else the Homebrew
 * commit. versionChangedAt is when OpenNavo noticed the change and is never shown as a date.
 */
export function latestVersionDate(
  pkg: Pick<PackageDetail, 'displayName' | 'version' | 'latestRelease' | 'versionCommittedAt'>
): string | undefined {
  return currentReleaseDate(pkg) ?? pkg.versionCommittedAt ?? undefined;
}
