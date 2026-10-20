import { readFileSync } from 'node:fs';
import type { PackageDetail } from '@opennavo/api';
import { selectPlural } from '@opennavo/shared';
import { describe, expect, it } from 'vitest';
import {
  aroundDate,
  capabilities,
  chipsOf,
  downloadChoice,
  factSources,
  fontFiles,
  homebrewRows,
  macosRange,
  oldestCheck,
  pausedByOpenNavo,
  priceAmount,
  priceEdition,
  priceUsage,
  quickFacts,
  slimPackage,
  verifiedPrices
} from '../app/utils/packageFacts';
import type { PriceFact } from '../app/utils/packageFacts';
import { faqItems } from '../app/utils/packageFaq';
import type { FaqTranslate } from '../app/utils/packageFaq';
import { softwareAppNode } from '../app/utils/structuredData';
import { evidence, pageOf } from './packageFixtures';

function translator(code: 'zh-CN' | 'en-US'): FaqTranslate {
  const messages = JSON.parse(readFileSync(new URL(`../i18n/locales/${code}.json`, import.meta.url), 'utf8'));
  return (key, named = {}, plural) => {
    const template = key.split('.').reduce((node, part) => node?.[part], messages);
    if (typeof template !== 'string') throw new Error(`missing ${code} ${key}`);
    const form = plural === undefined ? template : selectPlural(template, plural, code);
    return form.replace(/\{(\w+)\}/g, (_, name: string) => {
      if (!(name in named)) throw new Error(`unfilled {${name}} in ${code} ${key}`);
      return String(named[name]);
    });
  };
}

const SOURCE = 'https://github.com/Homebrew/homebrew-cask/blob/HEAD/Casks/e/editor.rb';

const platform = (arch: 'arm64' | 'x86_64', url: string | null, version = '5.2') =>
  ({ tag: `${arch}_sonoma`, macos: '14', arch, version, minMacos: '13', downloadUrl: url }) as NonNullable<
    PackageDetail['platforms']
  >[number];

function app(overrides: Partial<PackageDetail> = {}): PackageDetail {
  return {
    kind: 'cask',
    token: 'editor',
    name: 'Editor',
    displayName: 'Editor',
    names: ['Editor', 'Editor Pro'],
    summary: 'Edits text',
    developer: 'Acme',
    license: 'MIT',
    version: '5.2',
    isFont: false,
    autoUpdates: true,
    disabled: false,
    tap: 'homebrew/cask',
    sourceUrl: SOURCE,
    formulaeUrl: 'https://formulae.brew.sh/cask/editor',
    installCommand: 'brew install --cask editor',
    homepage: 'https://vendor.example/',
    downloadUrl: 'https://vendor.example/Editor.dmg',
    downloadSha256: 'A'.repeat(64),
    installs: { d30: 10, d90: 30, d365: 120 },
    homebrewStatus: { state: 'active', reason: null, date: null },
    supports: { status: 'known', arm64: true, x86_64: true },
    conflictsWith: { casks: [], formulae: [] },
    platforms: [platform('arm64', 'https://vendor.example/u.dmg'), platform('x86_64', 'https://vendor.example/u.dmg')],
    artifacts: { apps: ['Editor.app'], binaries: [], pkgs: [], uninstallNotes: [], entries: [] },
    screenshots: [],
    releaseCount: 0,
    ...overrides
  } as PackageDetail;
}

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
  verifiedAt: '2025-11-01T08:00:00Z',
  stale: false,
  offer: true
};

