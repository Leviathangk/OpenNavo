// "Alternatives to {app}" intent page (X2, F3), pure for testing. The page exists only where the server's
// decision opens it (seo pages.alternatives: indexable, its own canonical), so each app × intent × language
// has one target page; everywhere else it is a 404 and the app page keeps its Alternatives section.
import type { PublicComponents } from '@opennavo/api';
import type { SiteLocale } from '@opennavo/shared';

type PackageDetail = PublicComponents['schemas']['PackageDetail'];
type PackageAlternative = PublicComponents['schemas']['PackageAlternative'];
type ComparisonAttributeFact = PublicComponents['schemas']['ComparisonAttributeFact'];
type PriceFact = PublicComponents['schemas']['PriceFact'];

/** Confirmed alternatives with their verified comparison facts (facts.alternatives), else the plain list. */
export function alternativesOf(pkg: Pick<PackageDetail, 'facts' | 'alternatives'>): PackageAlternative[] {
  const facts = pkg.facts?.alternatives ?? [];
  return facts.length ? facts : (pkg.alternatives ?? []);
}

/** Whether the server opens the intent page for this app in this language; the page is a 404 otherwise. */
export function alternativesPageOpen(pkg: Pick<PackageDetail, 'facts' | 'alternatives' | 'indexing'>): boolean {
  const decision = pkg.indexing?.pages?.alternatives;
  return Boolean(
    decision && decision.robots === 'index, follow' && !decision.canonicalPath && alternativesOf(pkg).length
  );
}

/** Attributes the comparison can show, in table order; only verified facts reach the page (F3). */
export const COMPARISON_ATTRIBUTES = [
  'open_source',
  'offline',
  'apple_silicon',
  'import_formats',
  'export_formats'
] as const satisfies readonly ComparisonAttributeFact['name'][];
export type ComparisonAttribute = (typeof COMPARISON_ATTRIBUTES)[number];

export type ComparisonValue =
  | { kind: 'yes' }
  | { kind: 'no' }
  | { kind: 'formats'; formats: string[] }
  | { kind: 'unknown' };

/** One alternative's value for an attribute, from its verified fact; unknown without one. */
export function attributeValue(
  alternative: Pick<PackageAlternative, 'attributes'>,
  name: ComparisonAttribute
): { value: ComparisonValue; fact?: ComparisonAttributeFact } {
  const fact = alternative.attributes?.find(item => item.name === name);
  if (!fact) return { value: { kind: 'unknown' } };
  if (fact.formats?.length) return { value: { kind: 'formats', formats: fact.formats }, fact };
  if (typeof fact.booleanValue === 'boolean') return { value: { kind: fact.booleanValue ? 'yes' : 'no' }, fact };
  return { value: { kind: 'unknown' }, fact };
}

/** Comparison rows: attributes at least one alternative has a verified fact for. */
export function comparisonAttributes(
  alternatives: readonly Pick<PackageAlternative, 'attributes'>[]
): ComparisonAttribute[] {
  return COMPARISON_ATTRIBUTES.filter(name =>
    alternatives.some(item => item.attributes?.some(fact => fact.name === name))
  );
}

/**
 * A price fact in words: free use, or the amount in its own currency with its billing period; null when the
 * fact has neither (the page then says nothing about the price).
 */
export function priceText(
  price: PriceFact,
  locale: SiteLocale,
  t: (key: string, params?: Record<string, string>) => string
): string | null {
  if (price.usage === 'free') return t('package.facts.types.free');
  if (!price.amount || !price.currency)
    return price.usage === 'unknown' ? null : t(`package.facts.types.${price.usage}`);
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
