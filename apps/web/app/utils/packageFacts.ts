import { formatDate } from './packageDate';
// App-page facts (T2, T3, T6), pure for testing. Every block of the overview, its questions and its
// JSON-LD read the API's unified facts model (PackageDetail.facts) through these helpers, so no part
// of the page can state what another contradicts. Evidence decides the wording: a verified fact is
// stated with its check date, a stale one only "as of" its date and never as current, and anything
// pending review or without a dated source is left out.
import type { PackageDetail, PackageRepository, PublicComponents } from '@opennavo/api';
import { disabledByHomebrew, formatVersion, isThirdPartyTap } from '@opennavo/shared';
import type { SiteLocale as Locale } from '@opennavo/shared';
import { httpsUrl, linkSource } from './linkApp';
import type { Translate } from './seoTitles';

type Schemas = PublicComponents['schemas'];
export type AppFacts = Schemas['AppFacts'];
export type FactEvidence = Schemas['FactEvidence'];
export type RequirementFact = Schemas['RequirementFact'];
export type UninstallFact = Schemas['UninstallFact'];
export type PriceFact = Schemas['PriceFact'];
export type AlternativeFact = Schemas['PackageAlternative'];
export type ComparisonAttributeFact = Schemas['ComparisonAttributeFact'];
export type IntegrationFact = Schemas['AppIntegrationFact'];
export type SecurityIdentityFact = Schemas['SecurityIdentityFact'];
export type Arch = 'arm64' | 'x86_64';

/**
 * Detail fields the web does not read (T6). platforms is most of a detail's weight; artifacts, caveats,
 * the download and its checksum, the price, the vendor's macOS statement and the alternatives live on
 * in facts, which every block reads, so their top-level copies would only travel twice.
 */
const DROPPED = [
  'platforms',
  'descriptionEn',
  'onRequest',
  'tags',
  'summaryTranslation',
  'dependencies',
  'minMacos',
  'artifacts',
  'caveats',
  'downloadUrl',
  'downloadSha256',
  'price',
  'macosSupplement',
  'alternatives'
] as const;

/** The detail as app pages keep it: slimmed, and always with a facts model. */
export type PackagePage = Omit<PackageDetail, (typeof DROPPED)[number] | 'facts'> & {
  facts: AppFacts;
  /** Optional repository signals from the follow-up public API contract. */
  repository?: PackageRepository;
};

/** useAsyncData transform for the detail payload (T6). */
export function slimPackage(detail: PackageDetail & { repository?: PackageRepository }): PackagePage {
  const facts: AppFacts = { ...(detail.facts ?? fallbackFacts(detail)) };
  // The legacy price object lives on in facts.prices, which the blocks, questions and JSON-LD read.
  delete facts.price;
  const page: Record<string, unknown> = { ...detail, facts };
  for (const key of DROPPED) delete page[key];
  // Release files of a link app reach the page only with an https address: the download panel shows no
  // other, and the payload (serialized into the HTML) should not carry one either.
  const latest = detail.link?.github?.latest;
  if (latest && detail.link?.github)
    page.link = {
      ...detail.link,
      github: {
        ...detail.link.github,
        latest: { ...latest, assets: latest.assets.filter(asset => httpsUrl(asset.url)) }
      }
    };
  return page as PackagePage;
}

/** An older API without facts: the page keeps Homebrew's basics and states nothing it cannot source. */
function fallbackFacts(detail: PackageDetail): AppFacts {
  return {
    requirements: [],
    uninstall: [],
    overview: {
      version: detail.version,
      downloadUrl: detail.downloadUrl ?? null,
      sha256: detail.downloadSha256 ?? null,
      artifacts: detail.artifacts,
      caveats: detail.caveats ?? null,
      aliases: [],
      dependencyCount: (detail.dependsOn?.casks.length ?? 0) + (detail.dependsOn?.formulae.length ?? 0),
      conflictCount: detail.conflictsWith.casks.length + detail.conflictsWith.formulae.length
    },
    alternatives: detail.alternatives ?? [],
    prices: [],
    offers: []
  };
}

