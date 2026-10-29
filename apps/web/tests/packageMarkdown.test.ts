import { readFileSync } from 'node:fs';
import { siteMessage } from '../server/utils/siteCopy';
import { describe, expect, it } from 'vitest';
import type { PublicComponents } from '@opennavo/api';
import { SITE_LOCALES } from '@opennavo/shared';
import { mdText, nestedMarkdown, renderPackageMarkdown } from '../server/utils/packageMarkdown';
import { isOverviewPath, markdownAlternatesEnabled, markdownTarget } from '../shared/utils/markdownAlternate';

type PackageDetail = PublicComponents['schemas']['PackageDetail'];

const evidence = {
  sourceUrl: 'https://vendor.example/pricing',
  checkedAt: '2026-10-01T00:00:00Z',
  validUntil: null,
  status: 'verified' as const
};

const pkg = {
  kind: 'cask',
  token: 'facts-app',
  name: 'Facts App',
  displayName: 'Facts App',
  names: ['Facts App'],
  tap: 'homebrew/cask',
  formulaeUrl: 'https://formulae.brew.sh/cask/facts-app',
  isFont: false,
  summary: 'Edits *things* fast',
  version: '11.0',
  developer: 'Vendor',
  homepage: 'https://vendor.example/',
  license: 'MIT',
  installCommand: 'brew install --cask facts-app',
  installs: { d30: 1234, d90: 3000, d365: 9000 },
  categories: [{ slug: 'developer-tools', name: 'Developer Tools', icon: 'lucide:code' }],
  description: '## Facts App\n\nA paragraph.',
  sourceUrl: 'https://github.com/Homebrew/homebrew-cask/blob/HEAD/Casks/f/facts-app.rb',
  latestRelease: {
    version: '11.0',
    publishedAt: '2026-09-30T08:00:00Z',
    source: 'webpage',
    isLatest: true,
    isPrerelease: false,
    hasNotes: true,
    summary: 'Faster.',
    sections: [{ area: 'Editor', items: ['Better **tabs**'] }],
    translation: { status: 'source', locale: 'en-US' }
  },
  facts: {
    requirements: [
      {
        versions: ['11.0'],
        architectures: ['arm64'],
        platformMacOS: [],
        minimumMacOS: '13',
        maximumMacOS: null,
        appleSilicon: 'yes',
        downloadUrl: null,
        sha256: null,
        evidence
      }
    ],
    uninstall: [
      { kind: 'zap', value: '~/Library/Preferences/facts.plist', phase: 'zap', deletesUserData: true, evidence }
    ],
    overview: {
      version: '11.0',
      aliases: [],
      artifacts: { apps: [], binaries: [], pkgs: [], entries: [] },
      dependencyCount: 0,
      conflictCount: 0
    },
    alternatives: [
      { package: { token: 'zed', displayName: 'Zed' }, reasons: ['open_source'], limits: [], bestFor: null }
    ],
    prices: [
      {
        region: 'US',
        edition: 'Pro',
        period: 'monthly',
        usage: 'subscription',
        downloadFree: true,
        amount: '9.99',
        currency: 'USD',
        purchaseUrl: 'https://vendor.example/buy',
        channel: 'vendor',
        evidence
      },
      {
        region: 'US',
        edition: 'Team',
        period: 'yearly',
        usage: 'unknown',
        downloadFree: false,
        purchaseUrl: 'https://vendor.example/buy',
        channel: 'vendor',
        evidence: { ...evidence, status: 'stale' }
      }
    ]
  }
} as unknown as PackageDetail;

