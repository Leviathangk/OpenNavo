// G5-01..30 mirror docs/design/seo-batch/g5-samples.md. These are normalized public API
// payloads, not live-app claims or a second implementation of the backend's expiry/CVE policy.
import type { PackageDetail, PublicComponents } from '@opennavo/api';
import { SITE_LOCALES, type SiteLocale } from '@opennavo/shared';
import type { AppFacts, FactEvidence, PriceFact, RequirementFact, UninstallFact } from '../app/utils/packageFacts';
import { factsFrom, offersFrom } from './packageFixtures';

export const GEO_NOW = '2026-10-20T00:00:00Z';
export const GEO_CHECKED = '2026-10-19T00:00:00Z';
export const GEO_HASH = '0123456789abcdef'.repeat(4);
export const geoEvidence = (id: number, overrides: Partial<FactEvidence> = {}): FactEvidence => ({
  sourceUrl: `https://evidence.example/geo/${id}`,
  checkedAt: GEO_CHECKED,
  validUntil: '2026-11-18T00:00:00Z',
  status: 'verified',
  ...overrides
});
export const geoPrice = (id: number, overrides: Partial<PriceFact> = {}): PriceFact => ({
  region: 'US',
  edition: 'Pro',
  period: 'monthly',
  usage: 'subscription',
  downloadFree: false,
  amount: '9.99',
  currency: 'USD',
  purchaseUrl: `https://vendor.example/geo/${id}/buy`,
  channel: 'vendor',
  evidence: geoEvidence(id),
  ...overrides
});
export const geoRequirement = (id: number, overrides: Partial<RequirementFact> = {}): RequirementFact => ({
  versions: ['2.0.0'],
  architectures: ['arm64'],
  platformMacOS: [],
  minimumMacOS: '13',
  maximumMacOS: null,
  appleSilicon: 'unknown',
  downloadUrl: `https://vendor.example/geo/${id}/arm.dmg`,
  sha256: GEO_HASH,
  evidence: geoEvidence(id),
  ...overrides
});
export const geoUninstall = (id: number, overrides: Partial<UninstallFact> = {}): UninstallFact => ({
  kind: 'zap',
  value: `~/Library/Preferences/example.geo${id}.plist`,
  phase: 'zap',
  deletesUserData: true,
  evidence: geoEvidence(id),
  ...overrides
});

export function geoDetail(id: number): PackageDetail {
  const token = `geo-fixture-${String(id).padStart(2, '0')}`;
  const detail = {
    kind: 'cask',
    token,
    name: `Fixture ${id}`,
    displayName: `Fixture ${id}`,
    names: [`Fixture ${id}`],
    summary: `Synthetic app ${id}.`,
    description: '',
    version: '2.0.0',
    developer: 'Example Studio',
    isFont: false,
    disabled: false,
    autoUpdates: false,
    tap: 'homebrew/cask',
    sourceUrl: `https://evidence.example/geo/${id}/homebrew`,
    formulaeUrl: `https://formulae.brew.sh/cask/${token}`,
    homepage: `https://vendor.example/geo/${id}`,
    installCommand: `brew install --cask ${token}`,
    installs: { d30: 0, d90: 0, d365: 0 },
    supports: { status: 'unknown', arm64: false, x86_64: false },
    conflictsWith: { casks: [], formulae: [] },
    artifacts: { apps: [`Fixture ${id}.app`], binaries: [], pkgs: [], entries: [], uninstallNotes: [] },
    categories: [],
    platforms: [],
    screenshots: [],
    releaseCount: 0,
    summaryTranslation: { status: 'source', locale: 'en-US' },
    sourceLocale: 'en-US',
    machineTranslated: false,
    indexing: {
      indexable: true,
      reason: null,
      locales: [...SITE_LOCALES],
      enforced: true,
      notesCount: 0,
      pages: {
        overview: {
          robots: 'index, follow',
          canonicalPath: `/apps/${token}`,
          hreflang: [...SITE_LOCALES],
          inSitemap: true,
          announce: true,
          reason: null
        }
      }
    }
  } as PackageDetail;
  detail.facts = factsFrom(detail);
  return detail;
}
export interface GeoSample {
  id: string;
  label: string;
  detail: PackageDetail;
  locale: SiteLocale;
  variants: { label: string; detail: PackageDetail; locale?: SiteLocale; now?: string }[];
  background?: PublicComponents['schemas']['BackgroundItem'];
}
const labels = [
  'fixed cask identity',
  'link without version',
  'font',
  'latest',
  'discontinued but available',
  'renamed',
  'withdrawn versus timeout',
  'monthly',
  'yearly plans',
  'one-time v2',
  'verified free',
  'IAP versus store download',
  'limited trial',
  'regional currencies',
  'expiry boundary',
  'identity pending',
  'security unknown',
  'verified identity',
  'exact CVE projection',
  'platform ranges',
  'pending conflict',
  'historical requirements',
  'unchanged successful sync',
  'expired Homebrew sync',
  'optional zap',
  'shared label candidates',
  'alternatives and migration',
  'full values and caveats',
  'published translation snapshot',
  'ten languages and withheld'
];
export const GEO_SAMPLES: GeoSample[] = labels.map((label, index) => ({
  id: `G5-${String(index + 1).padStart(2, '0')}`,
  label,
  detail: geoDetail(index + 1),
  locale: 'en-US',
  variants: []
}));
const sample = (id: number) => GEO_SAMPLES[id - 1]!;
const detail = (id: number) => sample(id).detail;
const facts = (id: number) => detail(id).facts!;
const prices = (id: number, rows: PriceFact[]) => {
  facts(id).prices = rows;
  facts(id).offers = offersFrom(rows);
};
const variant = (
  id: number,
  label: string,
  edit: (detail: PackageDetail) => void,
  locale?: SiteLocale,
  now?: string
) => {
  const copy = structuredClone(detail(id));
  edit(copy);
  sample(id).variants.push({ label, detail: copy, locale, now });
};
const requirements = (id: number, rows: RequirementFact[]) => {
  facts(id).requirements = rows;
};

