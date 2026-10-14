import { readFileSync } from 'node:fs';
import type { PackageDetail } from '@opennavo/api';
import { selectPlural } from '@opennavo/shared';
import { describe, expect, it } from 'vitest';
import { chipSupport, dataLocations, FAQ_LIMIT, faqItems, uninstallSteps, zapPaths } from '../app/utils/packageFaq';
import type { FaqTranslate } from '../app/utils/packageFaq';

const LOCALES = ['zh-CN', 'en-US', 'ja-JP', 'es-ES', 'pt-BR', 'ru-RU'] as const;
type Code = (typeof LOCALES)[number];

// Real messages, plural forms picked like vue-i18n, so every template runs in all six languages.
function translator(code: Code): FaqTranslate {
  const messages = JSON.parse(readFileSync(new URL(`../i18n/locales/${code}.json`, import.meta.url), 'utf8'));
  return (key, named = {}, plural) => {
    const template = key.split('.').reduce((node, part) => node?.[part], messages);
    if (typeof template !== 'string') throw new Error(`missing ${code} ${key}`);
    const form = plural === undefined ? template : selectPlural(template, plural, code);
    if (plural === undefined && form.includes('|')) throw new Error(`plural message without a count: ${code} ${key}`);
    return form.replace(/\{(\w+)\}/g, (_, name: string) => {
      if (!(name in named)) throw new Error(`unfilled {${name}} in ${code} ${key}`);
      return String(named[name]);
    });
  };
}

const entry = (type: string, phase: 'install' | 'uninstall' | 'cleanup', declaration: Record<string, unknown>) => ({
  type,
  phase,
  sources: [],
  target: null,
  declaration
});

// Shaped like Docker Desktop's live record.
function docker(overrides: Partial<PackageDetail> = {}): PackageDetail {
  return {
    kind: 'cask',
    token: 'docker-desktop',
    name: 'Docker Desktop',
    displayName: 'Docker Desktop',
    version: '4.95.0,242831',
    tap: 'homebrew/cask',
    isFont: false,
    autoUpdates: true,
    installs: { d30: 28210, d90: 115223, d365: 527555 },
    downloadUrl: 'https://desktop.docker.com/mac/main/arm64/242831/Docker.dmg',
    downloadSha256: 'a'.repeat(64),
    homebrewStatus: { state: 'active', reason: null, date: null },
    supports: { status: 'known', arm64: true, x86_64: true, requiresRosetta: false },
    platforms: [
      {
        tag: 'arm64_sonoma',
        macos: '14',
        arch: 'arm64',
        version: '4.95.0,242831',
        downloadUrl: 'https://desktop.docker.com/arm64/Docker.dmg'
      },
      {
        tag: 'sonoma',
        macos: '14',
        arch: 'x86_64',
        version: '4.95.0,242831',
        downloadUrl: 'https://desktop.docker.com/amd64/Docker.dmg'
      }
    ],
    dependsOn: {
      macos: '>= 14',
      arch: [],
      formulae: [],
      casks: [],
      requirements: { macos: { '>=': ['14'] } }
    },
    conflictsWith: { casks: ['rancher'], formulae: [] },
    versionCommittedAt: null,
    artifacts: {
      apps: ['Docker.app'],
      binaries: [
        'docker',
        'docker-compose',
        'kubectl.docker',
        'docker-credential-desktop',
        'docker-credential-osxkeychain',
        'docker-credential-ecr-login'
      ],
      pkgs: [],
      entries: [
        entry('app', 'install', { app: ['Docker.app'] }),
        entry('uninstall', 'uninstall', {
          uninstall: [
            {
              delete: [
                '/Library/PrivilegedHelperTools/com.docker.socket',
                '/Library/PrivilegedHelperTools/com.docker.vmnetd'
              ],
              launchctl: ['com.docker.helper', 'com.docker.socket', 'com.docker.vmnetd'],
              quit: ['com.docker.docker', 'com.electron.dockerdesktop'],
              rmdir: '~/.docker/bin'
            }
          ]
        }),
        entry('zap', 'cleanup', {
          zap: [
            {
              rmdir: ['~/Library/Caches/com.plausiblelabs.crashreporter.data', '~/Library/Caches/KSCrashReports'],
              trash: [
                '~/.docker',
                '~/Library/Application Support/com.apple.sharedfilelist/com.apple.LSSharedFileList.ApplicationRecentDocuments/com.docker.helper.sfl*',
                '~/Library/Application Support/Docker Desktop',
                '~/Library/Caches/com.docker.docker',
                '~/Library/Containers/com.docker.docker',
                '~/Library/Group Containers/group.com.docker',
                '~/Library/Logs/Docker Desktop',
                '~/Library/Preferences/com.electron.dockerdesktop.plist',
                '~/Library/Preferences/com.docker.docker.plist'
              ]
            }
          ]
        })
      ]
    },
    ...overrides
  } as PackageDetail;
}

