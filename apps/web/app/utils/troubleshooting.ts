// Troubleshooting knowledge base (F7), pure for testing. Known issues are facts Hermes collected from
// official documents, GitHub issues and release notes, each with the app and macOS versions it applies to,
// its source and the day it was checked. Nothing here is a discussion: the page never invents a forum and
// never marks issues up as DiscussionForumPosting.
import type { PackageDetail, PublicComponents } from '@opennavo/api';
import type { SiteLocale } from '@opennavo/shared';

type Schemas = PublicComponents['schemas'];
export type KnownIssueFact = Schemas['KnownIssueFact'];
type PageDecision = Schemas['PageDecision'];

/**
 * The troubleshooting page opens with at least this many verified issues. Fewer fit the app page's Known
 * issues block, and a separate page would only repeat it (one target page per intent).
 */
export const TROUBLESHOOTING_MIN_ISSUES = 3;

type IssueSource = Pick<PackageDetail, 'facts'> | null | undefined;

/**
 * Issues a page may state: verified, naming the app and macOS versions they apply to, with steps, a source
 * and a check date. The API already sends only verified ones; anything else is left out here too.
 */
export function verifiedIssues(pkg: IssueSource): KnownIssueFact[] {
  return (pkg?.facts?.issues ?? []).filter(
    issue =>
      issue.evidence.status === 'verified' &&
      Boolean(issue.evidence.sourceUrl) &&
      Boolean(issue.evidence.checkedAt) &&
      issue.appVersions.length > 0 &&
      issue.macosVersions.length > 0 &&
      issue.steps.length > 0
  );
}

/** Whether /apps/{token}/troubleshooting exists; without enough verified issues it is a 404. */
export function hasTroubleshootingPage(pkg: IssueSource): boolean {
  return verifiedIssues(pkg).length >= TROUBLESHOOTING_MIN_ISSUES;
}

/** The troubleshooting page of an app (before the language prefix). */
export function troubleshootingPath(token: string): string {
  return `/apps/${token}/troubleshooting`;
}

/** Fragment of an issue on the troubleshooting page, so the app page's block can point at one. */
export function issueAnchor(index: number): string {
  return `issue-${index + 1}`;
}

/**
 * The page's decision: the server's (indexing.pages.troubleshooting, S5), which also drives the sitemap and
 * IndexNow. An older API without one gets the same rule worked out here: indexable only when the app's
 * overview is indexable in this language and every issue is written in it (an English fallback on a Chinese
 * page stays out of the index), with the page itself as its only language alternate, and not in the sitemap.
 */
export function troubleshootingDecision(
  pkg: Pick<PackageDetail, 'facts' | 'indexing'> | null | undefined,
  locale: SiteLocale
): PageDecision {
  const server = pkg?.indexing?.pages?.troubleshooting;
  if (server) return server;
  const issues = verifiedIssues(pkg);
  const overview = pkg?.indexing?.pages.overview;
  const enough = issues.length >= TROUBLESHOOTING_MIN_ISSUES;
  const localized = enough && issues.every(issue => issue.locale === locale);
  const indexable = localized && overview?.robots === 'index, follow';
  return {
    robots: indexable ? 'index, follow' : 'noindex, follow',
    canonicalPath: '',
    hreflang: indexable ? [locale] : [],
    inSitemap: false,
    announce: false,
    reason: indexable
      ? 'troubleshooting'
      : !enough
        ? 'troubleshooting_thin'
        : !localized
          ? 'locale_not_ready'
          : 'overview_not_indexable'
  };
}