export interface Capabilities {
  /** Homebrew commands (install, uninstall, --zap, upgrade) and what Homebrew's definition declares. */
  homebrew: boolean;
  /** Uninstall and optional clean-up: every cask, and link apps only when facts list declarations. */
  uninstall: boolean;
  /** Dependency and conflict summary. */
  dependencies: boolean;
  /** Homebrew's install analytics. */
  installs: boolean;
  /** Security facts: software that runs, not typefaces. */
  safety: boolean;
}

/** What the page shows, by distribution (T3): Homebrew casks, link apps (any macOS software) and fonts. */
export function capabilities(pkg: Pick<PackagePage, 'kind' | 'isFont' | 'facts'>): Capabilities {
  const homebrew = pkg.kind === 'cask';
  const { dependencyCount, conflictCount } = pkg.facts.overview;
  return {
    homebrew,
    uninstall: homebrew || pkg.facts.uninstall.length > 0,
    dependencies: homebrew && dependencyCount + conflictCount > 0,
    installs: pkg.kind !== 'link',
    safety: !pkg.isFont
  };
}

/** Evidence the page may state as a current fact. */
export function isVerified(evidence: FactEvidence | null | undefined): boolean {
  return evidence?.status === 'verified' && Boolean(evidence.checkedAt);
}

/** Evidence the page may show with its date: verified, or stale and so only "as of" that date. */
export function isDated(evidence: FactEvidence | null | undefined): boolean {
  return (evidence?.status === 'verified' || evidence?.status === 'stale') && Boolean(evidence.checkedAt);
}

/** The oldest check date among the evidence, as a full ISO timestamp. */
export function oldestCheck(evidence: readonly (FactEvidence | null | undefined)[]): string | undefined {
  const dates = evidence.flatMap(item => (item?.checkedAt && isDated(item) ? [item.checkedAt] : []));
  return dates.sort((a, b) => Date.parse(a) - Date.parse(b))[0];
}

/** The version Homebrew (or the developer, for link apps) offers today. */
export function currentVersion(pkg: Pick<PackagePage, 'facts' | 'version'>): string {
  return pkg.facts.overview.version || pkg.version;
}

/** Homebrew's own requirement rows, per declaration: their source is the Homebrew definition. */
export function homebrewRows(pkg: Pick<PackagePage, 'facts' | 'sourceUrl'>): RequirementFact[] {
  return pkg.facts.requirements.filter(
    row => row.architectures.length > 0 && Boolean(pkg.sourceUrl) && row.evidence.sourceUrl === pkg.sourceUrl
  );
}

/** Verified rows that describe the current version, from any source. */
export function currentRows(pkg: Pick<PackagePage, 'facts' | 'version'>): RequirementFact[] {
  const version = currentVersion(pkg);
  return pkg.facts.requirements.filter(row => isVerified(row.evidence) && row.versions.includes(version));
}

/** Dated, verified or stale requirement rows; pending claims stay hidden. */
export function shownRequirements(pkg: Pick<PackagePage, 'facts'>): RequirementFact[] {
  return pkg.facts.requirements.filter(row => isDated(row.evidence));
}

function common(values: (string | null)[]): string | null {
  const set = new Set(values);
  return values.length > 0 && set.size === 1 ? (values[0] ?? null) : null;
}

/** macOS range every verified row for the current version agrees on; null bounds are not stated. */
export function macosRange(pkg: Pick<PackagePage, 'facts' | 'version'>): {
  minimum: string | null;
  maximum: string | null;
} {
  const rows = currentRows(pkg);
  return {
    minimum: common(rows.map(row => row.minimumMacOS)),
    maximum: common(rows.map(row => row.maximumMacOS))
  };
}

/** Chips the verified rows for the current version name, Apple silicon first. */
export function chipsOf(pkg: Pick<PackagePage, 'facts' | 'version'>): Arch[] {
  const archs = new Set(currentRows(pkg).flatMap(row => row.architectures));
  return (['arm64', 'x86_64'] as const).filter(arch => archs.has(arch));
}