const appStore = {
  name: 'Keka',
  url: 'https://apps.apple.com/us/app/keka/id470158793?mt=12',
  bundleId: 'com.aone.keka',
  price: 6.49,
  currency: 'USD',
  region: 'US',
  languages: ['EN', 'JA', 'ZH'],
  checkedAt: '2025-12-01T08:00:00Z'
};

describe('App questions', () => {
  it('Answers Docker Desktop from its Homebrew record, in search order', () => {
    const items = faqItems(docker(), translator('en-US'), 'en-US');
    expect(items.map(item => item.key)).toEqual(['safety', 'uninstall', 'chips', 'data', 'version', 'extras']);
    const answer = (key: string) => items.find(item => item.key === key)!.answer;
    expect(items[0]!.question).toBe('Is Docker Desktop safe?');
    expect(answer('safety')).toBe(
      'Docker Desktop is in Homebrew’s official catalog and has not been disabled. Since September 2026, Homebrew disables apps that fail macOS Gatekeeper checks. When installing, Homebrew checks the download from desktop.docker.com against its SHA-256 checksum. It was installed 28,210 times through Homebrew in the last 30 days.'
    );
    expect(answer('uninstall')).toBe(
      'Run `brew uninstall --cask docker-desktop`. It quits the app, stops 3 background services, and deletes 2 privileged helpers. To delete its settings and caches as well, run `brew uninstall --zap --cask docker-desktop`. That also deletes 11 files and folders, including `~/.docker`.'
    );
    expect(answer('chips')).toBe(
      'Yes. Homebrew downloads separate builds for Apple silicon and Intel Macs. It needs macOS 14 or later.'
    );
    expect(answer('data')).toBe(
      'Its settings are in `~/Library/Preferences/com.docker.docker.plist`. Its data is in `~/.docker`, `~/Library/Application Support/Docker Desktop`, and `~/Library/Containers/com.docker.docker`. These locations come from Homebrew’s clean-up list.'
    );
    expect(answer('version')).toBe(
      'The latest version is 4.95.0. Docker Desktop updates itself; to update it with Homebrew, run `brew upgrade --cask --greedy docker-desktop`.'
    );
    expect(answer('extras')).toBe(
      'It also adds 6 command-line tools: `docker`, `docker-compose`, `kubectl.docker`, `docker-credential-desktop`, `docker-credential-osxkeychain`, and `docker-credential-ecr-login`. It cannot be installed alongside `rancher`.'
    );
  });

  it.each(LOCALES)('%s fills every template it uses', code => {
    const full = docker({
      price: undefined,
      appStore,
      openSource: {
        repo: 'acme/docker',
        url: 'https://github.com/acme/docker',
        license: 'Apache-2.0',
        archived: true,
        checkedAt: '2025-12-01T08:00:00Z'
      },
      versionCommittedAt: '2025-12-01T08:00:00Z',
      conflictsWith: { casks: ['rancher', 'docker-desktop@edge'], formulae: [] },
      artifacts: {
        ...docker().artifacts,
        binaries: ['a', 'b', 'c', 'd', 'e', 'f', 'g'],
        pkgs: ['Docker.pkg'],
        entries: [
          ...docker().artifacts.entries!,
          entry('qlplugin', 'install', {}),
          entry('uninstall', 'uninstall', {
            uninstall: [{ login_item: 'Docker', pkgutil: 'com.docker.pkg', kext: 'com.docker.kext' }]
          })
        ]
      },
      alternatives: [{ package: { displayName: 'OrbStack' }, reasons: ['lighter'], limits: [], bestFor: null }]
    } as Partial<PackageDetail>);
    const items = faqItems(full, translator(code), code);
    expect(items).toHaveLength(FAQ_LIMIT);
    for (const item of items) {
      expect(item.question).toContain('Docker Desktop');
      expect(item.answer).not.toMatch(/\{\w+\}|\||\.\.(\s|$)/);
    }
    // English pages do not ask about English.
    expect(items.some(item => item.key === 'language')).toBe(code !== 'en-US');
    // Disabled, deprecated and Rosetta wording, and the one-file clean-up.
    const disabled = faqItems(
      docker({
        homebrewStatus: { state: 'disabled', reason: 'fails_gatekeeper_check', date: '2026-09-01' }
      }),
      translator(code),
      code
    );
    expect(disabled[0]!.answer).not.toContain('desktop.docker.com');
    const deprecated = faqItems(
      docker({ homebrewStatus: { state: 'deprecated', reason: 'something custom', date: null } }),
      translator(code),
      code
    );
    expect(deprecated[0]!.answer).toContain('desktop.docker.com');
    const single = docker({
      supports: { status: 'known', arm64: true, x86_64: true, requiresRosetta: true },
      installs: { d30: 1, d90: 1, d365: 1 },
      artifacts: {
        ...docker().artifacts,
        entries: [
          entry('zap', 'cleanup', {
            zap: [{ trash: '~/Library/Preferences/com.docker.docker.plist' }]
          })
        ]
      }
    });
    expect(faqItems(single, translator(code), code).map(item => item.key)).toContain('uninstall');
  });

  it('Says why Homebrew disabled an app and nothing it no longer checks', () => {
    const t = translator('en-US');
    // A past year, so the formatted date always includes it.
    const [safety] = faqItems(
      docker({
        homebrewStatus: { state: 'disabled', reason: 'fails_gatekeeper_check', date: '2025-09-01' }
      }),
      t,
      'en-US'
    );
    expect(safety!.answer).toBe(
      'Homebrew has disabled Docker Desktop (it fails macOS Gatekeeper checks), so it can no longer be installed with Homebrew. It was disabled on Sep 1, 2025.'
    );
    const [custom] = faqItems(
      docker({ homebrewStatus: { state: 'disabled', reason: 'is 32-bit only', date: null } }),
      t,
      'en-US'
    );
    expect(custom!.answer).toContain('(the Homebrew definition explains why)');
    // Third-party taps and records without a status get no safety answer.
    expect(faqItems(docker({ tap: 'someone/tap' }), t, 'en-US')[0]!.key).toBe('uninstall');
    expect(faqItems(docker({ homebrewStatus: undefined }), t, 'en-US')[0]!.key).toBe('uninstall');
    const noChecksum = faqItems(
      docker({ downloadSha256: null, installs: { d30: 0, d90: 0, d365: 0 } }),
      t,
      'en-US'
    )[0]!;
    expect(noChecksum.answer).toContain(
      'The download comes from desktop.docker.com; Homebrew does not verify a checksum for it.'
    );
    expect(noChecksum.answer).not.toContain('installed');
  });

  it('Describes the App Store edition as such and never infers free from it', () => {
    const t = translator('zh-CN');
    const items = faqItems(docker({ appStore }), t, 'zh-CN');
    const price = items.find(item => item.key === 'price')!;
    expect(price.question).toBe('Docker Desktop 免费吗？');
    expect(price.answer).toBe(
      'Mac App Store（美国）上的“Keka”售价 US$6.49。Homebrew 安装的是开发者网站提供的版本，价格和功能可能与 App Store 版不同。来源：apps.apple.com，核查于 2025年12月1日。'
    );
    const free = faqItems(docker({ appStore: { ...appStore, price: 0 } }), translator('en-US'), 'en-US');
    expect(free.find(item => item.key === 'price')!.answer).toMatch(
      /^“Keka” is free to download on the Mac App Store\. Homebrew installs the version/
    );
    expect(items.find(item => item.key === 'language')!.answer).toBe(
      'Mac App Store 页面列出的界面语言包括中文。来源：apps.apple.com，核查于 2025年12月1日。'
    );
    const ru = faqItems(
      docker({ appStore: { ...appStore, languages: ['EN', 'DE', 'FR'] } }),
      translator('ru-RU'),
      'ru-RU'
    );
    expect(ru.find(item => item.key === 'language')!.answer).toMatch(
      /^На странице Mac App Store указано 3 языка интерфейса/
    );
    // Verified price evidence wins over the App Store listing.
    const verified = {
      type: 'free' as const,
      amount: null,
      currency: null,
      region: null,
      plan: null,
      period: null,
      appVersion: null,
      sourceUrl: 'https://vendor.example/pricing',
      method: 'vendor_site' as const,
      verifiedAt: '2025-12-01T08:00:00Z',
      stale: false,
      offer: true
    };
    const priced = faqItems(docker({ appStore, price: verified }), translator('en-US'), 'en-US');
    expect(priced.find(item => item.key === 'price')!.answer).toBe(
      'Yes, Docker Desktop is free to use. Source: vendor.example/pricing, checked Dec 1, 2025.'
    );
  });

  it('Puts beta channels in the version answer and keeps conflicts out of an empty extras answer', () => {
    const t = translator('en-US');
    const keka = docker({
      conflictsWith: { casks: ['docker-desktop@beta'], formulae: [] },
      artifacts: { ...docker().artifacts, binaries: [] }
    });
    const items = faqItems(keka, t, 'en-US');
    expect(items.map(item => item.key)).not.toContain('extras');
    expect(items.find(item => item.key === 'version')!.answer).toBe(
      'The latest version is 4.95.0. Docker Desktop updates itself; to update it with Homebrew, run `brew upgrade --cask --greedy docker-desktop`. Homebrew also offers `docker-desktop@beta`, which cannot be installed alongside it.'
    );
    // Chinese lists the uninstall steps as a plain enumeration.
    const zh = faqItems(docker(), translator('zh-CN'), 'zh-CN');
    expect(zh.find(item => item.key === 'uninstall')!.answer).toContain(
      '它会退出应用、停止 3 个后台服务、删除 2 个特权辅助程序。'
    );
    expect(zh.find(item => item.key === 'version')!.answer).toMatch(/^最新版本是 4\.95\.0。/);
  });

  it('Publishes the license GitHub reports, with archived repositories marked', () => {
    const items = faqItems(
      docker({
        openSource: {
          repo: 'gnachman/iTerm2',
          url: 'https://github.com/gnachman/iTerm2',
          license: 'GPL-2.0',
          archived: false,
          checkedAt: '2025-12-01T08:00:00Z'
        }
      }),
      translator('en-US'),
      'en-US'
    );
    expect(items.find(item => item.key === 'openSource')!.answer).toBe(
      'Docker Desktop’s source code is public on GitHub (gnachman/iTerm2) under the GPL-2.0 license.'
    );
  });

  it('Leaves out questions without facts, and fonts skip chips and extras', () => {
    const t = translator('en-US');
    const bare = docker({
      homebrewStatus: undefined,
      supports: { status: 'unknown', arm64: false, x86_64: false },
      conflictsWith: { casks: [], formulae: [] },
      version: 'latest',
      artifacts: { apps: ['Docker.app'], binaries: [], pkgs: [], entries: [] }
    });
    const items = faqItems(bare, t, 'en-US');
    expect(items.map(item => item.key)).toEqual(['uninstall']);
    expect(items[0]!.answer).toBe('Run `brew uninstall --cask docker-desktop`.');
    const font = docker({ isFont: true });
    expect(faqItems(font, t, 'en-US').map(item => item.key)).toEqual(['safety', 'uninstall', 'data', 'version']);
  });
});

