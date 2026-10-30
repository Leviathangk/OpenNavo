import type { PackageDetail } from '@opennavo/api';
import { describe, expect, it } from 'vitest';
import { BRAND } from '@opennavo/shared';
import { appEntityId, appOffers, desktopAppId, listItems, softwareAppNode } from '../app/utils/structuredData';
import type { PriceFact } from '../app/utils/packageFacts';
import { evidence, pageOf } from './packageFixtures';

const t = (key: string, named: Record<string, unknown> = {}) =>
  `[${key}${Object.keys(named).length ? ` ${Object.values(named).join(',')}` : ''}]`;

const offer: PriceFact = {
  region: 'US',
  edition: 'Pro',
  period: 'monthly',
  usage: 'subscription',
  downloadFree: false,
  amount: '9.99',
  currency: 'USD',
  purchaseUrl: 'https://vendor.example/pricing',
  channel: 'vendor',
  evidence: evidence('https://vendor.example/pricing', {
    checkedAt: '2026-10-01T08:00:00Z',
    validUntil: '2026-10-31T08:00:00Z'
  })
};

function app(overrides: Partial<PackageDetail> = {}): PackageDetail {
  return {
    kind: 'cask',
    token: 'editor',
    name: 'Editor',
    displayName: 'Editor',
    names: ['Editor', 'Editor Pro'],
    summary: 'Edits text',
    version: '5.2',
    isFont: false,
    tap: 'homebrew/cask',
    sourceUrl: 'https://github.com/Homebrew/homebrew-cask/blob/HEAD/Casks/e/editor.rb',
    iconUrl: 'https://cdn.example/editor.png',
    homepage: 'https://vendor.example/',
    primaryCategory: { slug: 'developer-tools', name: 'Developer Tools', icon: 'lucide:code' },
    screenshots: [
      {
        url: 'https://cdn.example/shot-1280.png',
        thumbUrl: 'https://cdn.example/shot-640.png',
        width: 1280,
        height: 800
      }
    ],
    dependsOn: { macos: '>= 13', arch: [], formulae: [], casks: [], requirements: { macos: { '>=': ['13'] } } },
    conflictsWith: { casks: [], formulae: [] },
    supports: { status: 'known', arm64: true, x86_64: false },
    downloadUrl: 'https://vendor.example/Editor.dmg',
    downloadSize: 317901801,
    releaseCount: 3,
    platforms: [
      {
        tag: 'arm64_sonoma',
        macos: '14',
        arch: 'arm64',
        version: '5.2',
        minMacos: '13',
        downloadUrl: 'https://vendor.example/Editor.dmg'
      }
    ],
    artifacts: { apps: ['Editor.app'], binaries: [], pkgs: [], uninstallNotes: [], entries: [] },
    ...overrides
  } as PackageDetail;
}

const url = 'https://opennavo.example/apps/editor';

