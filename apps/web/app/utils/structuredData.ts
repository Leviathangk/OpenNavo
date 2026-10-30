// Structured data (WP10, T2), pure for testing. Nodes only repeat what the page shows, read from the
// same facts model as the page's blocks: the version, the system requirements and chips of the
// verified requirement rows, the download the install guide links, and offers only from the API's
// facts.offers (verified, current, with an amount, currency, region and edition), so a price type or
// a free download alone never becomes an offer. There are no reviews or ratings, so app pages are not
// eligible for Google's software rich result; that gap is accepted rather than filled with install counts.
import { allPackageAliases, BRAND, formatBytes } from '@opennavo/shared';
import type { SiteLocale as Locale } from '@opennavo/shared';
import { applicationCategory } from './packageView';
import { httpsUrl, officialDownloadUrl } from './linkApp';
import { chipsOf, currentVersion, downloadChoice, macosRange, macosText } from './packageFacts';
import type { PackagePage, PriceFact } from './packageFacts';
import { reliableVersion } from './seoTitles';
import type { Translate } from './seoTitles';

/** @id of OpenNavo's own desktop app, the SoftwareApplication /download describes. */
export function desktopAppId(siteUrl: string): string {
  return `${siteUrl}/#desktop-app`;
}

/**
 * Stable @id of an app entity: the same in every language and on every page that names the app, so
 * articles can point at it in about and mentions. OpenNavo's own catalog entry is the desktop app, whose
 * overview answers a 301 to /download, so its pages point at the node /download defines.
 */
export function appEntityId(siteUrl: string, token: string): string {
  return token === BRAND.caskToken ? desktopAppId(siteUrl) : `${siteUrl}/apps/${token}#app`;
}

const UNIT_CODES: Partial<Record<PriceFact['period'], string>> = { monthly: 'MON', yearly: 'ANN' };

/** One Offer per price the API marks offer-ready, as the price block lists it. */
export function appOffers(offers: readonly PriceFact[] | undefined): Record<string, unknown>[] {
  return (offers ?? []).flatMap(offer => {
    const price = Number(offer.amount);
    if (!offer.currency || !Number.isFinite(price)) return [];
    const node: Record<string, unknown> = {
      price,
      priceCurrency: offer.currency,
      url: offer.purchaseUrl,
      name: offer.edition,
      eligibleRegion: { '@type': 'Country', name: offer.region }
    };
    // The resolver otherwise invents a validity date; ours ends with the evidence.
    if (offer.evidence.validUntil) node.priceValidUntil = offer.evidence.validUntil.slice(0, 10);
    const unitCode = offer.usage === 'subscription' ? UNIT_CODES[offer.period] : undefined;
    if (unitCode)
      node.priceSpecification = {
        '@type': 'UnitPriceSpecification',
        price,
        priceCurrency: offer.currency,
        referenceQuantity: { '@type': 'QuantitativeValue', value: 1, unitCode }
      };
    return [node];
  });
}

/**
 * SoftwareApplication input for an app page, or null for fonts: a typeface is not software, and
 * forcing the type would claim a rich result the page cannot support.
 */
export function softwareAppNode(
  pkg: PackagePage,
  url: string,
  t: Translate,
  locale: Locale = 'en-US'
): Record<string, unknown> | null {
  if (pkg.isFont) return null;
  const node: Record<string, unknown> = {
    name: pkg.displayName,
    operatingSystem: 'macOS',
    applicationCategory: applicationCategory(
      pkg.primaryCategory?.slug,
      (pkg.categories ?? []).map(category => category.slug)
    ),
    url
  };
  const alternateNames = allPackageAliases(pkg.names, pkg.displayName);
  if (alternateNames.length) node.alternateName = alternateNames;
  const version = reliableVersion(currentVersion(pkg));
  if (version) node.softwareVersion = version;
  const homepage = pkg.homepage ?? httpsUrl(pkg.link?.homepage);
  if (homepage) node.sameAs = homepage;
  if (pkg.summary) node.description = pkg.summary;
  if (pkg.iconUrl) node.image = pkg.iconUrl;
  if (pkg.screenshots.length) node.screenshot = pkg.screenshots.map(shot => shot.url);
  // The range the facts table states for the current version, in the page's words.
  const requirements = macosText(macosRange(pkg), t);
  if (requirements) node.softwareRequirements = requirements;
  const chips = chipsOf(pkg);
  if (chips.length) node.processorRequirements = chips.join(', ');
  if (pkg.kind === 'link') {
    // Link apps (store/security plan §4): only the developer's own site or the app's GitHub repository.
    const official = pkg.link ? officialDownloadUrl(pkg.link) : undefined;
    if (official) node.downloadUrl = official;
  } else {
    const download = downloadChoice(pkg);
    if (download?.kind === 'direct') node.downloadUrl = download.url;
    node.identifier = { '@type': 'PropertyValue', propertyID: 'Homebrew cask', value: pkg.token };
    if (pkg.downloadSize) node.fileSize = formatBytes(pkg.downloadSize, { locale });
  }
  if (pkg.releaseCount > 0) node.releaseNotes = `${url}/versions`;
  const offers = appOffers(pkg.facts.offers);
  if (offers.length) node.offers = offers.length === 1 ? offers[0] : offers;
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
