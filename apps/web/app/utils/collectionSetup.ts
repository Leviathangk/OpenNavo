// Collection setup (F4), pure for testing: what "Install all" installs, which apps come from their developers'
// sites instead, price totals exactly as the API groups them, and each app's sourced macOS compatibility.
// Nothing here adds, converts or merges what the server keeps apart (currencies, billing periods, editions).
import type { PublicComponents } from '@opennavo/api';
import type { SiteLocale } from '@opennavo/shared';

export type CollectionDetail = PublicComponents['schemas']['CollectionDetail'];
export type CollectionSetup = PublicComponents['schemas']['CollectionSetup'];
export type CollectionPriceTotal = PublicComponents['schemas']['CollectionPriceTotal'];
type RequirementFact = PublicComponents['schemas']['RequirementFact'];
type CollectionItem = PublicComponents['schemas']['CollectionItem'];

export const PRICE_PERIODS = ['free', 'one_time', 'monthly', 'yearly', 'unknown'] as const;
export type PricePeriod = (typeof PRICE_PERIODS)[number];

/** A billing period the page has words for; anything else reads as unknown rather than being guessed. */
export function pricePeriod(value: string): PricePeriod {
  return (PRICE_PERIODS as readonly string[]).includes(value) ? (value as PricePeriod) : 'unknown';
}

/** A total in its own currency (never converted); the plain amount and code when the code is not ISO 4217. */
export function formatTotal(total: Pick<CollectionPriceTotal, 'amount' | 'currency'>, locale: SiteLocale): string {
  const amount = Number(total.amount);
  if (!Number.isFinite(amount)) return `${total.amount} ${total.currency}`;
  try {
    return new Intl.NumberFormat(locale, { style: 'currency', currency: total.currency }).format(amount);
  } catch {
    return `${total.amount} ${total.currency}`;
  }
}

/** A region code in the page language (US → United States); the code itself when unknown to the runtime. */
export function regionName(region: string, locale: SiteLocale): string {
  try {
    return new Intl.DisplayNames([locale], { type: 'region' }).of(region.toUpperCase()) ?? region;
  } catch {
    return region;
  }
}

export interface SetupEntry {
  token: string;
  name: string;
  /** The app's OpenNavo page path, without a language prefix. */
  path: string;
}

export interface ExternalEntry extends SetupEntry {
  /** The developer's site the API names for this link app. */
  url: string;
}

const entryFor = (items: readonly CollectionItem[], token: string): SetupEntry => {
  const item = items.find(candidate => candidate.package.token === token);
  return { token, name: item?.package.displayName ?? token, path: `/apps/${token}` };
};

/** Apps "Install all" and the Brewfile cover, in the API's order: exactly setup.installableTokens. */
export function installableEntries(detail: Pick<CollectionDetail, 'items' | 'setup'>): SetupEntry[] {
  return (detail.setup?.installableTokens ?? []).map(token => entryFor(detail.items, token));
}

/** Link apps, listed on their own with their official sites; they never enter the Brewfile. */
export function externalEntries(detail: Pick<CollectionDetail, 'items' | 'setup'>): ExternalEntry[] {
  return (detail.setup?.externalApps ?? []).map(app => ({ ...entryFor(detail.items, app.token), url: app.url }));
}

export type AppleSilicon = 'yes' | 'no' | 'unknown';

export interface CompatibilityRow extends SetupEntry {
  /** macOS bounds per requirement, already worded; empty when no source states one. */
  macos: string[];
  appleSilicon: AppleSilicon;
}

export interface BoundLabels {
  /** "macOS {version} or later". */
  minimum: string;
  /** "up to macOS {version}". */
  maximum: string;
}

const fill = (template: string, version: string) => template.replace('{version}', version);

function bounds(requirement: RequirementFact, labels: BoundLabels): string {
  return [
    ...(requirement.minimumMacOS ? [fill(labels.minimum, requirement.minimumMacOS)] : []),
    ...(requirement.maximumMacOS ? [fill(labels.maximum, requirement.maximumMacOS)] : [])
  ].join(', ');
}

/**
 * One row per collection app, in the page's order: the macOS bounds its requirements state (architectures
 * named only when they differ, never merged into one range) and Apple silicon support: yes when a
 * requirement says it runs natively, no when they only say it does not, else unknown.
 */
export function compatibilityRows(
  detail: Pick<CollectionDetail, 'items' | 'setup'>,
  labels: BoundLabels
): CompatibilityRow[] {
  const byToken = new Map((detail.setup?.compatibility ?? []).map(entry => [entry.token, entry.requirements]));
  return detail.items.map(item => {
    const token = item.package.token;
    const requirements = byToken.get(token) ?? [];
    const worded = requirements.flatMap(requirement => {
      const range = bounds(requirement, labels);
      return range ? [{ arch: requirement.architectures.join(', '), range }] : [];
    });
    const distinct = new Set(worded.map(row => row.range));
    const macos =
      distinct.size > 1
        ? [...new Set(worded.map(row => (row.arch ? `${row.arch}: ${row.range}` : row.range)))]
        : [...distinct];
    const support = requirements.map(requirement => requirement.appleSilicon);
    const appleSilicon: AppleSilicon = support.includes('yes') ? 'yes' : support.includes('no') ? 'no' : 'unknown';
    return { ...entryFor(detail.items, token), macos, appleSilicon };
  });
}