describe('T6 payload', () => {
  it('Drops what the pages do not read and keeps the facts that replace it', () => {
    const detail = app({ price, descriptionEn: 'Edits text', tags: ['editor'] });
    const page = slimPackage({ ...detail, facts: pageOf(detail).facts });
    for (const key of ['platforms', 'descriptionEn', 'tags', 'artifacts', 'downloadUrl', 'downloadSha256', 'price'])
      expect(page).not.toHaveProperty(key);
    expect(page.facts.overview.artifacts.apps).toEqual(['Editor.app']);
    expect(page.facts.overview.downloadUrl).toBe('https://vendor.example/Editor.dmg');
    expect(page.installCommand).toBe('brew install --cask editor');
  });

  it('Gives an older API without facts a model that states nothing new', () => {
    const page = slimPackage(app());
    expect(page.facts.requirements).toEqual([]);
    expect(page.facts.prices).toEqual([]);
    expect(page.facts.overview.sha256).toBe('A'.repeat(64));
    expect(quickFacts(page, translator('en-US'), 'en-US').rows.map(row => row.key)).toEqual([
      'developer',
      'license',
      'install',
      'homebrew',
      'names'
    ]);
  });
});

describe('T3 capabilities', () => {
  it('Enables blocks by distribution', () => {
    expect(capabilities(pageOf(app()))).toEqual({
      homebrew: true,
      uninstall: true,
      dependencies: false,
      installs: true,
      safety: true
    });
    const link = capabilities(pageOf(app({ kind: 'link', platforms: [] })));
    expect(link).toMatchObject({ homebrew: false, uninstall: false, installs: false, safety: true });
    expect(capabilities(pageOf(app({ isFont: true }))).safety).toBe(false);
    expect(capabilities(pageOf(app({ conflictsWith: { casks: ['editor@beta'], formulae: [] } }))).dependencies).toBe(
      true
    );
  });

  it('Tells OpenNavo’s pause from Homebrew’s disable (T1)', () => {
    const notice = { date: null, reason: 'No version update for more than 90 days' };
    expect(pausedByOpenNavo(pageOf(app({ disabled: true, disable: notice })))).toBe(true);
    expect(pausedByOpenNavo(pageOf(app({ disabled: true, disable: notice, recommendationPaused: false })))).toBe(false);
    expect(
      pausedByOpenNavo(
        pageOf(
          app({ disabled: true, disable: notice, homebrewStatus: { state: 'disabled', reason: null, date: null } })
        )
      )
    ).toBe(false);
  });
});

describe('Requirement rows', () => {
  it('Keeps Homebrew’s rows apart from the vendor’s and states only what verified rows agree on', () => {
    const page = pageOf(app());
    expect(homebrewRows(page)).toHaveLength(2);
    expect(chipsOf(page)).toEqual(['arm64', 'x86_64']);
    expect(macosRange(page)).toEqual({ minimum: '13', maximum: null });
    const split = pageOf(app({ platforms: [platform('arm64', 'a'), { ...platform('x86_64', 'b'), minMacos: '12' }] }));
    expect(macosRange(split).minimum).toBeNull();
    const old = pageOf(app({ platforms: [platform('arm64', 'a', '4.0')] }));
    expect(chipsOf(old)).toEqual([]);
  });

  it('Links the file Homebrew uses only when the match is reliable', () => {
    expect(downloadChoice(pageOf(app()))).toEqual({ kind: 'direct', url: 'https://vendor.example/u.dmg' });
    expect(downloadChoice(pageOf(app({ platforms: [] })))).toEqual({
      kind: 'direct',
      url: 'https://vendor.example/Editor.dmg'
    });
    expect(downloadChoice(pageOf(app({ platforms: [], downloadUrl: null })))).toEqual({
      kind: 'official',
      url: 'https://vendor.example/',
      varies: false
    });
    expect(downloadChoice(pageOf(app({ platforms: [], downloadUrl: null, homepage: null })))).toBeNull();
    const split = [
      platform('x86_64', 'https://vendor.example/intel.dmg'),
      platform('arm64', 'https://vendor.example/arm.dmg')
    ];
    expect(downloadChoice(pageOf(app({ platforms: split })))).toEqual({
      kind: 'arch',
      items: [
        { arch: 'arm64', url: 'https://vendor.example/arm.dmg' },
        { arch: 'x86_64', url: 'https://vendor.example/intel.dmg' }
      ]
    });
    const varies = [
      platform('arm64', 'https://vendor.example/a.dmg'),
      { ...platform('arm64', 'https://vendor.example/b.dmg'), macos: '15' }
    ];
    expect(downloadChoice(pageOf(app({ platforms: varies })))).toEqual({
      kind: 'official',
      url: 'https://vendor.example/',
      varies: true
    });
    expect(downloadChoice(pageOf(app({ platforms: [platform('arm64', null)] })))).toMatchObject({
      kind: 'official',
      varies: true
    });
    // Legacy builds for older macOS releases are not the current version.
    expect(
      downloadChoice(pageOf(app({ platforms: [platform('x86_64', 'https://vendor.example/old.dmg', '4.0')] })))
    ).toEqual({ kind: 'direct', url: 'https://vendor.example/Editor.dmg' });
  });
});

