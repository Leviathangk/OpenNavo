import { computed, createSSRApp, h, ref, type Component } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { ReleaseEntry } from '@opennavo/api';
import { SITE_LOCALES } from '@opennavo/shared';
import { siteMessage } from '../server/utils/siteCopy';
import { leadSentences } from '../app/utils/appAnswer';
import { formatDate } from '../app/utils/packageDate';
import { mergeReleaseEntries, officialSource, subpageAppNode } from '../app/utils/packageFacts';
import LatestRelease from '../app/components/package/LatestReleaseCard.vue';
import Price from '../app/components/package/PackagePrice.vue';
import Requirements from '../app/components/package/PackageRequirements.vue';
import Timeline from '../app/components/package/PackageVersionTimeline.vue';
import Command from '../app/components/package/PackageCommand.vue';
import { GEO_SAMPLES, geoPrice, geoRequirement } from './geoFixtures';
import { pageOf } from './packageFixtures';

const t = (key: string, named?: Record<string, unknown>) =>
  siteMessage('en-US', key, named as Record<string, string | number>);
beforeEach(() => {
  vi.stubGlobal('computed', computed);
  vi.stubGlobal('useI18n', () => ({ t, locale: ref('en') }));
  vi.stubGlobal('toAppLocale', () => 'en-US');
});
afterEach(() => vi.unstubAllGlobals());
const page = () => pageOf(GEO_SAMPLES[0]!.detail);
const release = (overrides: Partial<ReleaseEntry> = {}): ReleaseEntry => ({
  version: '1.0',
  source: 'homebrew',
  hasNotes: false,
  isLatest: false,
  isPrerelease: false,
  sections: [],
  publishedAt: '2026-10-21T00:00:00Z',
  translation: { status: 'source', locale: 'en-US' },
  ...overrides
});
async function render(component: Component, props: Record<string, unknown>) {
  const app = createSSRApp({ render: () => h(component, props) });
  app.component('NuxtLink', {
    props: ['to'],
    setup:
      (p, { slots }) =>
      () =>
        h('a', { href: p.to }, slots.default?.())
  });
  app.component('MarkdownContent', { props: ['source'], setup: p => () => h('div', p.source) });
  return renderToString(app);
}