/** The macOS range as the page writes it, e.g. "macOS 13 or later · up to macOS 15"; empty when unknown. */
export function macosText(range: { minimum: string | null; maximum: string | null }, t: Translate): string {
  return [
    range.minimum ? t('package.facts.minimum', { version: range.minimum }) : '',
    range.maximum ? t('package.facts.maximum', { version: range.maximum }) : ''
  ]
    .filter(Boolean)
    .join(' · ');
}

export type DownloadChoice =
  | { kind: 'direct'; url: string }
  | { kind: 'arch'; items: { arch: Arch; url: string }[] }
  | { kind: 'official'; url: string; varies: boolean };

/**
 * Where to download without Homebrew: the file Homebrew uses when one URL serves every Mac, one per
 * chip when each chip has exactly one, otherwise the official website. Read from Homebrew's rows for
 * the current version, so it matches the requirements table.
 */
export function downloadChoice(
  pkg: Pick<PackagePage, 'facts' | 'homepage' | 'version' | 'sourceUrl'>
): DownloadChoice | null {
  const version = currentVersion(pkg);
  const rows = homebrewRows(pkg).filter(row => isDated(row.evidence) && row.versions.includes(version));
  const official = (varies: boolean): DownloadChoice | null =>
    pkg.homepage ? { kind: 'official', url: pkg.homepage, varies } : null;
  const download = pkg.facts.overview.downloadUrl;
  if (!rows.length) return download ? { kind: 'direct', url: download } : official(false);
  const perArch = new Map<Arch, Set<string>>();
  for (const row of rows) {
    if (!row.downloadUrl) return official(true);
    for (const arch of row.architectures as Arch[])
      perArch.set(arch, (perArch.get(arch) ?? new Set()).add(row.downloadUrl));
  }
  const urls = new Set(rows.map(row => row.downloadUrl!));
  if (urls.size === 1) return { kind: 'direct', url: [...urls][0]! };
  if ([...perArch.values()].some(set => set.size > 1)) return official(true);
  const items = (['arm64', 'x86_64'] as const).flatMap(arch =>
    perArch.has(arch) ? [{ arch, url: [...perArch.get(arch)!][0]! }] : []
  );
  return { kind: 'arch', items };
}

/** Uninstall declarations by phase, with the dates of their evidence (zap is the optional data clean-up). */
export function uninstallFacts(pkg: Pick<PackagePage, 'facts'>, phase: 'uninstall' | 'zap'): UninstallFact[] {
  return pkg.facts.uninstall.filter(item => item.phase === phase && isDated(item.evidence));
}

/** Prices the page shows: verified ones as current, stale ones with their date. */
export function shownPrices(pkg: Pick<PackagePage, 'facts'>): PriceFact[] {
  return (pkg.facts.prices ?? []).filter(
    price => isDated(price.evidence) && price.amount != null && Boolean(price.currency)
  );
}

/** Prices the page may state as current: verified. */
export function verifiedPrices(pkg: Pick<PackagePage, 'facts'>): PriceFact[] {
  return (pkg.facts.prices ?? []).filter(price => isVerified(price.evidence));
}

/** Amount with currency and billing period, e.g. "$9.99 per month"; empty without an amount. */
export function priceAmount(price: Pick<PriceFact, 'amount' | 'currency' | 'period'>, t: Translate, locale: Locale) {
  if (price.amount == null || !price.currency) return '';
  let amount: string;
  try {
    amount = new Intl.NumberFormat(locale, { style: 'currency', currency: price.currency }).format(
      Number(price.amount)
    );
  } catch {
    amount = `${price.amount} ${price.currency}`;
  }
  if (price.period === 'monthly') return t('package.facts.perMonth', { price: amount });
  if (price.period === 'yearly') return t('package.facts.perYear', { price: amount });
  return amount;
}

/** How a price is paid, in the page language: "Subscription", or "Free to download" for a store listing. */
export function priceUsage(price: Pick<PriceFact, 'usage' | 'downloadFree'>, t: Translate): string {
  if (price.usage !== 'unknown') return t(`package.facts.types.${price.usage}`);
  return price.downloadFree ? t('package.price.downloadFree') : '';
}

