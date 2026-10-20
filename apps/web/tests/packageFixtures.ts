// Test fixtures for app pages: a detail with the facts the API derives from it (apps/server
// internal/facts/normalized.go and catalog/*_facts.go), so tests feed the web the same shape it gets in
// production. Evidence is verified unless a test says otherwise; the web never recomputes status.
import type { PackageDetail } from '@opennavo/api';
import { slimPackage } from '../app/utils/packageFacts';
import type {
  AppFacts,
  FactEvidence,
  PackagePage,
  PriceFact,
  RequirementFact,
  UninstallFact
} from '../app/utils/packageFacts';

export const CHECKED = '2025-12-01T08:00:00Z';
export const VALID_UNTIL = '2026-12-01T08:00:00Z';

export function evidence(sourceUrl: string, overrides: Partial<FactEvidence> = {}): FactEvidence {
  return { sourceUrl, checkedAt: CHECKED, validUntil: VALID_UNTIL, status: 'verified', ...overrides };
}

type Declaration = Record<string, unknown>;

/** Port of facts.Uninstall: explicit strings from uninstall and zap stanzas, deduplicated per phase. */
export function uninstallFrom(detail: PackageDetail, proof: FactEvidence): UninstallFact[] {
  const out: UninstallFact[] = [];
  const seen = new Set<string>();
  for (const entry of detail.artifacts.entries ?? []) {
    if (entry.phase !== 'uninstall' && entry.phase !== 'cleanup') continue;
    const phase = entry.phase === 'cleanup' ? 'zap' : 'uninstall';
    const add = (kind: UninstallFact['kind'], value: string) => {
      const key = `${phase}\u0000${kind}\u0000${value}`;
      if (!value || seen.has(key)) return;
      seen.add(key);
      out.push({ kind, value, phase, deletesUserData: phase === 'zap', evidence: proof });
    };
    const walk = (object: Declaration) => {
      for (const key of ['uninstall', 'zap']) {
        const nested = object[key];
        if (Array.isArray(nested))
          for (const item of nested)
            if (item && typeof item === 'object' && !Array.isArray(item)) walk(item as Declaration);
        if (nested && typeof nested === 'object' && !Array.isArray(nested)) walk(nested as Declaration);
      }
      for (const key of ['launchctl', 'login_item', 'pkgutil', 'trash', 'delete', 'rmdir']) {
        const kind = (['trash', 'delete', 'rmdir'].includes(key) ? 'zap' : key) as UninstallFact['kind'];
        const raw = object[key];
        const values =
          typeof raw === 'string' ? [raw] : Array.isArray(raw) ? raw.filter(v => typeof v === 'string') : [];
        for (const value of values) add(kind, value as string);
      }
    };
    walk(entry.declaration as Declaration);
  }
  return out;
}

/** Port of facts.Requirements plus the vendor supplement rows: identical declarations merge their labels. */
export function requirementsFrom(detail: PackageDetail, proof: FactEvidence): RequirementFact[] {
  const out: RequirementFact[] = [];
  const keys = new Map<string, number>();
  for (const platform of detail.platforms ?? []) {
    const rosetta = platform.requiresRosetta ?? detail.supports?.requiresRosetta ?? false;
    const row: RequirementFact = {
      versions: [platform.version],
      architectures: platform.arch ? [platform.arch] : [],
      platformMacOS: [],
      minimumMacOS: platform.minMacos ?? null,
      maximumMacOS: null,
      appleSilicon: rosetta && platform.arch === 'arm64' ? 'no' : 'unknown',
      downloadUrl: platform.downloadUrl ?? null,
      sha256: /^[a-f0-9]{64}$/i.test(platform.downloadSha256 ?? '') ? platform.downloadSha256!.toLowerCase() : null,
      evidence: proof
    };
    const key = JSON.stringify(row);
    const index = keys.get(key);
    if (index === undefined) {
      keys.set(key, out.length);
      out.push({ ...row, platformMacOS: platform.macos ? [platform.macos] : [] });
    } else if (platform.macos && !out[index]!.platformMacOS.includes(platform.macos)) {
      out[index]!.platformMacOS.push(platform.macos);
    }
  }
  const supplement = detail.macosSupplement;
  if (supplement) {
    const status =
      supplement.conflicts.length || supplement.appVersion !== detail.version ? 'needs_review' : 'verified';
    const vendor = evidence(supplement.sourceUrl, { checkedAt: supplement.verifiedAt, status });
    for (const range of supplement.ranges)
      out.push({
        versions: supplement.appVersion ? [supplement.appVersion] : [],
        architectures: [range.arch],
        platformMacOS: [],
        minimumMacOS: range.minimum,
        maximumMacOS: range.maximum,
        appleSilicon: range.arch === 'arm64' ? (range.support === 'native' ? 'yes' : 'no') : 'unknown',
        downloadUrl: null,
        sha256: null,
        evidence: vendor
      });
  }
  return out;
}