describe('application SEO repair', () => {
  it.each(SITE_LOCALES)('normalizes developer punctuation before interpolation in %s', locale => {
    const pkg = page();
    pkg.developer = 'Notion Labs, Inc.';
    const sentences = leadSentences(pkg, (key, named) =>
      siteMessage(locale, key, named as Record<string, string | number>)
    );
    expect(sentences.join(' ')).not.toContain('Inc..');
    expect(sentences.join(' ')).not.toMatch(/[.。]{2}/);
    expect(sentences.join(' ')).toContain('Notion Labs, Inc');
  });
  it.each(SITE_LOCALES)('includes years in factual dates in %s', locale => {
    expect(formatDate('2026-10-21T23:59:00Z', { locale })).toContain('2026');
    expect(formatDate('2025-01-01T00:00:00Z', { locale })).toContain('2025');
    expect(formatDate('invalid', { locale })).toBe('');
  });
  it('omits empty release cards, including whitespace notes', async () => {
    expect(
      await render(LatestRelease, {
        release: release({ summary: '  ', bodyMarkdown: '\n' }),
        total: 1,
        versionsHref: '/apps/sample/versions'
      })
    ).not.toContain('whats-new');
    const html = await render(LatestRelease, {
      release: release({ summary: 'Improved export' }),
      total: 1,
      versionsHref: '/apps/sample/versions'
    });
    expect(html).toContain('Improved export');
    expect(html).toContain('2026');
    expect(html).toContain('<time');
  });
  it.each(SITE_LOCALES)('omits release translation provenance in %s', async locale => {
    vi.stubGlobal('useI18n', () => ({
      t: (key: string) => siteMessage(locale, key),
      locale: ref(locale)
    }));
    vi.stubGlobal('toAppLocale', () => locale);
    for (const component of [LatestRelease, Timeline]) {
      const renderRelease = (status: ReleaseEntry['translation']['status']) => {
        const value = release({
          hasNotes: true,
          summary: 'Improved export',
          translation: { status, locale, machineTranslated: status === 'machine' }
        });
        return render(component, { release: value, entries: [value], total: 1, versionsHref: '/apps/sample/versions' });
      };
      const original = await renderRelease('source');
      expect(await renderRelease('machine')).toBe(original);
      expect(await renderRelease('manual')).toBe(original);
    }
  });
  it('omits pending requirements and prices without amounts, retaining zero prices', async () => {
    const pkg = page();
    pkg.facts.requirements = [
      geoRequirement(1, {
        evidence: {
          sourceUrl: 'https://example.com',
          status: 'needs_review',
          checkedAt: null,
          validUntil: null
        }
      })
    ];
    expect(await render(Requirements, { pkg })).not.toContain('<table');
    pkg.facts.prices = [geoPrice(1, { amount: null })];
    expect(await render(Price, { pkg })).not.toContain('<table');
    pkg.facts.prices = [geoPrice(1, { amount: '0' })];
    expect(await render(Price, { pkg })).toContain('$0.00');
  });
  it('renders 24 releases with 19 empty ones compactly and no unknown compatibility', async () => {
    const entries = Array.from({ length: 24 }, (_, i) =>
      release({ version: `5.${24 - i}`, hasNotes: i < 5, summary: i < 5 ? `Changes ${i}` : null })
    );
    const html = await render(Timeline, {
      entries,
      compatibilityLabels: {
        title: 'Compatibility',
        unknown: 'Unknown',
        minimum: 'macOS {version}',
        maximum: 'up to {version}',
        appleSilicon: { yes: 'Native', no: 'Rosetta' }
      }
    });
    expect(html).not.toContain('No separate release notes');
    expect(html).not.toContain('Compatibility');
    expect(html.match(/class="on-version-entry /g)).toHaveLength(5);
    expect(html.match(/<time/g)?.length).toBeGreaterThanOrEqual(24);
    expect(html).toContain('2026');
  });
  it('preserves compact and merged RSS fragments without duplicate IDs', async () => {
    const html = await render(Timeline, {
      entries: [
        release({ id: 11, version: '2.0' }),
        release({ id: 12, version: '1.0', summary: 'Notes' }),
        release({ id: 13, version: '1.0,123' }),
        release({ version: '1.0,456' }),
        release({ version: '0.9' })
      ]
    });
    for (const id of ['r-11', 'r-12', 'r-13', 'v-1.0-456', 'v-0.9']) {
      expect(html.split(`id="${id}"`)).toHaveLength(2);
    }
  });
  it('retains known compatibility even on a compact release without notes', async () => {
    const html = await render(Timeline, {
      entries: [
        release({
          compatibility: {
            minimumMacOS: '13',
            maximumMacOS: null,
            appleSilicon: 'unknown',
            requirements: []
          }
        })
      ],
      compatibilityLabels: {
        title: 'Compatibility',
        unknown: 'Unknown',
        minimum: 'macOS {version} or later',
        maximum: 'up to {version}',
        appleSilicon: { yes: 'Native', no: 'Rosetta' }
      }
    });
    expect(html).toContain('macOS 13 or later');
    expect(html).not.toContain('Unknown');
  });
  it.each([
    { minimumMacOS: '13', maximumMacOS: '15', appleSilicon: 'yes' as const, requirements: [] },
    { minimumMacOS: null, maximumMacOS: null, appleSilicon: 'no' as const, requirements: [] },
    {
      minimumMacOS: null,
      maximumMacOS: null,
      appleSilicon: 'unknown' as const,
      requirements: [{ architectures: ['arm64'], minimumMacOS: '14', maximumMacOS: null }]
    }
  ])('retains build compatibility when the release with notes has no evidence: %j', async known => {
    const unknown = { minimumMacOS: null, maximumMacOS: null, appleSilicon: 'unknown' as const, requirements: [] };
    const base = release({ id: 21, version: '5.4', summary: 'Base notes', compatibility: unknown });
    const build = release({ id: 22, version: '5.4,120', compatibility: known });
    const labels = {
      title: 'Compatibility',
      unknown: 'Unknown',
      minimum: 'macOS {version} or later',
      maximum: 'up to {version}',
      appleSilicon: { yes: 'Native', no: 'Rosetta' }
    };
    for (const entries of [
      [base, build],
      [build, base]
    ]) {
      expect(mergeReleaseEntries(entries)[0]!.compatibility).toEqual(known);
      const html = await render(Timeline, { entries, compatibilityLabels: labels });
      expect(html).toContain('Base notes');
      if (known.minimumMacOS) expect(html).toContain('macOS 13 or later');
      if (known.maximumMacOS) expect(html).toContain('up to 15');
      if (known.appleSilicon === 'yes') expect(html).toContain('Native');
      if (known.appleSilicon === 'no') expect(html).toContain('Rosetta');
      if (known.requirements.length) expect(html).toContain('arm64: macOS 14 or later');
    }
    expect(base.compatibility).toEqual(unknown);
    expect(build.compatibility).toEqual(known);
  });
  it('merges a base version and its build while preserving notes and latest status', async () => {
    const entries = [
      release({ version: '5.4', summary: 'Base notes', hasNotes: true }),
      release({
        version: '5.4,120',
        build: '120',
        isLatest: true,
        bodyMarkdown: 'Build notes',
        hasNotes: true
      })
    ];
    const merged = mergeReleaseEntries(entries);
    expect(merged).toHaveLength(1);
    expect(merged[0]).toMatchObject({
      version: '5.4',
      summary: 'Base notes',
      bodyMarkdown: 'Build notes',
      isLatest: true
    });
    expect(entries[0]!.version).toBe('5.4');
    const html = await render(Timeline, { entries });
    expect(html.match(/class="on-version-entry /g)).toHaveLength(1);
  });
  it('keeps dated source evidence available for lifecycle warnings without claiming missing checksums', () => {
    const pkg = page();
    pkg.homebrewStatus = { state: 'active', reason: null, date: null };
    pkg.facts.requirements = [
      geoRequirement(1, {
        versions: [pkg.version],
        downloadUrl: 'https://vendor.example/app.dmg',
        sha256: 'a'.repeat(64),
        evidence: {
          status: 'verified',
          checkedAt: '2026-10-21T00:00:00Z',
          validUntil: '2026-12-01T00:00:00Z',
          sourceUrl: pkg.sourceUrl
        }
      })
    ];
    expect(officialSource(pkg)).toMatchObject({
      verified: true,
      checkedAt: '2026-10-21T00:00:00Z',
      entries: [{ host: 'vendor.example', sha256: 'a'.repeat(64) }]
    });
    pkg.facts.requirements[0]!.sha256 = null;
    expect(officialSource(pkg)).toMatchObject({ verified: false, entries: [{ sha256: null }] });
    pkg.homebrewStatus.state = 'disabled';
    expect(officialSource(pkg)).not.toBeNull();
    pkg.kind = 'link';
    expect(officialSource(pkg)).toBeNull();
  });
  it('gives clipboard buttons distinct accessible names', async () => {
    for (const key of ['install', 'uninstall', 'cleanup']) {
      const label = t(`package.copy.${key}`);
      expect(await render(Command, { command: 'brew test sample', copyLabel: label })).toContain(
        `aria-label="${label}"`
      );
    }
  });
  it('defines the application referred to by a subpage without commercial metadata', () => {
    const node = subpageAppNode(page(), 'https://example.com/apps/sample#app', 'https://example.com/apps/sample');
    expect(node).toMatchObject({
      '@type': 'SoftwareApplication',
      '@id': 'https://example.com/apps/sample#app',
      operatingSystem: 'macOS'
    });
    expect(Object.keys(node).sort()).toEqual(
      ['@id', '@type', 'name', 'operatingSystem', 'softwareVersion', 'url'].sort()
    );
  });
});
