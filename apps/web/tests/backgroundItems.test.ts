import { describe, expect, it } from 'vitest';
import type { PackageSummary } from '@opennavo/api';
import {
  backgroundItemDecision,
  declaringApps,
  groupByDeveloper,
  indexPageNoindex,
  isValidLabel,
  itemTypes,
  labelPath,
  launchdFolders,
  plainText,
  readLabelQuery,
  startupLink,
  uninstallLink,
  usableContent,
  type BackgroundItem,
  type BackgroundOwner
} from '../app/utils/backgroundItems';

const NOW = '2026-10-18T08:00:00Z';

function pkg(token: string, extra: Partial<PackageSummary> = {}): PackageSummary {
  return {
    kind: 'cask',
    token,
    name: token,
    displayName: token,
    version: '1.0.0',
    installs30d: 10,
    autoUpdates: false,
    deprecated: false,
    disabled: false,
    isFont: false,
    isLibrary: false,
    editorChoice: false,
    versionChangedAt: NOW,
    ...extra
  };
}

function owner(token: string, extra: Partial<BackgroundOwner> = {}): BackgroundOwner {
  return {
    package: pkg(token),
    kind: 'launchctl',
    phase: 'uninstall',
    evidence: {
      sourceUrl: `https://github.com/Homebrew/homebrew-cask/blob/HEAD/Casks/${token}.rb`,
      checkedAt: NOW,
      validUntil: null,
      status: 'verified'
    },
    uninstallUrl: `opennavo://uninstall/${token}?preview=1`,
    ...extra
  };
}

function item(label: string, extra: Partial<BackgroundItem> = {}): BackgroundItem {
  return {
    label,
    apps: [owner('example-app')],
    developers: ['Example Inc'],
    shared: false,
    ownership: 'declared_candidates',
    identityChecks: ['signature', 'path', 'version'],
    content: null,
    indexable: false,
    startupUrl: `opennavo://startup?label=${encodeURIComponent(label)}`,
    ...extra
  };
}

const content = (status: 'verified' | 'stale' | 'needs_review' | 'unknown', itemType = 'launch_agent' as const) => ({
  locale: 'en-US' as const,
  description: 'Keeps **Example** up to date. See [the docs](https://vendor.example/docs).',
  itemType,
  commonPaths: ['~/Library/LaunchAgents/com.example.agent.plist'],
  evidence: { sourceUrl: 'https://vendor.example/docs', checkedAt: NOW, validUntil: null, status }
});

describe('Background item labels', () => {
  it('accepts what the API looks up and 404s the rest before any request', () => {
    expect(isValidLabel('com.microsoft.update.agent')).toBe(true);
    expect(isValidLabel('homebrew.mxcl.postgresql@16')).toBe(true);
    expect(isValidLabel('Example Helper')).toBe(true);
    for (const bad of [
      '',
      ' com.example',
      'com.example ',
      'a/b',
      'a\\b',
      'a?b',
      'a#b',
      'a\nb',
      'a\u0007b',
      'x'.repeat(256)
    ])
      expect(isValidLabel(bad)).toBe(false);
    // 255 bytes is the limit, counted in UTF-8 like the API does.
    expect(isValidLabel('é'.repeat(127))).toBe(true);
    expect(isValidLabel('é'.repeat(128))).toBe(false);
  });

  it('gives each label one address, keeping the characters a path may hold', () => {
    expect(labelPath('com.microsoft.update.agent')).toBe('/background-items/com.microsoft.update.agent');
    expect(labelPath('homebrew.mxcl.postgresql@16')).toBe('/background-items/homebrew.mxcl.postgresql@16');
    expect(labelPath('com.example helper')).toBe('/background-items/com.example%20helper');
    expect(labelPath('a+b:c')).toBe('/background-items/a+b:c');
    expect(labelPath('100%')).toBe('/background-items/100%25');
  });

  it('reads ?q= as the API takes it and refuses a repeated one', () => {
    expect(readLabelQuery(undefined)).toBe('');
    expect(readLabelQuery(null)).toBe('');
    expect(readLabelQuery('  google ')).toBe('google');
    expect(readLabelQuery(['a', 'b'])).toBeUndefined();
    const long = readLabelQuery('中'.repeat(100))!;
    expect(new TextEncoder().encode(long).length).toBeLessThanOrEqual(255);
    expect(long).toBe('中'.repeat(85));
  });
});

