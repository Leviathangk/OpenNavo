import { describe, expect, it } from 'vitest';
import type { PublicComponents } from '@opennavo/api';
import {
  alternativesOf,
  alternativesPageOpen,
  attributeValue,
  comparisonAttributes,
  priceText
} from '../app/utils/alternativesPage';

type PageDecision = PublicComponents['schemas']['PageDecision'];
type PackageAlternative = PublicComponents['schemas']['PackageAlternative'];

const decision = (patch: Partial<PageDecision>): PageDecision => ({
  robots: 'index, follow',
  canonicalPath: '',
  hreflang: ['en-US'],
  inSitemap: true,
  announce: true,
  reason: '',
  ...patch
});

const evidence = {
  sourceUrl: 'https://vendor.example/docs',
  checkedAt: '2026-10-01T00:00:00Z',
  validUntil: null,
  status: 'verified' as const
};

const alternative = (token: string, patch: Partial<PackageAlternative> = {}): PackageAlternative =>
  ({
    package: { kind: 'cask', token, displayName: token },
    reasons: ['open_source'],
    limits: [],
    bestFor: null,
    ...patch
  }) as PackageAlternative;

const pages = (alternatives?: PageDecision) =>
  ({
    pages: {
      overview: decision({}),
      versions: decision({}),
      dependencies: decision({}),
      details: decision({}),
      ...(alternatives ? { alternatives } : {})
    }
  }) as never;

describe('Alternatives intent page (X2)', () => {
  it('Opens only where the server decides the page is indexable with its own canonical', () => {
    const list = [alternative('zed'), alternative('sublime-text'), alternative('nova')];
    expect(alternativesPageOpen({ alternatives: list, indexing: pages(decision({})) })).toBe(true);
    for (const [robots, expected] of [
      ['index, follow, max-image-preview:large, max-snippet:-1, max-video-preview:-1', true],
      ['index, noindex, follow', false],
      ['follow, max-snippet:-1', false]
    ] as const) {
      expect(
        alternativesPageOpen({
          alternatives: list,
          indexing: pages(decision({ robots: robots as PageDecision['robots'] }))
        })
      ).toBe(expected);
    }
    // Not qualified: noindex with the overview as canonical (intent_disabled, intent_not_eligible, …).
    expect(
      alternativesPageOpen({
        alternatives: list,
        indexing: pages(decision({ robots: 'noindex, follow', canonicalPath: '/apps/code', reason: 'intent_disabled' }))
      })
    ).toBe(false);
    expect(alternativesPageOpen({ alternatives: list, indexing: pages() })).toBe(false);
    expect(alternativesPageOpen({ alternatives: list })).toBe(false);
    expect(alternativesPageOpen({ alternatives: [], indexing: pages(decision({})) })).toBe(false);
  });

  it('Prefers the comparison facts over the plain list', () => {
    const facts = { alternatives: [alternative('zed')] } as never;
    expect(alternativesOf({ facts, alternatives: [alternative('nova')] }).map(item => item.package.token)).toEqual([
      'zed'
    ]);
    expect(alternativesOf({ alternatives: [alternative('nova')] }).map(item => item.package.token)).toEqual(['nova']);
  });

  it('Compares only verified attributes, unknown where an app has none', () => {
    const zed = alternative('zed', {
      attributes: [
        { name: 'open_source', booleanValue: true, evidence },
        { name: 'export_formats', formats: ['md', 'html'], evidence }
      ]
    });
    const nova = alternative('nova', { attributes: [{ name: 'open_source', booleanValue: false, evidence }] });
    expect(comparisonAttributes([zed, nova])).toEqual(['open_source', 'export_formats']);
    expect(attributeValue(zed, 'open_source').value).toEqual({ kind: 'yes' });
    expect(attributeValue(nova, 'open_source').value).toEqual({ kind: 'no' });
    expect(attributeValue(zed, 'export_formats').value).toEqual({ kind: 'formats', formats: ['md', 'html'] });
    expect(attributeValue(nova, 'export_formats')).toEqual({ value: { kind: 'unknown' } });
  });

  it('Words a price in its own currency and period, and says nothing without one', () => {
    const t = (key: string, params?: Record<string, string>) => (params ? `${key}(${params.price})` : key);
    const price = {
      region: 'US',
      edition: 'Pro',
      period: 'yearly' as const,
      usage: 'subscription' as const,
      downloadFree: true,
      amount: '99',
      currency: 'USD',
      purchaseUrl: 'https://vendor.example/buy',
      channel: 'vendor' as const,
      evidence
    };
    expect(priceText(price, 'en-US', t)).toBe('package.facts.perYear($99.00)');
    expect(priceText({ ...price, period: 'one_time' }, 'en-US', t)).toBe('$99.00');
    expect(priceText({ ...price, usage: 'free', amount: undefined }, 'en-US', t)).toBe('package.facts.types.free');
    expect(priceText({ ...price, usage: 'unknown', amount: undefined }, 'en-US', t)).toBeNull();
  });
});