describe('F2 prices', () => {
  const t = translator('en-US');
  const fact = (overrides: Partial<PriceFact> = {}): PriceFact => ({
    region: 'US',
    edition: 'Pro',
    period: 'monthly',
    usage: 'subscription',
    downloadFree: false,
    amount: '9.99',
    currency: 'USD',
    purchaseUrl: 'https://vendor.example/pricing',
    channel: 'vendor',
    evidence: evidence('https://vendor.example/pricing'),
    ...overrides
  });

  it('Formats amounts, billing and editions without inventing any', () => {
    expect(priceAmount(fact(), t, 'en-US')).toBe('$9.99 per month');
    expect(priceAmount(fact({ period: 'yearly' }), t, 'en-US')).toBe('$9.99 per year');
    expect(priceAmount(fact({ period: 'one_time', currency: 'EUR' }), t, 'en-US')).toBe('€9.99');
    expect(priceAmount(fact({ currency: 'XX' }), t, 'en-US')).toBe('9.99 XX per month');
    expect(priceAmount(fact({ amount: undefined }), t, 'en-US')).toBe('');
    expect(priceUsage(fact(), t)).toBe('Subscription');
    // A store's free download is said to be a free download, not free to use.
    expect(priceUsage(fact({ usage: 'unknown', downloadFree: true, channel: 'app_store' }), t)).toBe(
      'Free to download'
    );
    expect(priceUsage(fact({ usage: 'unknown' }), t)).toBe('');
    expect(priceEdition(fact({ edition: 'unspecified' }))).toBe('');
    expect(priceEdition(fact({ edition: 'App Store', channel: 'app_store' }))).toBe('');
    expect(priceEdition(fact())).toBe('Pro');
  });

  it('Shows stale prices with their date but states only verified ones', () => {
    const page = pageOf(app(), {
      prices: [
        fact(),
        fact({ edition: 'Team', evidence: evidence('https://vendor.example/pricing', { status: 'stale' }) })
      ]
    });
    expect(verifiedPrices(page)).toHaveLength(1);
    const rows = quickFacts(page, t, 'en-US').rows;
    expect(rows.find(row => row.key === 'price')).toMatchObject({
      value: 'Subscription · $9.99 per month',
      anchor: 'price'
    });
  });
});