/** A vendor plan name worth showing; the API writes "unspecified" and "App Store" when there is none. */
export function priceEdition(price: Pick<PriceFact, 'edition' | 'channel'>): string {
  const edition = price.edition.trim();
  return edition && edition !== 'unspecified' && !(price.channel === 'app_store' && edition === 'App Store')
    ? edition
    : '';
}

/** Region name in the page language; ZZ is the API's "no region". */
export function priceRegionName(region: string, locale: Locale): string {
  if (!region || region === 'ZZ') return '';
  try {
    return new Intl.DisplayNames([locale], { type: 'region' }).of(region) ?? region;
  } catch {
    return region;
  }
}

/** Security identities the page may state: verified, and for the version on offer. */
export function verifiedIdentities(pkg: Pick<PackagePage, 'facts'>): SecurityIdentityFact[] {
  return (pkg.facts.security?.identities ?? []).filter(identity => isVerified(identity.evidence));
}

/** Alternatives with only their verified comparison attributes and prices (F3). */
export function verifiedAlternatives(pkg: Pick<PackagePage, 'facts'>): AlternativeFact[] {
  return pkg.facts.alternatives.map(item => ({
    ...item,
    attributes: (item.attributes ?? []).filter(attribute => isVerified(attribute.evidence)),
    prices: (item.prices ?? []).filter(price => isVerified(price.evidence))
  }));
}

/** Font files the definition installs, with their formats (fonts have no other facts table rows). */
export function fontFiles(pkg: Pick<PackagePage, 'facts'>): { count: number; formats: string[] } {
  const files = (pkg.facts.overview.artifacts.entries ?? [])
    .filter(entry => entry.type === 'font')
    .flatMap(entry => entry.sources);
  const formats = [
    ...new Set(files.flatMap(file => /\.([a-z0-9]{2,5})$/i.exec(file)?.[1]?.toUpperCase() ?? []))
  ].sort();
  return { count: new Set(files).size, formats };
}

/** True when the definition names a real version (Homebrew writes "latest" for unversioned casks). */
export function hasVersion(version: string): boolean {
  const value = formatVersion(version);
  return Boolean(value) && value.toLowerCase() !== 'latest';
}

export interface QuickFact {
  key: string;
  label: string;
  value: string;
  /** Commands and paths: one complete code element, so they are extractable as they are. */
  code?: boolean;
  /** In-page anchor with the details, e.g. #price. */
  anchor?: string;
  anchorLabel?: string;
}

/** Paused by OpenNavo's 90-day rule while Homebrew still offers the app (T1), apart from Homebrew's own disable. */
export function pausedByOpenNavo(
  pkg: Pick<PackagePage, 'recommendationPaused' | 'disable' | 'disabled' | 'homebrewStatus'>
): boolean {
  if (disabledByHomebrew(pkg)) return false;
  return pkg.recommendationPaused ?? Boolean(pkg.disable);
}

/**
 * The facts table (T2): what the stats strip above the tabs does not already say, each value from the
 * verified facts the rest of the page uses, and the oldest check date among them. Casks list chips,
 * macOS, price, developer, license, the install command and Homebrew's status; link apps their version,
 * download source and signature; fonts their files and install location.
 */