describe('What an item page may say', () => {
  it('shows a description only while it is verified or stale, never under review or unknown', () => {
    expect(usableContent(item('a', { content: content('verified') }))).not.toBeNull();
    expect(usableContent(item('a', { content: content('stale') }))).not.toBeNull();
    expect(usableContent(item('a', { content: content('needs_review') }))).toBeNull();
    expect(usableContent(item('a', { content: content('unknown') }))).toBeNull();
    expect(usableContent(item('a'))).toBeNull();
  });

  it('types an item from its verified content, else from what the casks declare', () => {
    expect(itemTypes(item('a', { content: content('verified', 'launch_agent') }))).toEqual(['launch_agent']);
    // Under review: the declared kinds decide, not the unverified type.
    expect(itemTypes(item('a', { content: content('needs_review', 'launch_agent') }))).toEqual(['launchd']);
    expect(
      itemTypes(item('a', { apps: [owner('x', { kind: 'login_item' }), owner('y', { kind: 'pkgutil' })] }))
    ).toEqual(['login_item', 'package_receipt']);
    expect(itemTypes(item('a', { apps: [] }))).toEqual(['unknown']);
  });

  it('names the usual launchd folders for the type it has', () => {
    expect(launchdFolders(['launch_agent'])).toEqual(['~/Library/LaunchAgents', '/Library/LaunchAgents']);
    expect(launchdFolders(['launch_daemon'])).toEqual(['/Library/LaunchDaemons']);
    expect(launchdFolders(['launchd'])).toHaveLength(3);
  });

  it('lists each declaring app once with all of its declarations', () => {
    const apps = declaringApps(
      item('a', {
        apps: [owner('zoom'), owner('zoom', { phase: 'zap' }), owner('google-chrome', { kind: 'login_item' })]
      })
    );
    expect(apps.map(app => app.package.token)).toEqual(['zoom', 'google-chrome']);
    expect(apps[0]!.declarations.map(entry => entry.phase)).toEqual(['uninstall', 'zap']);
  });

  it('turns Markdown into one plain line for the meta description', () => {
    expect(plainText(content('verified').description)).toBe('Keeps Example up to date. See the docs.');
  });
});

describe('Index groups', () => {
  it('groups by developer, then shared labels, then labels whose apps name nobody', () => {
    const collator = new Intl.Collator('en-US');
    const groups = groupByDeveloper(
      [
        item('com.google.keystone.agent', { developers: ['Google'] }),
        item('com.shared.helper', { developers: ['Alpha', 'Beta'] }),
        item('com.adobe.agent', { developers: ['Adobe'] }),
        item('org.unnamed', { developers: [] }),
        item('com.google.updater', { developers: ['Google'] })
      ],
      collator
    );
    expect(groups.map(group => group.developer ?? (group.several ? 'several' : 'unknown'))).toEqual([
      'Adobe',
      'Google',
      'several',
      'unknown'
    ]);
    expect(groups[1]!.items.map(entry => entry.label)).toEqual(['com.google.keystone.agent', 'com.google.updater']);
  });

  it('keeps searches and pages without a ready entry out of the index', () => {
    expect(indexPageNoindex([item('a', { indexable: true })], '')).toBe(false);
    expect(indexPageNoindex([item('a', { indexable: true })], 'google')).toBe(true);
    expect(indexPageNoindex([item('a')], '')).toBe(true);
    expect(indexPageNoindex([], '')).toBe(true);
  });
});

describe('Deep links and the page decision', () => {
  it('only passes on the preview links the client accepts', () => {
    expect(startupLink(item('com.example.agent'))).toBe('opennavo://startup?label=com.example.agent');
    expect(startupLink(item('a', { startupUrl: 'javascript:alert(1)' }))).toBeNull();
    expect(startupLink(item('a', { startupUrl: 'opennavo://startup?label=a&execute=1' }))).toBeNull();
    expect(uninstallLink(owner('zoom'))).toBe('opennavo://uninstall/zoom?preview=1');
    expect(uninstallLink(owner('zoom', { uninstallUrl: 'opennavo://uninstall/zoom' }))).toBeNull();
    expect(uninstallLink(owner('zoom', { uninstallUrl: 'opennavo://uninstall/Zoom?preview=1' }))).toBeNull();
  });

  it('follows indexable, with one canonical path and only this language as alternate', () => {
    expect(backgroundItemDecision(item('a', { indexable: true }), 'de-DE', '/de/background-items/a')).toEqual({
      robots: 'index, follow',
      canonicalPath: '/de/background-items/a',
      hreflang: ['de-DE'],
      inSitemap: true,
      announce: false,
      reason: 'background_item'
    });
    const closed = backgroundItemDecision(item('a'), 'en-US', '/background-items/a');
    expect(closed.robots).toBe('noindex, follow');
    expect(closed.hreflang).toEqual([]);
    expect(closed.inSitemap).toBe(false);
  });
});