describe('T2 one facts model', () => {
  it('Agrees across the facts table, the questions and the JSON-LD', () => {
    const t = translator('en-US');
    const page = pageOf(app({ price: { ...price, type: 'free', amount: null, currency: null, period: null } }));
    const facts = quickFacts(page, t, 'en-US');
    const value = (key: string) => facts.rows.find(row => row.key === key)?.value;
    expect(value('chips')).toBe('Apple silicon · Intel');
    expect(value('macos')).toBe('macOS 13 or later');
    expect(value('price')).toBe('Free');
    expect(value('install')).toBe('brew install --cask editor');
    expect(value('homebrew')).toBe('Available; the app updates itself');
    expect(value('names')).toBe('Editor Pro');
    expect(facts.checkedAt).toBe('2025-11-01T08:00:00Z');

    const faq = faqItems(page, t, 'en-US');
    expect(faq.find(item => item.key === 'price')!.answer).toMatch(/^Yes, Editor is free to use\./);
    expect(faq.find(item => item.key === 'chips')!.answer).toBe(
      'Homebrew installs it on both Apple silicon and Intel Macs, from the same download. It needs macOS 13 or later.'
    );

    const node = softwareAppNode(page, 'https://opennavo.example/apps/editor', t, 'en-US')!;
    expect(node.softwareRequirements).toBe(value('macos'));
    expect(node.processorRequirements).toBe('arm64, x86_64');
    // The legacy free price has no region or edition, so it is on the page but is not an offer.
    expect(node).not.toHaveProperty('offers');
  });

  it('Lists link apps’ version, source and signature, and fonts’ files', () => {
    const t = translator('zh-CN');
    const link = pageOf(
      app({
        kind: 'link',
        version: '2.4.1',
        platforms: [],
        license: null,
        link: {
          homepage: 'https://lumen.app/',
          downloadUrl: 'https://lumen.app/download',
          github: null,
          teamId: 'ABCDE12345',
          pricing: 'free',
          notes: null,
          adoptedByCask: null
        }
      })
    );
    expect(quickFacts(link, t, 'zh-CN').rows.map(row => [row.key, row.value])).toEqual([
      ['version', '2.4.1'],
      ['source', 'lumen.app'],
      ['price', '免费'],
      ['developer', 'Acme'],
      ['teamId', 'ABCDE12345'],
      ['names', 'Editor Pro']
    ]);
    const font = pageOf(
      app({
        isFont: true,
        artifacts: {
          apps: [],
          binaries: [],
          pkgs: [],
          entries: [
            {
              type: 'font',
              phase: 'install',
              sources: ['Mono-Regular.ttf', 'Mono-Bold.otf'],
              target: null,
              declaration: {}
            }
          ]
        }
      })
    );
    expect(fontFiles(font)).toEqual({ count: 2, formats: ['OTF', 'TTF'] });
    expect(quickFacts(font, t, 'zh-CN').rows.map(row => row.key)).toEqual([
      'files',
      'location',
      'developer',
      'license',
      'install',
      'homebrew',
      'names'
    ]);
  });

  it('Cites every dated source once and never dates one without evidence', () => {
    const page = pageOf(app({ price }), {
      requirements: [
        ...pageOf(app()).facts.requirements,
        {
          versions: ['5.2'],
          architectures: ['arm64'],
          platformMacOS: [],
          minimumMacOS: '12',
          maximumMacOS: null,
          appleSilicon: 'yes',
          downloadUrl: null,
          sha256: null,
          evidence: evidence('https://vendor.example/requirements', { status: 'needs_review' })
        }
      ]
    });
    expect(factSources(page).map(source => [source.kind, source.url, Boolean(source.checkedAt)])).toEqual([
      ['homebrew', SOURCE, true],
      ['formulae', 'https://formulae.brew.sh/cask/editor', false],
      ['price', 'https://vendor.example/pricing', true]
    ]);
    expect(oldestCheck([evidence('a', { checkedAt: '2026-01-02T00:00:00Z' }), evidence('b'), null])).toBe(
      '2025-12-01T08:00:00Z'
    );
    expect(oldestCheck([evidence('a', { status: 'needs_review' })])).toBeUndefined();
  });

  it('Splits a sentence around its date so the date can be a time element', () => {
    const t = translator('zh-CN');
    expect(aroundDate(t, 'package.facts.asOf')).toEqual({ before: '截至 ', after: '' });
    expect(aroundDate(translator('en-US'), 'package.quick.checked')).toEqual({
      before: 'These facts were checked on or after ',
      after: '.'
    });
  });
});