describe('Markdown alternates (X4)', () => {
  it('Is off unless the runtime switch is on', () => {
    expect(markdownAlternatesEnabled(undefined)).toBe(false);
    expect(markdownAlternatesEnabled(false)).toBe(false);
    expect(markdownAlternatesEnabled('1')).toBe(false);
    expect(markdownAlternatesEnabled(true)).toBe(true);
    expect(markdownAlternatesEnabled('true')).toBe(true);
  });

  it('Reads the app and language from .md paths and links them from overviews only', () => {
    expect(markdownTarget('/apps/visual-studio-code.md')).toEqual({ locale: 'en-US', token: 'visual-studio-code' });
    expect(markdownTarget('/zh/apps/python@3.14.md')).toEqual({ locale: 'zh-CN', token: 'python@3.14' });
    for (const path of ['/apps/Foo.md', '/apps/foo.md/', '/apps/foo/versions.md', '/xx/apps/foo.md', '/apps/foo'])
      expect(markdownTarget(path)).toBeUndefined();
    expect(isOverviewPath('/apps/foo')).toBe(true);
    expect(isOverviewPath('/apps')).toBe(false);
    expect(isOverviewPath('/apps/foo/versions')).toBe(false);
  });

  it('Writes the same scoped and dated facts as the overview', () => {
    const md = renderPackageMarkdown(pkg, { siteUrl: 'https://opennavo.example', locale: 'en-US' });
    expect(md.startsWith('# Facts App\n\nEdits \\*things\\* fast\n')).toBe(true);
    expect(md).toContain('https://opennavo.example/apps/facts-app');
    expect(md).toContain('- **Install**: `brew install --cask facts-app`');
    expect(md).toContain('[Developer Tools](https://opennavo.example/categories/developer-tools)');
    // The description's own headings sit below this document's sections.
    expect(md).toContain('## About\n\n### Facts App\n\nA paragraph.');
    expect(md).toContain('- macOS 13 or later · Version: 11.0 · Apple silicon · Runs natively');
    expect(md).toContain('Checked 2026-10-01');
    expect(md).toContain('- Subscription · $9.99 per month (United States, Pro)');
    expect(md).not.toContain('(United States, Team)');
    expect(md).not.toContain('As of 2026-10-01 · May have changed');
    expect(md).toContain('- [Zed](https://opennavo.example/apps/zed) — Open source');
    const cleanup = md.slice(md.indexOf('- `brew uninstall --zap'));
    expect(cleanup).toContain(siteMessage('en-US', 'package.guide.zap'));
    expect(cleanup).toContain(siteMessage('en-US', 'package.guide.uninstall'));
    expect(cleanup).toContain('`~/Library/Preferences/facts.plist`');
    expect(cleanup).toContain(siteMessage('en-US', 'package.uninstall.checked', { date: '2026-10-01' }));
    expect(md).toContain('### 11.0 (2026-09-30)\n\nFaster.\n\n**Editor**\n\n- Better **tabs**');
    expect(md.endsWith('\n')).toBe(true);
  });

  it('Writes the page language with its own links', () => {
    const md = renderPackageMarkdown(pkg, { siteUrl: 'https://opennavo.example', locale: 'zh-CN' });
    expect(md).toContain('https://opennavo.example/zh/apps/facts-app');
    expect(md).toContain('## 介绍');
    expect(md).toContain('macOS 13 起');
  });

  it.each(SITE_LOCALES)('Omits translation provenance from Markdown in %s', locale => {
    const options = { siteUrl: 'https://opennavo.example', locale };
    const detail = { ...pkg, sourceLocale: 'en-US' as const, machineTranslated: true };
    expect(renderPackageMarkdown(detail, options)).toBe(
      renderPackageMarkdown({ ...detail, machineTranslated: false }, options)
    );
  });

  it('Keeps yearly billing, source and store download scope', () => {
    const detail = structuredClone(pkg);
    const price = detail.facts!.prices[0]!;
    price.period = 'yearly';
    let md = renderPackageMarkdown(detail, { siteUrl: 'https://opennavo.example', locale: 'en-US' });
    expect(md).toContain('$9.99 per year');
    price.channel = 'app_store';
    price.usage = 'unknown';
    price.amount = '0';
    md = renderPackageMarkdown(detail, { siteUrl: 'https://opennavo.example', locale: 'en-US' });
    expect(md).toContain('Free to download');
    expect(md).toContain(siteMessage('en-US', 'package.price.storeNote'));
    expect(md).not.toContain('Subscription');
  });

  it('Omits unknown, pending and undated claims; retains dated older-version requirements', () => {
    const detail = structuredClone(pkg);
    for (const price of detail.facts!.prices) price.evidence.status = 'unknown';
    for (const entry of detail.facts!.uninstall) entry.evidence.checkedAt = null;
    const row = detail.facts!.requirements[0]!;
    row.evidence.status = 'unknown';
    let md = renderPackageMarkdown(detail, { siteUrl: 'https://opennavo.example', locale: 'en-US' });
    expect(md).not.toMatch(/per month|--zap|macOS 13/);
    row.evidence.status = 'needs_review';
    row.versions = ['9.0'];
    md = renderPackageMarkdown(detail, { siteUrl: 'https://opennavo.example', locale: 'en-US' });
    expect(md).not.toContain('Version: 9.0');
    expect(md).not.toContain(siteMessage('en-US', 'package.facts.pending'));
    row.evidence = { ...row.evidence, status: 'stale', checkedAt: '2026-10-01T00:00:00Z' };
    md = renderPackageMarkdown(detail, { siteUrl: 'https://opennavo.example', locale: 'en-US' });
    expect(md).toContain('Version: 9.0');
    expect(md).toContain(siteMessage('en-US', 'package.facts.stale'));
    row.minimumMacOS = null;
    row.platformMacOS = ['14'];
    md = renderPackageMarkdown(detail, { siteUrl: 'https://opennavo.example', locale: 'en-US' });
    expect(md).toContain('Build for macOS 14');
    expect(md).not.toContain('macOS 14 or later');
  });

  it('Uses German server copy with the normal English fallback for absent keys', () => {
    const german = JSON.parse(readFileSync(new URL('../i18n/locales/de-DE.json', import.meta.url), 'utf8'));
    expect(siteMessage('de-DE', 'markdown.about')).toBe(german.markdown.about);
    expect(siteMessage('de-DE', 'markdown.about')).not.toBe(siteMessage('en-US', 'markdown.about'));
    const md = renderPackageMarkdown(pkg, { siteUrl: 'https://opennavo.example', locale: 'de-DE' });
    expect(md).toContain(`## ${german.markdown.about}`);
    expect(md).toContain('https://opennavo.example/de/apps/facts-app');
    expect(siteMessage('de-DE', 'missing.test.key')).toBe('missing.test.key');
  });

  it('Escapes markup in plain text and nests body headings', () => {
    expect(mdText('a*b [c] `d` <e>')).toBe('a\\*b \\[c\\] \\`d\\` \\<e\\>');
    expect(nestedMarkdown('# A\n## B\ntext', 2)).toBe('### A\n#### B\ntext');
    expect(nestedMarkdown('###### F', 3)).toBe('###### F');
  });
});
