import { formatDate } from './packageDate';
// App-page sentences (WP9, T2), pure for testing. Every sentence comes from a recorded fact in the
// detail's facts model or the catalog record: what the app is, who makes it, where a link app's files
// come from, and what a Homebrew install puts on the Mac. A missing fact drops its sentence instead of
// being guessed. Prices, chips and system requirements stay out of the lead: they live in the facts
// table beside it (T2), which states each one with its source.
import type { SiteLocale as Locale } from '@opennavo/shared';
import { linkSource } from './linkApp';
import { isDated } from './packageFacts';
import type { PackagePage } from './packageFacts';
import type { Translate } from './seoTitles';

/** Host and path without scheme, as @opennavo/ui displayUrl shows links (kept here so tests stay Vue-free). */
const displayUrl = (url: string) => url.replace(/^https?:\/\//, '').replace(/\/$/, '');

/** Chinese and Japanese sentences run together; the other languages separate them with a space. */
export function joinSentences(sentences: string[], locale: Locale): string {
  return sentences.join(locale === 'zh-CN' || locale === 'zh-Hant' || locale === 'ja-JP' ? '' : ' ');
}

type LeadFacts = Pick<PackagePage, 'kind' | 'displayName' | 'isFont' | 'primaryCategory' | 'developer' | 'link'> & {
  facts: Pick<PackagePage['facts'], 'overview'>;
};

/**
 * The lead (T2): what it is and who makes it; for a link app also where its files come from, since
 * OpenNavo hosts none. Facts go to the table beside it rather than into one long sentence.
 */
export function leadSentences(pkg: LeadFacts, t: Translate): string[] {
  const name = pkg.displayName;
  const category = pkg.primaryCategory?.name;
  // Link apps are apps by definition; casks say so through an .app bundle.
  const app = pkg.kind === 'link' || pkg.facts.overview.artifacts.apps.length > 0;
  const out = [
    pkg.isFont
      ? t('package.answer.font', { name })
      : t(`package.answer.${app ? 'app' : 'software'}${category ? 'Category' : ''}`, { name, category })
  ];
  if (pkg.developer) out.push(t('package.lead.developer', { developer: pkg.developer.trim().replace(/[.。]+$/, '') }));
  const source = pkg.kind === 'link' && pkg.link ? linkSource(pkg.link) : undefined;
  if (source) out.push(t('package.lead.source', { source }));
  return out;
}

/** Whether the Homebrew definition lists files for `brew uninstall --zap`. */
export function hasZap(pkg: Pick<PackagePage, 'facts'>): boolean {
  return pkg.facts.uninstall.some(item => item.phase === 'zap' && isDated(item.evidence));
}

export type InstallNote = { key: 'installApp' | 'installFont' } | { key: 'installPkg'; pkg: string };

/** What the install does, by kind: an app bundle, a font, or a .pkg installer that needs a password. */
export function installNote(pkg: Pick<PackagePage, 'facts' | 'isFont'>): InstallNote | null {
  const artifacts = pkg.facts.overview.artifacts;
  if (pkg.isFont) return { key: 'installFont' };
  if (artifacts.apps.length) return { key: 'installApp' };
  if (artifacts.pkgs.length) return { key: 'installPkg', pkg: artifacts.pkgs[0]! };
  return null;
}

type Evidence = { sourceUrl: string; verifiedAt: string };

export function evidenceLine(evidence: Evidence, t: Translate, locale: Locale): string {
  // Abbreviated dates end with a period ("14 окт."); the sentence keeps just one.
  return t('package.facts.checked', {
    source: displayUrl(evidence.sourceUrl),
    date: formatDate(evidence.verifiedAt, { locale })
  }).replace(/\.\.$/, '.');
}
