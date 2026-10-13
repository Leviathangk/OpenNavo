// Structured data (WP10), pure for testing. Nodes only repeat what the page shows: version,
// requirements, architectures and screenshots match the visible sections. Offers follow the API's
// offer flag (verified, current, and free or a complete amount), so a price type alone never
// becomes a price. There are no reviews or ratings, so app pages are not eligible for Google's
// software rich result; that gap is accepted rather than filled with install counts.
import type { PackageDetail } from '@opennavo/api';
import { applicationCategory } from './packageView';
import { downloadChoice } from './appAnswer';
import { reliableVersion } from './seoTitles';
import type { Translate } from './seoTitles';

const DAY = 24 * 60 * 60 * 1000;
/** Matches the server's evidence window: older prices stop being offered. */
const EVIDENCE_DAYS = 365;

/**
 * Stable @id of an app entity: the same in every language and on every page that names the app, so
 * articles can point at it in about and mentions.
 */
export function appEntityId(siteUrl: string, token: string): string {
  return `${siteUrl}/apps/${token}#app`;
}

/** Offer for a verified price; null when structured data may not state one. */
export function appOffer(price: PackageDetail['price'], t: Translate): Record<string, unknown> | null {
  if (!price?.offer) return null;
  const free = price.type === 'free' || price.type === 'free_with_iap';
  // The resolver otherwise invents a validity date; ours ends when the evidence goes stale.
  const validUntil = new Date(Date.parse(price.verifiedAt) + EVIDENCE_DAYS * DAY).toISOString().slice(0, 10);
  const offer: Record<string, unknown> = {
    price: free ? 0 : price.amount,
    url: price.sourceUrl,
    priceValidUntil: validUntil
  };
  if (price.currency) offer.priceCurrency = price.currency;
  if (price.type === 'free_with_iap') offer.description = t('package.facts.types.free_with_iap');
  if (!free) {
    offer.name = price.plan;
    offer.eligibleRegion = { '@type': 'Country', name: price.region };
    if (price.type === 'subscription' && (price.period === 'month' || price.period === 'year'))
      offer.priceSpecification = {
        '@type': 'UnitPriceSpecification',
        price: price.amount,
        priceCurrency: price.currency,
        referenceQuantity: {
          '@type': 'QuantitativeValue',
          value: 1,
          unitCode: price.period === 'month' ? 'MON' : 'ANN'
        }
      };
  }
  return offer;
}

/**
 * SoftwareApplication input for an app page, or null for fonts: a typeface is not software, and
 * forcing the type would claim a rich result the page cannot support.
 */
export function softwareAppNode(pkg: PackageDetail, url: string, t: Translate): Record<string, unknown> | null {
  if (pkg.isFont) return null;
  const node: Record<string, unknown> = {
    name: pkg.displayName,
    operatingSystem: 'macOS',
    applicationCategory: applicationCategory(pkg.primaryCategory?.slug),
    url
  };
  const alternateNames = pkg.names.filter(name => name !== pkg.displayName);
  if (alternateNames.length) node.alternateName = alternateNames;
  const version = reliableVersion(pkg.version);
  if (version) node.softwareVersion = version;
  if (pkg.homepage) node.sameAs = pkg.homepage;
  if (pkg.summary) node.description = pkg.summary;
  if (pkg.iconUrl) node.image = pkg.iconUrl;
  if (pkg.screenshots.length) node.screenshot = pkg.screenshots.map(shot => shot.url);
  // Homebrew's full constraint, as the requirements row shows it.
  if (pkg.dependsOn?.macos) node.softwareRequirements = `macOS ${pkg.dependsOn.macos}`;
  if (pkg.supports.status === 'known') {
    const archs = (['arm64', 'x86_64'] as const).filter(arch => pkg.supports[arch]);
    if (archs.length) node.processorRequirements = archs.join(', ');
  }
  const download = downloadChoice(pkg);
  if (download?.kind === 'direct') node.downloadUrl = download.url;
  const offer = appOffer(pkg.price, t);
  if (offer) node.offers = offer;
  return node;
}

export interface ListEntry {
  name: string;
  url: string;
}

/** ListItems in page order; start is the position of the first entry (pages after the first continue). */
export function listItems(entries: ListEntry[], start = 1): Record<string, unknown>[] {
  return entries.map((entry, index) => ({ position: start + index, name: entry.name, url: entry.url }));
}