describe('WP10 SoftwareApplication from the facts model (T2)', () => {
  it('Repeats what the page shows and offers only the API’s offer-ready prices', () => {
    const node = softwareAppNode(pageOf(app(), { offers: [offer] }), url, t)!;
    expect(node).toMatchObject({
      name: 'Editor',
      alternateName: ['Editor Pro'],
      operatingSystem: 'macOS',
      applicationCategory: 'DeveloperApplication',
      softwareVersion: '5.2',
      // The facts table's own words for the verified rows.
      softwareRequirements: '[package.facts.minimum 13]',
      processorRequirements: 'arm64',
      screenshot: ['https://cdn.example/shot-1280.png'],
      downloadUrl: 'https://vendor.example/Editor.dmg',
      identifier: { '@type': 'PropertyValue', propertyID: 'Homebrew cask', value: 'editor' },
      fileSize: '318 MB',
      releaseNotes: `${url}/versions`,
      url
    });
    expect(node.offers).toEqual({
      price: 9.99,
      priceCurrency: 'USD',
      url: 'https://vendor.example/pricing',
      name: 'Pro',
      eligibleRegion: { '@type': 'Country', name: 'US' },
      priceValidUntil: '2026-10-31',
      priceSpecification: {
        '@type': 'UnitPriceSpecification',
        price: 9.99,
        priceCurrency: 'USD',
        referenceQuantity: { '@type': 'QuantitativeValue', value: 1, unitCode: 'MON' }
      }
    });
    expect(node).not.toHaveProperty('aggregateRating');
  });

  it('keeps alternate names of every script in structured data', () => {
    const node = softwareAppNode(pageOf(app({ names: ['Editor', '编辑器', 'エディター', 'Редактор'] })), url, t)!;
    expect(node.alternateName).toEqual(['编辑器', 'エディター', 'Редактор']);
  });

  it('excludes a name matching the cask token', () => {
    const node = softwareAppNode(pageOf(app({ names: ['Editor', 'Editor Pro', 'editor'] })), url, t)!;
    expect(node.alternateName).toEqual(['Editor Pro']);
  });

  it('Never turns a price into an offer by itself', () => {
    const price = {
      type: 'free' as const,
      amount: null,
      currency: null,
      region: null,
      plan: null,
      period: null,
      appVersion: null,
      sourceUrl: 'https://vendor.example/pricing',
      method: 'vendor_site' as const,
      verifiedAt: '2026-10-01T08:00:00Z',
      stale: false,
      offer: false
    };
    // A verified price without region, edition or amount is shown on the page but is not an offer.
    const page = pageOf(app({ price }));
    expect(page.facts.prices).toHaveLength(1);
    expect(page.facts.offers).toEqual([]);
    expect(softwareAppNode(page, url, t)).not.toHaveProperty('offers');
    expect(appOffers([{ ...offer, amount: undefined }])).toEqual([]);
    const yearly = appOffers([
      { ...offer, period: 'yearly' },
      { ...offer, usage: 'paid', period: 'one_time', edition: 'Lifetime' }
    ]);
    expect(yearly[0]!.priceSpecification).toMatchObject({ referenceQuantity: { unitCode: 'ANN' } });
    expect(yearly[1]).not.toHaveProperty('priceSpecification');
    expect(
      softwareAppNode(pageOf(app(), { offers: [offer, { ...offer, edition: 'Team' }] }), url, t)!.offers
    ).toHaveLength(2);
  });

  it('Leaves out what is unknown or unverified, and skips fonts', () => {
    expect(softwareAppNode(pageOf(app({ isFont: true })), 'https://opennavo.example/apps/font', t)).toBeNull();
    const bare = softwareAppNode(
      pageOf(
        app({
          version: 'latest',
          names: ['Editor'],
          homepage: null,
          summary: null,
          iconUrl: null,
          screenshots: [],
          dependsOn: null,
          platforms: [],
          downloadUrl: null,
          downloadSize: null,
          releaseCount: 0
        })
      ),
      url,
      t
    )!;
    expect(Object.keys(bare).sort()).toEqual(['applicationCategory', 'identifier', 'name', 'operatingSystem', 'url']);
    // Stale Homebrew rows are shown with their date; the JSON-LD states none of them.
    const page = pageOf(app());
    const stale = {
      ...page,
      facts: {
        ...page.facts,
        requirements: page.facts.requirements.map(row => ({
          ...row,
          evidence: { ...row.evidence, status: 'stale' as const }
        }))
      }
    };
    const node = softwareAppNode(stale, url, t)!;
    expect(node).not.toHaveProperty('softwareRequirements');
    expect(node).not.toHaveProperty('processorRequirements');
  });
});

describe('App entity ids', () => {
  it('Are stable per app, and OpenNavo’s own entry is the desktop app /download describes', () => {
    expect(appEntityId('https://x', 'visual-studio-code')).toBe('https://x/apps/visual-studio-code#app');
    expect(appEntityId('https://x', 'python@3.14')).toBe('https://x/apps/python@3.14#app');
    expect(appEntityId('https://x', BRAND.caskToken)).toBe(desktopAppId('https://x'));
    expect(desktopAppId('https://x')).toBe('https://x/#desktop-app');
  });
});

describe('WP10 lists', () => {
  it('Numbers entries in page order, continuing across pages', () => {
    expect(
      listItems(
        [
          { name: 'A', url: 'https://x/a' },
          { name: 'B', url: 'https://x/b' }
        ],
        25
      )
    ).toEqual([
      { position: 25, name: 'A', url: 'https://x/a' },
      { position: 26, name: 'B', url: 'https://x/b' }
    ]);
    expect(listItems([{ name: 'A', url: 'https://x/a' }])[0]!.position).toBe(1);
  });
});

describe('Link apps', () => {
  const link = {
    homepage: 'https://lumen.app/',
    downloadUrl: 'https://download.lumen.app/Lumen.dmg',
    github: { repo: 'lumen-labs/lumen', latest: null },
    teamId: null,
    pricing: 'free' as const,
    notes: null,
    adoptedByCask: null
  };
  const linkApp = (overrides: Partial<PackageDetail['link'] & object> = {}) =>
    pageOf(app({ kind: 'link', homepage: null, downloadUrl: null, platforms: [], link: { ...link, ...overrides } }));

  it('States a download only on the developer site or in the GitHub repository', () => {
    expect(softwareAppNode(linkApp(), url, t)).toMatchObject({
      downloadUrl: 'https://download.lumen.app/Lumen.dmg',
      sameAs: 'https://lumen.app/'
    });
    expect(softwareAppNode(linkApp(), url, t)).not.toHaveProperty('identifier');
    // A mirror is never the official file; the GitHub release stands in for it.
    expect(softwareAppNode(linkApp({ downloadUrl: 'https://mirror.example/Lumen.dmg' }), url, t)!.downloadUrl).toBe(
      'https://github.com/lumen-labs/lumen/releases/latest'
    );
    expect(
      softwareAppNode(linkApp({ downloadUrl: 'https://mirror.example/Lumen.dmg', github: null }), url, t)
    ).not.toHaveProperty('downloadUrl');
  });
});