export function quickFacts(pkg: PackagePage, t: Translate, locale: Locale): { rows: QuickFact[]; checkedAt?: string } {
  const rows: QuickFact[] = [];
  const evidence: (FactEvidence | null | undefined)[] = [];
  const add = (row: QuickFact) => {
    if (row.value) rows.push(row);
  };
  const date = (value: string) => formatDate(value, { locale });

  if (pkg.kind === 'link') {
    const version = currentVersion(pkg);
    if (hasVersion(version)) {
      const latest = pkg.latestRelease;
      const released =
        latest && formatVersion(latest.version) === formatVersion(version) ? latest.publishedAt : undefined;
      add({
        key: 'version',
        label: t('package.stats.latest'),
        value: [formatVersion(version), released ? date(released) : ''].filter(Boolean).join(' · ')
      });
    }
    const source = pkg.link ? linkSource(pkg.link) : undefined;
    if (source) add({ key: 'source', label: t('package.quick.source'), value: source });
  }

  if (pkg.isFont) {
    const files = fontFiles(pkg);
    if (files.count)
      add({
        key: 'files',
        label: t('package.quick.fontFiles'),
        value: [String(files.count), files.formats.join(', ')].filter(Boolean).join(' · ')
      });
    if (pkg.kind === 'cask')
      add({
        key: 'location',
        label: t('package.details.location'),
        value: '~/Library/Fonts',
        code: true
      });
  } else {
    const rowsNow = currentRows(pkg);
    const chips = chipsOf(pkg);
    if (chips.length) {
      add({
        key: 'chips',
        label: t('package.quick.chips'),
        value: chips.map(arch => t(`package.arch.${arch}`)).join(' · ')
      });
      evidence.push(...rowsNow.filter(row => row.architectures.length).map(row => row.evidence));
    }
    const macos = macosText(macosRange(pkg), t);
    if (macos) {
      add({ key: 'macos', label: t('package.quick.macos'), value: macos });
      evidence.push(...rowsNow.map(row => row.evidence));
    }
  }

  const prices = verifiedPrices(pkg);
  if (prices.length) {
    const usages = [...new Set(prices.map(price => priceUsage(price, t)).filter(Boolean))];
    const amount = prices.map(price => priceAmount(price, t, locale)).find(Boolean);
    add({
      key: 'price',
      label: t('package.facts.price'),
      value: [usages.join(' / '), amount].filter(Boolean).join(' · '),
      anchor: shownPrices(pkg).length ? 'price' : undefined,
      anchorLabel: shownPrices(pkg).length ? t('package.quick.priceDetails') : undefined
    });
    evidence.push(...prices.map(price => price.evidence));
  } else if (pkg.link && pkg.link.pricing !== 'unknown') {
    // A link app's listed pricing has no evidence of its own, so it is stated without a check date.
    add({
      key: 'price',
      label: t('package.facts.price'),
      value: t(`package.link.pricing.${pkg.link.pricing}`)
    });
  }

  const identity = verifiedIdentities(pkg).find(item => item.developer || item.teamId);
  const developer = identity?.developer || pkg.developer;
  if (developer) {
    add({ key: 'developer', label: t('package.quick.developer'), value: developer });
    if (identity?.developer) evidence.push(identity.evidence);
  }
  const teamId = identity?.teamId ?? pkg.link?.teamId;
  if (teamId) {
    add({ key: 'teamId', label: t('package.link.teamId'), value: teamId, code: true });
    if (identity?.teamId) evidence.push(identity.evidence);
  }
  if (pkg.license) add({ key: 'license', label: t('package.info.license'), value: pkg.license });

  if (pkg.kind === 'cask') {
    add({
      key: 'install',
      label: t('package.quick.install'),
      value: pkg.installCommand,
      code: true
    });
    const status = disabledByHomebrew(pkg)
      ? t('package.quick.homebrewDisabled')
      : pausedByOpenNavo(pkg)
        ? t('package.quick.homebrewPaused')
        : pkg.deprecation
          ? t('package.deprecated')
          : t(pkg.autoUpdates ? 'package.quick.homebrewAuto' : 'package.quick.homebrewAvailable');
    add({ key: 'homebrew', label: 'Homebrew', value: status });
  }

  const names = pkg.names.filter(name => name !== pkg.displayName);
  if (names.length) add({ key: 'names', label: t('package.details.names'), value: names.join(', ') });

  return { rows, checkedAt: oldestCheck(evidence) };
}

export type SourceKind =
  | 'homebrew'
  | 'formulae'
  | 'price'
  | 'appStore'
  | 'requirements'
  | 'security'
  | 'issue'
  | 'integration';

export interface FactSource {
  kind: SourceKind;
  url: string;
  /** Present only when the evidence carries a check date. */
  checkedAt?: string;
  stale: boolean;
}