requirements(1, [geoRequirement(1, { evidence: geoEvidence(1, { sourceUrl: detail(1).sourceUrl }) })]);
Object.assign(detail(2), {
  kind: 'link',
  version: '',
  sourceUrl: '',
  installCommand: '',
  link: {
    homepage: 'https://vendor.example/link',
    downloadUrl: 'https://vendor.example/link/download',
    pricing: 'unknown',
    bundleIds: []
  }
});
facts(2).overview.version = '';
detail(3).isFont = true;
facts(3).overview.artifacts.apps = [];
facts(3).overview.artifacts.entries = [
  { type: 'font', phase: 'install', sources: ['Fixture.ttf'], declaration: { font: 'Fixture.ttf' } }
];
detail(4).version = 'latest';
facts(4).overview.version = 'latest';
requirements(4, [geoRequirement(4, { versions: ['latest'] })]);
detail(5).recommendationPaused = true;
detail(5).homebrewStatus = { state: 'deprecated', reason: 'discontinued', date: '2026-10-19' };
detail(5).deprecation = { date: '2026-10-19', reason: 'discontinued' };
requirements(5, [geoRequirement(5)]);
detail(6).names.push('Former Fixture');
facts(6).overview.aliases = ['geo-fixture-old'];
// Withdrawn is an API 404 in the route test. This data exists only to prove the route never renders it.
detail(7).indexing!.indexable = false;
detail(7).indexing!.reason = 'hidden';
detail(7).indexing!.pages.overview = {
  robots: 'noindex, follow',
  canonicalPath: '/apps/geo-fixture-07',
  hreflang: [],
  inSitemap: false,
  announce: false,
  reason: 'hidden'
};
variant(7, 'timeout keeps public entity', p => {
  p.indexing = structuredClone(detail(1).indexing);
  p.indexing!.pages.overview.canonicalPath = '/apps/geo-fixture-07';
});
prices(8, [geoPrice(8)]);
prices(9, [
  geoPrice(9, { period: 'yearly', amount: '99.00' }),
  geoPrice(9, {
    edition: 'Team',
    period: 'yearly',
    amount: '199.00',
    evidence: geoEvidence(9, { sourceUrl: 'https://evidence.example/geo/9/team' })
  })
]);
prices(10, [geoPrice(10, { edition: 'Standard v2', period: 'one_time', usage: 'paid', amount: '49.00' })]);
prices(11, [geoPrice(11, { edition: 'Standard', period: 'free', usage: 'free', downloadFree: true, amount: '0' })]);
prices(12, [
  geoPrice(12, {
    usage: 'free_with_iap',
    period: 'unknown',
    downloadFree: true,
    amount: undefined,
    currency: undefined
  })
]);
variant(12, 'store download only', p => {
  p.facts!.prices = [
    geoPrice(12, {
      channel: 'app_store',
      edition: 'App Store',
      usage: 'unknown',
      period: 'unknown',
      amount: '0',
      downloadFree: true
    })
  ];
});
prices(13, [
  geoPrice(13, {
    usage: 'trial',
    edition: '14-day trial',
    period: 'unknown',
    downloadFree: true,
    amount: undefined,
    currency: undefined
  })
]);
prices(14, [geoPrice(14), geoPrice(14, { region: 'JP', currency: 'JPY', amount: '1200' })]);
sample(14).locale = 'zh-CN';
prices(15, [geoPrice(15, { evidence: geoEvidence(15, { validUntil: GEO_NOW }) })]);
variant(
  15,
  'at expiry normalized stale',
  p => {
    p.facts!.prices[0]!.evidence.status = 'stale';
    p.facts!.offers = [];
  },
  undefined,
  GEO_NOW
);
const identity = (id: number, status: FactEvidence['status'] = 'verified') => ({
  appVersion: '2.0.0',
  developer: 'Example Studio',
  teamId: 'EXAMPLE123',
  channels: ['vendor' as const],
  openSource: 'yes' as const,
  repositoryUrl: 'https://github.com/example/fixture',
  evidence: geoEvidence(id, { status })
});
facts(16).security!.identities = [identity(16, 'needs_review')];
variant(17, 'missing date', p => {
  p.facts!.security!.identities = [
    { ...identity(17), evidence: geoEvidence(17, { checkedAt: null, status: 'unknown' }) }
  ];
});
facts(18).security!.identities = [identity(18)];
detail(18).openSource = {
  url: 'https://github.com/example/fixture',
  repo: 'example/fixture',
  license: 'MIT',
  archived: false,
  checkedAt: GEO_CHECKED
} as PackageDetail['openSource'];
facts(19).security!.vulnerabilities = [
  {
    id: 'CVE-2026-99991',
    appVersion: '2.0.0',
    productCpe: 'cpe:2.3:a:example:fixture:2.0.0:*:*:*:*:*:*:*',
    sourceUrl: 'https://evidence.example/geo/19/cve',
    evidence: geoEvidence(19)
  }
];
variant(19, 'other product, old, wildcard, rejected or expired are absent from API projection', p => {
  p.facts!.security!.vulnerabilities = [];
});
requirements(20, [
  geoRequirement(20, {
    maximumMacOS: '15',
    appleSilicon: 'no',
    evidence: geoEvidence(20, { sourceUrl: detail(20).sourceUrl })
  }),
  geoRequirement(20, {
    architectures: ['x86_64'],
    minimumMacOS: '12',
    maximumMacOS: '14',
    downloadUrl: 'https://vendor.example/intel.dmg',
    sha256: 'a'.repeat(64),
    evidence: geoEvidence(20, { sourceUrl: detail(20).sourceUrl })
  })
]);
detail(20).supports = { status: 'known', arm64: true, x86_64: true, requiresRosetta: true };
variant(20, 'arm installation is not proof of native', p => {
  p.facts!.requirements[0]!.appleSilicon = 'unknown';
  p.supports.requiresRosetta = false;
});
requirements(21, [
  geoRequirement(21),
  geoRequirement(21, {
    minimumMacOS: '12',
    evidence: geoEvidence(21, { sourceUrl: 'https://vendor.example/conflict', status: 'needs_review' })
  })
]);
variant(21, 'no requirements', p => {
  p.facts!.requirements = [];
});
requirements(22, [geoRequirement(22)]);
variant(22, 'old version unknown', p => {
  p.version = '1.0.0';
  p.facts!.overview.version = '1.0.0';
  p.facts!.requirements = [];
});
variant(22, 'old version historical only', p => {
  p.version = '1.0.0';
  p.facts!.overview.version = '1.0.0';
  p.facts!.requirements = [
    geoRequirement(22, { versions: ['1.0.0'], minimumMacOS: '11', evidence: geoEvidence(22, { status: 'stale' }) })
  ];
});
requirements(23, [geoRequirement(23, { evidence: geoEvidence(23, { sourceUrl: detail(23).sourceUrl }) })]);
facts(23).uninstall = [geoUninstall(23)];
detail(23).updatedAt = '2020-01-01T00:00:00Z';
requirements(24, [
  geoRequirement(24, {
    evidence: geoEvidence(24, {
      checkedAt: '2025-01-01T00:00:00Z',
      validUntil: '2025-02-01T00:00:00Z',
      status: 'stale'
    })
  })
]);
facts(24).uninstall = [geoUninstall(24, { evidence: facts(24).requirements[0]!.evidence })];
prices(24, [geoPrice(24)]);
facts(25).uninstall = [
  geoUninstall(25, { phase: 'uninstall', kind: 'launchctl', value: 'com.example.shared', deletesUserData: false }),
  geoUninstall(25, { phase: 'uninstall', kind: 'pkgutil', value: 'com.example.installer', deletesUserData: false }),
  geoUninstall(25),
  geoUninstall(25, { value: '~/Library/Application Support/Fixture' })
];
facts(26).uninstall = [
  geoUninstall(26, { phase: 'uninstall', kind: 'launchctl', value: 'com.example.shared', deletesUserData: false })
];
sample(26).background = {
  label: 'com.example.shared',
  apps: [],
  developers: ['Example Studio', 'Other Studio'],
  locales: ['en-US'],
  shared: true,
  ownership: 'declared_candidates',
  identityChecks: ['signature', 'path', 'version'],
  content: {
    locale: 'en-US',
    description: 'Shared declaration; verify signature, path and version.',
    itemType: 'launch_agent',
    commonPaths: [],
    evidence: geoEvidence(26)
  },
  indexable: true,
  startupUrl: 'opennavo://startup/com.example.shared?preview=1'
};
sample(26).background!.apps = [26, 126].map((id, index) => ({
  package: {
    kind: 'cask',
    token: `geo-owner-${id}`,
    displayName: `Owner ${id}`,
    name: `Owner ${id}`
  } as PublicComponents['schemas']['BackgroundOwner']['package'],
  kind: 'launchctl',
  phase: 'uninstall',
  developer: index ? 'Other Studio' : 'Example Studio',
  evidence: geoEvidence(26),
  deletesUserData: false
}));
facts(27).alternatives = [
  {
    package: {
      kind: 'cask',
      token: 'geo-target',
      displayName: 'Target',
      name: 'Target'
    } as AppFacts['alternatives'][number]['package'],
    reasons: ['offline'],
    limits: [],
    bestFor: null,
    attributes: [
      { name: 'offline', booleanValue: true, evidence: geoEvidence(27) },
      { name: 'export_formats', formats: ['JSON'], evidence: geoEvidence(27) },
      { name: 'open_source', booleanValue: true, evidence: geoEvidence(27, { status: 'stale' }) }
    ],
    prices: [],
    migration: {
      targetPackageId: 27,
      locale: 'en-US',
      sourceVersion: '2.0.0',
      targetVersion: '3.0.0',
      evidence: geoEvidence(27),
      steps: (['backup', 'export', 'import', 'verify'] as const).map(action => ({
        action,
        text: `${action} JSON from 2.0.0 to 3.0.0`,
        evidence: geoEvidence(27, { sourceUrl: `https://evidence.example/geo/27/${action}` })
      }))
    }
  }
];
facts(28).overview.sha256 = GEO_HASH;
facts(28).overview.downloadUrl = 'https://vendor.example/full-download.dmg';
facts(28).overview.caveats = 'Keep <data> and run `example --check` only after backup.';
facts(28).overview.aliases = ['old-fixture'];
facts(28).overview.artifacts.binaries = ['fixture-cli'];
variant(28, 'no_check normalizes to null', p => {
  p.facts!.overview.sha256 = null;
});
sample(29).locale = 'fr-FR';
detail(29).summary = 'Instantané publié H1';
detail(29).description = 'Corps publié H1';
detail(29).machineTranslated = true;
detail(29).sourceLocale = 'en-US';
variant(29, 'manual published snapshot', p => {
  p.machineTranslated = false;
  p.description = 'Texte humain H1';
});
prices(30, [geoPrice(30)]);
requirements(30, [geoRequirement(30)]);
for (const locale of SITE_LOCALES)
  variant(
    30,
    `ready ${locale}`,
    p => {
      p.summary = `Synthetic ${locale} snapshot`;
      p.indexing!.pages.overview.canonicalPath = `/apps/${p.token}`;
    },
    locale
  );
for (const locale of ['de-DE', 'zh-Hant'] as const)
  variant(
    30,
    `withheld ${locale}`,
    p => {
      p.indexing!.locales = ['en-US'];
      p.indexing!.pages.overview.hreflang = ['en-US'];
      p.indexing!.pages.overview.robots = 'noindex, follow';
      p.indexing!.pages.overview.inSitemap = false;
    },
    locale
  );