/** Port of catalog.priceFacts: the legacy price, then the Mac App Store listing's download price. */
export function pricesFrom(detail: PackageDetail): PriceFact[] {
  const out: PriceFact[] = [];
  const price = detail.price;
  if (price) {
    let period: PriceFact['period'] = 'unknown';
    if (price.period) period = ({ one_time: 'one_time', month: 'monthly', year: 'yearly' } as const)[price.period];
    if (price.type === 'free') period = 'free';
    out.push({
      region: price.region ?? 'ZZ',
      edition: price.plan ?? 'unspecified',
      period,
      usage: price.type,
      downloadFree: price.type === 'free' || price.type === 'free_with_iap',
      ...(price.amount != null ? { amount: String(price.amount) } : {}),
      ...(price.currency ? { currency: price.currency } : {}),
      purchaseUrl: price.sourceUrl,
      channel: 'vendor',
      evidence: evidence(price.sourceUrl, {
        checkedAt: price.verifiedAt,
        status: price.stale ? 'stale' : 'verified'
      })
    });
  }
  const store = detail.appStore;
  if (store)
    out.push({
      region: store.region,
      edition: 'App Store',
      period: 'unknown',
      usage: 'unknown',
      downloadFree: store.price === 0,
      amount: String(store.price),
      currency: store.currency,
      purchaseUrl: store.url,
      channel: 'app_store',
      evidence: evidence(store.url, { checkedAt: store.checkedAt })
    });
  return out;
}

/** Port of PriceFact.OfferReady. */
export function offersFrom(prices: PriceFact[]): PriceFact[] {
  return prices.filter(
    price =>
      price.evidence.status === 'verified' &&
      price.amount != null &&
      Boolean(price.currency) &&
      price.region !== 'ZZ' &&
      price.edition !== 'unspecified' &&
      ['free', 'paid', 'subscription'].includes(price.usage)
  );
}

/** The facts model the API sends for a detail, built the way the server builds it. */
export function factsFrom(detail: PackageDetail): AppFacts {
  const homebrew =
    detail.kind === 'link'
      ? evidence(detail.sourceUrl, { checkedAt: null, validUntil: null, status: 'unknown' })
      : evidence(detail.sourceUrl);
  const prices = pricesFrom(detail);
  return {
    requirements: requirementsFrom(detail, homebrew),
    uninstall: uninstallFrom(detail, homebrew),
    overview: {
      version: detail.version,
      downloadUrl: detail.downloadUrl ?? null,
      sha256: detail.downloadSha256 ?? null,
      artifacts: detail.artifacts,
      caveats: detail.caveats ?? null,
      aliases: [],
      dependencyCount: (detail.dependsOn?.casks.length ?? 0) + (detail.dependsOn?.formulae.length ?? 0),
      conflictCount: (detail.conflictsWith?.casks.length ?? 0) + (detail.conflictsWith?.formulae.length ?? 0)
    },
    // Reason and limit codes stay only when a verified attribute or price backs them (refreshFacts).
    alternatives: (detail.alternatives ?? []).map(item => ({
      ...item,
      reasons: [],
      limits: [],
      attributes: [],
      prices: []
    })),
    ...(detail.price ? { price: detail.price } : {}),
    ...(detail.macosSupplement ? { macosSupplement: detail.macosSupplement } : {}),
    prices,
    offers: offersFrom(prices),
    security: {
      identities: detail.openSource
        ? [
            {
              channels: [],
              openSource: 'yes',
              repositoryUrl: detail.openSource.url,
              evidence: evidence(detail.openSource.url, { checkedAt: detail.openSource.checkedAt })
            }
          ]
        : [],
      vulnerabilities: [],
      scanUrl: `opennavo://scan/${detail.token}?preview=1`
    },
    issues: [],
    integrations: []
  };
}

/** A detail as the app pages hold it: facts derived like the API's (unless given), then slimmed. */
export function pageOf(detail: PackageDetail, facts?: Partial<AppFacts>): PackagePage {
  return slimPackage({ ...detail, facts: { ...factsFrom(detail), ...facts } });
}