describe('Record readers', () => {
  it('Does not claim a shared installer for Raycast’s different architecture versions', () => {
    const sample = JSON.parse(
      readFileSync(new URL('../../server/testdata/homebrew/raycast.json', import.meta.url), 'utf8')
    );
    const intel = sample.variations.sonoma;
    const pkg = docker({
      version: sample.version,
      platforms: [
        {
          tag: 'arm64_sonoma',
          macos: '14',
          arch: 'arm64',
          version: sample.version,
          downloadUrl: sample.url
        },
        {
          tag: 'sonoma',
          macos: '14',
          arch: 'x86_64',
          version: intel.version,
          downloadUrl: intel.url
        }
      ]
    });
    expect(chipSupport(pkg)).toBeNull();
    expect(faqItems(pkg, translator('zh-CN'), 'zh-CN').find(item => item.key === 'chips')).toBeUndefined();
  });

  it('Requires explicit matching downloads on both chips before claiming a shared installer', () => {
    const [arm, intel] = docker().platforms!;
    expect(chipSupport(docker({ platforms: [arm!] }))).toBeNull();
    expect(chipSupport(docker({ platforms: [intel!] }))).toBeNull();
    expect(chipSupport(docker({ platforms: [{ ...arm!, downloadUrl: null }, intel!] }))).toBeNull();
    // One matching variant does not make every installer shared.
    expect(chipSupport(docker({ platforms: [arm!, intel!, { ...intel!, downloadUrl: arm!.downloadUrl }] }))).toBeNull();
    expect(
      chipSupport(
        docker({
          platforms: [arm!, { ...intel!, downloadUrl: arm!.downloadUrl }, { ...arm!, downloadUrl: null }]
        })
      )
    ).toBeNull();
  });

  it('Reads uninstall steps and clean-up paths', () => {
    expect(uninstallSteps(docker())).toEqual({
      quits: true,
      services: 3,
      loginItems: 0,
      helpers: 2,
      receipts: 0,
      kexts: 0
    });
    expect(zapPaths(docker())).toHaveLength(11);
    expect(dataLocations(docker())).toEqual({
      settings: '~/Library/Preferences/com.docker.docker.plist',
      folders: ['~/.docker', '~/Library/Application Support/Docker Desktop', '~/Library/Containers/com.docker.docker']
    });
    expect(dataLocations(docker({ artifacts: { apps: [], binaries: [], pkgs: [] } }))).toEqual({
      settings: undefined,
      folders: []
    });
  });

  it('Says “separate builds” only when each chip has its own download', () => {
    expect(chipSupport(docker())).toBe('separate');
    expect(chipSupport(docker({ platforms: [] }))).toBeNull();
    const same = docker().platforms!.map(platform => ({
      ...platform,
      downloadUrl: 'https://desktop.docker.com/universal/Docker.dmg'
    }));
    expect(chipSupport(docker({ platforms: same }))).toBe('shared');
    const old = docker().platforms!.map(platform => ({ ...platform, version: '4.0.0' }));
    expect(chipSupport(docker({ platforms: old }))).toBeNull();
    expect(
      chipSupport(docker({ supports: { status: 'known', arm64: true, x86_64: true, requiresRosetta: true } }))
    ).toBe('rosetta');
    expect(chipSupport(docker({ supports: { status: 'known', arm64: true, x86_64: false } }))).toBe('armOnly');
    expect(chipSupport(docker({ supports: { status: 'known', arm64: false, x86_64: true } }))).toBe('intelOnly');
    expect(chipSupport(docker({ supports: { status: 'unknown', arm64: true, x86_64: true } }))).toBeNull();
  });
});