/**
 * Every source the page's facts cite, once each, with its check date (T2 Sources). Only dated evidence
 * (verified or stale) is cited; a source without a date is never given one.
 */
export function factSources(pkg: PackagePage): FactSource[] {
  const out: FactSource[] = [];
  const seen = new Set<string>();
  const add = (kind: SourceKind, url: string | null | undefined, evidence?: FactEvidence | null) => {
    if (!url || !url.startsWith('https://') || seen.has(url)) return;
    seen.add(url);
    out.push({
      kind,
      url,
      checkedAt: evidence && isDated(evidence) ? (evidence.checkedAt ?? undefined) : undefined,
      stale: evidence?.status === 'stale'
    });
  };
  if (pkg.kind === 'cask') {
    const homebrew = [...homebrewRows(pkg).map(row => row.evidence), ...pkg.facts.uninstall.map(item => item.evidence)];
    add('homebrew', pkg.sourceUrl, homebrew.find(isDated));
    // formulae.brew.sh only lists Homebrew's own taps.
    if (!isThirdPartyTap(pkg.tap)) add('formulae', pkg.formulaeUrl);
  }
  for (const price of shownPrices(pkg))
    add(price.channel === 'app_store' ? 'appStore' : 'price', price.evidence.sourceUrl, price.evidence);
  for (const row of pkg.facts.requirements)
    if (isDated(row.evidence) && row.evidence.sourceUrl !== pkg.sourceUrl)
      add('requirements', row.evidence.sourceUrl, row.evidence);
  for (const identity of pkg.facts.security?.identities ?? [])
    if (isDated(identity.evidence)) add('security', identity.evidence.sourceUrl, identity.evidence);
  for (const issue of pkg.facts.issues ?? [])
    if (isVerified(issue.evidence)) add('issue', issue.evidence.sourceUrl, issue.evidence);
  for (const integration of pkg.facts.integrations ?? [])
    if (isVerified(integration.evidence)) add('integration', integration.evidence.sourceUrl, integration.evidence);
  return out;
}

const DATE_MARK = '\ue002';

/**
 * A translated sentence split around its {date}, so the page can wrap the date in <time>. Messages
 * without the placeholder come back whole in before.
 */
export function aroundDate(
  t: Translate,
  key: string,
  named: Record<string, unknown> = {}
): { before: string; after: string } {
  const [before = '', ...rest] = t(key, { ...named, date: DATE_MARK }).split(DATE_MARK);
  return { before, after: rest.join('') };
}

/** Calendar date of an ISO timestamp, for datetime attributes. */
export function isoDate(value: string): string {
  return value.slice(0, 10);
}

/** A release has content only when it contains nonblank notes. */
export function releaseHasNotes(entry: import('@opennavo/api').ReleaseEntry): boolean {
  return Boolean(
    entry.summary?.trim() ||
    entry.bodyMarkdown?.trim() ||
    entry.sections.some(section => section.items.some(item => item.trim()))
  );
}

/** An allocated compatibility object can still contain no known requirements. */
function hasCompatibilityEvidence(value: import('@opennavo/api').ReleaseEntry['compatibility']): boolean {
  return Boolean(
    value &&
    (value.minimumMacOS?.trim() ||
      value.maximumMacOS?.trim() ||
      value.appleSilicon === 'yes' ||
      value.appleSilicon === 'no' ||
      value.requirements.some(row => row.minimumMacOS?.trim() || row.maximumMacOS?.trim()))
  );
}

