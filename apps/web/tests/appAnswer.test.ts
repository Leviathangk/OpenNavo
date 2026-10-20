import { readFileSync } from 'node:fs';
import type { PackageDetail } from '@opennavo/api';
import { describe, expect, it } from 'vitest';
import { evidenceLine, hasZap, installNote, joinSentences, leadSentences } from '../app/utils/appAnswer';
import type { Translate } from '../app/utils/seoTitles';
import { pageOf } from './packageFixtures';

const LOCALES = ['zh-CN', 'en-US'] as const;

// Real messages with vue-i18n {name} interpolation. The lead's sentences are new (T2), so they run in the two
// source languages; the other languages get them from the translators.
function translator(code: (typeof LOCALES)[number]): Translate {
  const messages = JSON.parse(readFileSync(new URL(`../i18n/locales/${code}.json`, import.meta.url), 'utf8'));
  return (key, named = {}) => {
    const template = key.split('.').reduce((node, part) => node?.[part], messages);
    if (typeof template !== 'string') throw new Error(`missing ${code} ${key}`);
    return template.replace(/\{(\w+)\}/g, (_, name: string) => {
      if (!(name in named)) throw new Error(`unfilled {${name}} in ${code} ${key}`);
      return String(named[name]);
    });
  };
}

function app(overrides: Partial<PackageDetail> = {}): PackageDetail {
  return {
    kind: 'cask',
    token: 'editor',
    name: 'Editor',
    displayName: 'Editor',
    names: ['Editor'],
    summary: 'Edits text',
    developer: 'Acme',
    version: '5.2',
    isFont: false,
    autoUpdates: false,
    tap: 'homebrew/cask',
    sourceUrl: 'https://github.com/Homebrew/homebrew-cask/blob/HEAD/Casks/e/editor.rb',
    primaryCategory: { slug: 'developer-tools', name: 'Developer Tools', icon: 'lucide:code' },
    artifacts: { apps: ['Editor.app'], binaries: [], pkgs: [], uninstallNotes: [], entries: [] },
    conflictsWith: { casks: [], formulae: [] },
    supports: { status: 'known', arm64: true, x86_64: true },
    homepage: 'https://vendor.example/',
    platforms: [],
    ...overrides
  } as PackageDetail;
}

describe('T2 lead', () => {
  it.each(LOCALES)('%s says what the app is and who makes it, nothing else', code => {
    const t = translator(code);
    const lead = leadSentences(pageOf(app()), t);
    expect(lead).toHaveLength(2);
    expect(lead[0]).toContain('Editor');
    expect(lead[1]).toContain('Acme');
    // No price, chips or macOS in the lead: those are in the facts table.
    expect(joinSentences(lead, code)).not.toMatch(/macOS|\$|免费|free/i);
    expect(leadSentences(pageOf(app({ developer: null })), t)).toHaveLength(1);
    expect(leadSentences(pageOf(app({ isFont: true, artifacts: { ...app().artifacts, apps: [] } })), t)[0]).toContain(
      'Editor'
    );
    for (const variant of [
      app({ primaryCategory: null }),
      app({ artifacts: { ...app().artifacts, apps: [] } }),
      app({ primaryCategory: null, artifacts: { ...app().artifacts, apps: [] } })
    ])
      expect(leadSentences(pageOf(variant), t)[0]).toContain('Editor');
  });

  it('Names where a link app downloads from', () => {
    const t = translator('en-US');
    const link = app({
      kind: 'link',
      artifacts: { apps: [], binaries: [], pkgs: [], uninstallNotes: [], entries: [] },
      link: {
        homepage: 'https://lumen.app/',
        downloadUrl: 'https://lumen.app/download',
        github: null,
        teamId: null,
        pricing: 'free',
        notes: null,
        adoptedByCask: null
      }
    });
    expect(leadSentences(pageOf(link), t)).toEqual([
      'Editor is a Developer Tools app for Mac.',
      'It is made by Acme.',
      'You download it from lumen.app.'
    ]);
    expect(joinSentences(['一。', '二。'], 'zh-CN')).toBe('一。二。');
    expect(joinSentences(['一。', '二。'], 'zh-Hant')).toBe('一。二。');
    expect(joinSentences(['One.', 'Two.'], 'en-US')).toBe('One. Two.');
  });
});

describe('WP9 install guide', () => {
  it('Describes the install by kind and offers --zap only when files are listed', () => {
    expect(installNote(pageOf(app()))).toEqual({ key: 'installApp' });
    expect(installNote(pageOf(app({ isFont: true })))).toEqual({ key: 'installFont' });
    expect(installNote(pageOf(app({ artifacts: { ...app().artifacts, apps: [], pkgs: ['Driver.pkg'] } })))).toEqual({
      key: 'installPkg',
      pkg: 'Driver.pkg'
    });
    expect(installNote(pageOf(app({ artifacts: { ...app().artifacts, apps: [] } })))).toBeNull();
    expect(hasZap(pageOf(app()))).toBe(false);
    expect(
      hasZap(
        pageOf(
          app({
            artifacts: {
              ...app().artifacts,
              entries: [
                {
                  type: 'zap',
                  phase: 'cleanup',
                  sources: [],
                  target: null,
                  declaration: { zap: [{ trash: '~/Library/Preferences/editor.plist' }] }
                }
              ]
            }
          })
        )
      )
    ).toBe(true);
  });

  it('Writes the evidence line with one closing period', () => {
    const t = translator('en-US');
    expect(
      evidenceLine({ sourceUrl: 'https://vendor.example/pricing/', verifiedAt: '2025-12-01T08:00:00Z' }, t, 'en-US')
    ).toBe('Source: vendor.example/pricing, checked Dec 1, 2025.');
  });
});
