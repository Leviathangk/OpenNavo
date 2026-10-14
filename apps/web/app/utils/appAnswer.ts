// App-page answers (WP9), pure for testing. Every sentence comes from a recorded fact: Homebrew's
// declarations, OpenNavo's category, or price and macOS evidence verified against a source. A
// missing fact drops its sentence instead of being guessed: chip support is never read as native
// code, and Homebrew's macOS constraint is only shortened to "X or later" when that is all it says.
import type { PackageDetail } from '@opennavo/api';
import { formatDate, formatVersion } from '@opennavo/shared';
import type { Locale } from '@opennavo/shared';
import type { Translate } from './seoTitles';

type Arch = 'arm64' | 'x86_64';

/** Host and path without scheme, as @opennavo/ui displayUrl shows links (kept here so tests stay Vue-free). */
const displayUrl = (url: string) => url.replace(/^https?:\/\//, '').replace(/\/$/, '');

/** Homebrew's macOS requirement when it is a single "≥ version", else null. */
export function minimumOnly(pkg: Pick<PackageDetail, 'dependsOn'>): string | null {
  const macos = (pkg.dependsOn?.requirements as Record<string, unknown> | undefined)?.macos;
  if (!macos || typeof macos !== 'object' || Array.isArray(macos)) return null;
  const entries = Object.entries(macos as Record<string, unknown>);
  if (entries.length !== 1) return null;
  const [operator, versions] = entries[0]!;
  return operator === '>=' && Array.isArray(versions) && versions.length === 1 && typeof versions[0] === 'string'
    ? versions[0]
    : null;
}

/** Chinese and Japanese sentences run together; the other languages separate them with a space. */
export function joinSentences(sentences: string[], locale: Locale): string {
  return sentences.join(locale === 'zh-CN' || locale === 'ja-JP' ? '' : ' ');
}

type AnswerFacts = Pick<
  PackageDetail,
  'displayName' | 'isFont' | 'primaryCategory' | 'artifacts' | 'price' | 'dependsOn'
>;

/** One or two lead sentences: what it is, then verified price and the minimum macOS when known. */
export function leadSentences(pkg: AnswerFacts, t: Translate): string[] {
  const name = pkg.displayName;
  const category = pkg.primaryCategory?.name;
  const app = pkg.artifacts.apps.length > 0;
  const out = [
    pkg.isFont
      ? t('package.answer.font', { name })
      : t(`package.answer.${app ? 'app' : 'software'}${category ? 'Category' : ''}`, { name, category })
  ];
  const details: string[] = [];
  // Old evidence stays on the facts card with its date; the lead only states current facts.
  if (pkg.price && !pkg.price.stale) details.push(t(`package.answer.price.${pkg.price.type}`));
  const minimum = minimumOnly(pkg);
  if (minimum) details.push(t('package.answer.requires', { version: minimum }));
  return [...out, ...details];
}

export type DownloadChoice =
  | { kind: 'direct'; url: string }
  | { kind: 'arch'; items: { arch: Arch; url: string }[] }
  | { kind: 'official'; url: string; varies: boolean };

/**
 * Where to download without Homebrew: the file Homebrew uses when one URL serves every Mac, one per
 * chip when each chip has exactly one, otherwise the official website.
 */
export function downloadChoice(
  pkg: Pick<PackageDetail, 'downloadUrl' | 'homepage' | 'platforms' | 'version'>
): DownloadChoice | null {
  const platforms = (pkg.platforms ?? []).filter(platform => platform.version === pkg.version);
  const official = (varies: boolean): DownloadChoice | null =>
    pkg.homepage ? { kind: 'official', url: pkg.homepage, varies } : null;
  if (!platforms.length) return pkg.downloadUrl ? { kind: 'direct', url: pkg.downloadUrl } : official(false);
  const perArch = new Map<Arch, Set<string>>();
  for (const platform of platforms) {
    if (!platform.downloadUrl) return official(true);
    const arch = platform.arch as Arch;
    perArch.set(arch, (perArch.get(arch) ?? new Set()).add(platform.downloadUrl));
  }
  const urls = new Set(platforms.map(platform => platform.downloadUrl!));
  if (urls.size === 1) return { kind: 'direct', url: [...urls][0]! };
  if ([...perArch.values()].some(set => set.size > 1)) return official(true);
  const items = (['arm64', 'x86_64'] as const).flatMap(arch =>
    perArch.has(arch) ? [{ arch, url: [...perArch.get(arch)!][0]! }] : []
  );
  return { kind: 'arch', items };
}

/** Whether the Homebrew definition lists files for `brew uninstall --zap`. */
export function hasZap(pkg: Pick<PackageDetail, 'artifacts'>): boolean {
  return (pkg.artifacts.entries ?? []).some(entry => entry.type === 'zap');
}

export type InstallNote = { key: 'installApp' | 'installFont' } | { key: 'installPkg'; pkg: string };

/** What the install does, by kind: an app bundle, a font, or a .pkg installer that needs a password. */
export function installNote(pkg: Pick<PackageDetail, 'artifacts' | 'isFont'>): InstallNote | null {
  if (pkg.isFont) return { key: 'installFont' };
  if (pkg.artifacts.apps.length) return { key: 'installApp' };
  if (pkg.artifacts.pkgs.length) return { key: 'installPkg', pkg: pkg.artifacts.pkgs[0]! };
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

/** Amount with currency and billing period, e.g. "$9.99 per month"; empty without an amount. */
export function priceAmount(price: NonNullable<PackageDetail['price']>, t: Translate, locale: Locale): string {
  if (price.amount == null || !price.currency) return '';
  let amount: string;
  try {
    amount = new Intl.NumberFormat(locale, { style: 'currency', currency: price.currency }).format(price.amount);
  } catch {
    amount = `${price.amount} ${price.currency}`;
  }
  if (price.period === 'month') return t('package.facts.perMonth', { price: amount });
  if (price.period === 'year') return t('package.facts.perYear', { price: amount });
  return amount;
}

/**
 * Two different dates for the current version: when Homebrew recorded it and, when release data
 * covers this exact version, when the vendor published it.
 */
export function versionDates(pkg: Pick<PackageDetail, 'version' | 'versionCommittedAt' | 'latestRelease'>): {
  recorded?: string;
  released?: string;
} {
  const version = formatVersion(pkg.version);
  if (!version || version.toLowerCase() === 'latest') return {};
  const latest = pkg.latestRelease;
  const released = latest && formatVersion(latest.version) === version ? (latest.publishedAt ?? undefined) : undefined;
  return { recorded: pkg.versionCommittedAt ?? undefined, released };
}
