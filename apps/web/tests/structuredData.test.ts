import type { PackageDetail } from '@opennavo/api';
import { describe, expect, it } from 'vitest';
import { appOffer, listItems, softwareAppNode } from '../app/utils/structuredData';

const t = (key: string) => `[${key}]`;

const price = {
  type: 'subscription' as const,
  amount: 9.99,
  currency: 'USD',
  region: 'US',
  plan: 'Pro',
  period: 'month' as const,
  appVersion: '5.2',
  sourceUrl: 'https://vendor.example/pricing',
  method: 'vendor_site' as const,
  verifiedAt: '2026-10-01T08:00:00Z',
  stale: false,
  offer: true
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
    supports: { status: 'known', arm64: true, x86_64: false },
    downloadUrl: 'https://vendor.example/Editor.dmg',
    platforms: [],
    artifacts: { apps: ['Editor.app'], binaries: [], pkgs: [], uninstallNotes: [], entries: [] },
    ...overrides
  } as PackageDetail;
}

describe('WP10 SoftwareApplication', () => {
  it('Repeats what the page shows and offers only verified prices', () => {
    const node = softwareAppNode(app({ price }), 'https://opennavo.example/apps/editor', t)!;
    expect(node).toMatchObject({
      name: 'Editor',
      alternateName: ['Editor Pro'],
      operatingSystem: 'macOS',
      applicationCategory: 'DeveloperApplication',
      softwareVersion: '5.2',
      softwareRequirements: 'macOS >= 13',
      processorRequirements: 'arm64',
      screenshot: ['https://cdn.example/shot-1280.png'],
      downloadUrl: 'https://vendor.example/Editor.dmg',
      url: 'https://opennavo.example/apps/editor'
    });
    expect(node.offers).toEqual({
      price: 9.99,
      priceCurrency: 'USD',
      url: 'https://vendor.example/pricing',
      priceValidUntil: '2027-10-01',
      name: 'Pro',
      eligibleRegion: { '@type': 'Country', name: 'US' },
      priceSpecification: {
        '@type': 'UnitPriceSpecification',
        price: 9.99,
        priceCurrency: 'USD',
        referenceQuantity: { '@type': 'QuantitativeValue', value: 1, unitCode: 'MON' }
      }
    });
    expect(node).not.toHaveProperty('aggregateRating');
  });

  it('Leaves out what is unknown and skips fonts', () => {
    expect(softwareAppNode(app({ isFont: true }), 'https://opennavo.example/apps/font', t)).toBeNull();
    const bare = softwareAppNode(
      app({
        version: 'latest',
        names: ['Editor'],
        homepage: null,
        summary: null,
        iconUrl: null,
        screenshots: [],
        dependsOn: null,
        supports: { status: 'unknown', arm64: false, x86_64: false },
        downloadUrl: null,
        price: { ...price, offer: false }
      }),
      'https://opennavo.example/apps/editor',
      t
    )!;
    expect(Object.keys(bare).sort()).toEqual(['applicationCategory', 'name', 'operatingSystem', 'url']);
  });

  it('Publishes price 0 only for free apps and notes in-app purchases', () => {
    const free = {
      ...price,
      type: 'free_with_iap' as const,
      amount: null,
      currency: null,
      region: null,
      plan: null,
      period: null
    };
    expect(appOffer(free, t)).toEqual({
      price: 0,
      url: 'https://vendor.example/pricing',
      priceValidUntil: '2027-10-01',
      description: '[package.facts.types.free_with_iap]'
    });
    expect(appOffer({ ...price, type: 'paid', period: 'one_time' }, t)).not.toHaveProperty('priceSpecification');
    expect(appOffer(null, t)).toBeNull();
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
    app({ kind: 'link', homepage: null, downloadUrl: null, link: { ...link, ...overrides } });

  it('States a download only on the developer site or in the GitHub repository', () => {
    const url = 'https://opennavo.example/apps/lumen';
    expect(softwareAppNode(linkApp(), url, t)).toMatchObject({
      downloadUrl: 'https://download.lumen.app/Lumen.dmg',
      sameAs: 'https://lumen.app/'
    });
    // A mirror is never the official file; the GitHub release stands in for it.
    expect(softwareAppNode(linkApp({ downloadUrl: 'https://mirror.example/Lumen.dmg' }), url, t)!.downloadUrl).toBe(
      'https://github.com/lumen-labs/lumen/releases/latest'
    );
    expect(
      softwareAppNode(linkApp({ downloadUrl: 'https://mirror.example/Lumen.dmg', github: null }), url, t)
    ).not.toHaveProperty('downloadUrl');
  });
});
