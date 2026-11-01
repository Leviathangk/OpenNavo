import { readFileSync } from 'node:fs';
import { siteMessage } from '../server/utils/siteCopy';
import { describe, expect, it } from 'vitest';
import type { PublicComponents } from '@opennavo/api';
import { SITE_LOCALES } from '@opennavo/shared';
import { codeSpan, mdText, mdUrl, nestedMarkdown, renderPackageMarkdown } from '../server/utils/packageMarkdown';
import {
  isOverviewPath,
  markdownAlternatesEnabled,
  markdownPageTarget,
  markdownTarget,
  prefersMarkdown,
  supportsMarkdownNegotiation
} from '../shared/utils/markdownAlternate';

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
    expect(markdownPageTarget('/zh/apps/foo')).toEqual({ kind: 'app', locale: 'zh-CN', token: 'foo' });
    expect(markdownPageTarget('/zh')).toEqual({ kind: 'home', locale: 'zh-CN' });
    expect(markdownPageTarget('/apps/foo/versions')).toBeUndefined();
    expect(supportsMarkdownNegotiation('/')).toBe(true);
    expect(supportsMarkdownNegotiation('/apps/foo')).toBe(true);
    expect(supportsMarkdownNegotiation('/apps/foo.md')).toBe(false);
    expect(supportsMarkdownNegotiation('/apps/foo/versions')).toBe(false);
    expect(isOverviewPath('/apps')).toBe(false);
    expect(isOverviewPath('/apps/foo/versions')).toBe(false);
  });

  it.each([
    ['text/markdown', true],
    ['text/markdown ; q=1', true],
    ['text/markdown, text/html;q=0.8', true],
    ['text/html, text/markdown;q=0.5', false],
    ['text/markdown;q=0.5, */*;q=0.9', false],
    ['text/markdown;q=0.9, text/*;q=0.5', true],
    ['text/html', false],
    ['*/*', false],
    ['text/markdown;q=0, text/html', false],
    ['text/markdown;q=0.8, text/html;q=0.8', true],
    [undefined, false]
  ] as const)('selects Markdown only when Accept prefers it: %s', (accept, expected) => {
    expect(prefersMarkdown(accept)).toBe(expected);
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
    expect(md).toContain('### 11.0 (2026-09-30)\n\nFaster.\n\n**Editor**\n\n- Better \\*\\*tabs\\*\\*');
    expect(md.endsWith('\n')).toBe(true);
  });

  it('Escapes release note items that would become links or markup', () => {
    const release = {
      ...pkg,
      latestRelease: {
        ...pkg.latestRelease,
        summary: 'Notes',
        sections: [
          {
            area: 'Editor',
            items: ['See [the update](https://evil.example) for details', 'Use `cmd` now', 'Better **tabs**']
          }
        ]
      }
    } as PackageDetail;
    const md = renderPackageMarkdown(release, { siteUrl: 'https://opennavo.example', locale: 'en-US' });
    expect(md).toContain(
      `## ${siteMessage('en-US', 'markdown.latestRelease')}\n\n### 11.0 (2026-09-30)\n\nNotes\n\n**Editor**\n\n` +
        '- See \\[the update\\](https://evil.example) for details\n- Use \\`cmd\\` now\n- Better \\*\\*tabs\\*\\*'
    );
    expect(md).not.toContain('[the update](https://evil.example)');
  });

  it.each([
    ['disabled', { homebrewStatus: { state: 'disabled', reason: 'discontinued', date: null } }, false],
    ['deprecated', { homebrewStatus: { state: 'deprecated', reason: 'unmaintained', date: null } }, true],
    ['paused', { homebrewStatus: { state: 'active', reason: null, date: null }, disabled: true }, true]
  ] as const)('Writes brew commands only while Homebrew can install it: %s', (_, fields, commands) => {
    const md = renderPackageMarkdown({ ...pkg, ...fields } as PackageDetail, {
      siteUrl: 'https://opennavo.example',
      locale: 'en-US'
    });
    for (const command of ['brew install --cask facts-app', 'brew uninstall --cask facts-app', '--zap'])
      expect(md.includes(command), command).toBe(commands);
  });

  it('Writes the same neutral source row as the page for a closed-source identity', () => {
    // The HTML page states this row (PackageSafety); the .md alternate must not tell a reader less.
    const identity = {
      channels: [],
      openSource: 'no' as const,
      evidence: {
        sourceUrl: 'https://vendor.example/identity',
        checkedAt: '2026-10-01T00:00:00Z',
        validUntil: null,
        status: 'verified' as const
      }
    };
    const detail = {
      ...pkg,
      openSource: null,
      repository: undefined,
      facts: { ...pkg.facts!, security: { identities: [identity] } }
    };
    const md = renderPackageMarkdown(detail as PackageDetail, { siteUrl: 'https://opennavo.example', locale: 'en-US' });
    expect(md).toContain(
      `- ${siteMessage('en-US', 'package.safety.source')}: ${siteMessage('en-US', 'package.safety.sourceNotPublic')} — <https://vendor.example/identity>`
    );
    // A record that only says unknown claims nothing, and a stated open source keeps the row out.
    const unknown = {
      ...detail,
      facts: { ...detail.facts, security: { identities: [{ ...identity, openSource: 'unknown' as const }] } }
    };
    expect(
      renderPackageMarkdown(unknown as PackageDetail, { siteUrl: 'https://opennavo.example', locale: 'en-US' })
    ).not.toContain(siteMessage('en-US', 'package.safety.sourceNotPublic'));
    const alreadyOpen = { ...detail, openSource: { url: 'https://github.com/acme/app', license: 'MIT' } };
    expect(
      renderPackageMarkdown(alreadyOpen as PackageDetail, { siteUrl: 'https://opennavo.example', locale: 'en-US' })
    ).not.toContain(siteMessage('en-US', 'package.safety.sourceNotPublic'));
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

  it('Escapes hostile catalog text and only links http(s) URLs', () => {
    const detail = structuredClone(pkg);
    detail.displayName = 'Evil [x](https://evil.example) `tick` <b>*';
    detail.summary = 'Line one\nLine two';
    detail.homepage = 'javascript:alert(1)';
    detail.installCommand = 'brew install --cask evil`tick';
    detail.facts!.overview.downloadUrl = 'https://vendor.example/a b<c>';
    detail.facts!.prices[0]!.purchaseUrl = 'https://vendor.example/buy)evil';
    detail.facts!.uninstall[0]!.value = 'bad`path[1]';
    const md = renderPackageMarkdown(detail, { siteUrl: 'https://opennavo.example', locale: 'en-US' });
    expect(md.startsWith('# Evil \\[x\\](https://evil.example) \\`tick\\` \\<b\\>\\*\n')).toBe(true);
    expect(md).toContain('Line one Line two');
    expect(md).not.toContain('javascript:');
    expect(md).toContain('``brew install --cask evil`tick``');
    expect(md).toContain('<https://vendor.example/a%20b%3Cc%3E>');
    expect(md).toContain('<https://vendor.example/buy)evil>');
    expect(md).toContain('``bad`path[1]``');
  });

  it('Keeps values from closing a code span and links nothing but http(s)', () => {
    expect(codeSpan('plain')).toBe('`plain`');
    expect(codeSpan('a`b')).toBe('``a`b``');
    expect(codeSpan('`a`')).toBe('`` `a` ``');
    expect(codeSpan('a\nb')).toBe('`a b`');
    expect(mdUrl('https://a.example/b c<d>')).toBe('<https://a.example/b%20c%3Cd%3E>');
    expect(mdUrl('javascript:alert(1)')).toBe('javascript:alert(1)');
  });

  it('Escapes markup in plain text and nests body headings', () => {
    expect(mdText('a*b [c] `d` <e>')).toBe('a\\*b \\[c\\] \\`d\\` \\<e\\>');
    expect(nestedMarkdown('# A\n## B\ntext', 2)).toBe('### A\n#### B\ntext');
    expect(nestedMarkdown('###### F', 3)).toBe('###### F');
  });
});