/** Coalesce Homebrew's build record with the upstream base release on this page. */
export function mergeReleaseEntries(
  entries: readonly import('@opennavo/api').ReleaseEntry[]
): import('@opennavo/api').ReleaseEntry[] {
  const merged = new Map<string, import('@opennavo/api').ReleaseEntry>();
  for (const entry of entries) {
    const key = entry.version.split(',')[0]!.trim();
    const previous = merged.get(key);
    if (!previous) {
      merged.set(key, { ...entry, hasNotes: releaseHasNotes(entry) });
      continue;
    }
    const preferred = releaseHasNotes(previous) ? previous : entry;
    const other = preferred === previous ? entry : previous;
    merged.set(key, {
      ...preferred,
      version: key,
      hasNotes: releaseHasNotes(previous) || releaseHasNotes(entry),
      build: undefined,
      isLatest: previous.isLatest || entry.isLatest,
      publishedAt: preferred.publishedAt || other.publishedAt,
      brewCommittedAt: preferred.brewCommittedAt || other.brewCommittedAt,
      // Keep one evidence snapshot intact; do not let an unknown placeholder
      // override the other release's known bounds or architecture support.
      compatibility: hasCompatibilityEvidence(preferred.compatibility)
        ? preferred.compatibility
        : (other.compatibility ?? preferred.compatibility),
      summary: [...new Set([preferred.summary, other.summary].filter(Boolean))].join('\n\n') || null,
      bodyMarkdown: [...new Set([preferred.bodyMarkdown, other.bodyMarkdown].filter(Boolean))].join('\n\n') || null,
      sections: [
        ...preferred.sections,
        ...other.sections.filter(
          section => !preferred.sections.some(existing => JSON.stringify(existing) === JSON.stringify(section))
        )
      ]
    });
  }
  return [...merged.values()];
}

export function releaseCompatibilityText(
  value: import('@opennavo/api').ReleaseEntry['compatibility'],
  labels: { minimum: string; maximum: string; appleSilicon: { yes: string; no: string } }
): string {
  if (!value) return '';
  const bounds = (minimum?: string | null, maximum?: string | null) =>
    [
      minimum ? labels.minimum.replace('{version}', minimum) : '',
      maximum ? labels.maximum.replace('{version}', maximum) : ''
    ].filter(Boolean);
  const parts = bounds(value.minimumMacOS, value.maximumMacOS);
  if (!parts.length)
    parts.push(
      ...new Set(
        value.requirements.flatMap(row => {
          const range = bounds(row.minimumMacOS, row.maximumMacOS).join(', ');
          return range ? [`${row.architectures.length ? `${row.architectures.join(', ')}: ` : ''}${range}`] : [];
        })
      )
    );
  if (value.appleSilicon === 'yes' || value.appleSilicon === 'no') parts.push(labels.appleSilicon[value.appleSilicon]);
  return parts.join(' · ');
}

/** A direct Homebrew URL, with only checks backed by current, dated evidence. */
export function officialDownload(pkg: PackagePage) {
  if (pkg.kind !== 'cask' || disabledByHomebrew(pkg) || pkg.homebrewStatus?.state === 'disabled') return null;
  const choice = downloadChoice(pkg);
  if (choice?.kind !== 'direct') return null;
  const url = httpsUrl(choice.url);
  if (!url) return null;
  const rows = homebrewRows(pkg).filter(
    row => isVerified(row.evidence) && row.versions.includes(currentVersion(pkg)) && row.downloadUrl === url
  );
  const checksums = rows
    .map(row => row.sha256)
    .filter((sha): sha is string => Boolean(sha && /^[a-f0-9]{64}$/i.test(sha)));
  const checksum = checksums.length === rows.length && new Set(checksums.map(sha => sha.toLowerCase())).size === 1;
  return {
    url,
    host: new URL(url).hostname,
    checksum,
    checkedAt: oldestCheck(rows.map(row => row.evidence)),
    active: pkg.homebrewStatus?.state === 'active' || pkg.homebrewStatus?.state === 'deprecated'
  };
}

/** Subpages reference a complete, minimal application node without offers or ratings. */
export function subpageAppNode(pkg: PackagePage, id: string, url: string) {
  return {
    '@type': 'SoftwareApplication' as const,
    '@id': id,
    name: pkg.displayName,
    url,
    operatingSystem: 'macOS',
    softwareVersion: currentVersion(pkg)
  };
}

/** Matches the stable fragments used by release feeds and version cards. */
export function releaseAnchorId(entry: import('@opennavo/api').ReleaseEntry): string {
  return entry.id ? `r-${entry.id}` : `v-${entry.version.replace(/[^0-9A-Za-z._-]+/g, '-')}`;
}
