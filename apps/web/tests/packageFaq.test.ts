import { readFileSync } from 'node:fs';
import type { PackageDetail } from '@opennavo/api';
import { selectPlural } from '@opennavo/shared';
import { describe, expect, it } from 'vitest';
import { chipSupport, dataLocations, FAQ_LIMIT, faqItems, uninstallSteps, zapPaths } from '../app/utils/packageFaq';
import type { FaqTranslate } from '../app/utils/packageFaq';
import { formatDate } from '../app/utils/packageDate';
import { evidence, pageOf } from './packageFixtures';
import { faqItems as mainFaqItems } from './fixtures/packageFaq.main';
import { slimPackage } from '../app/utils/packageFacts';

const LOCALES = ['zh-CN', 'en-US', 'zh-Hant', 'ja-JP', 'es-ES', 'pt-BR', 'ru-RU', 'de-DE', 'fr-FR', 'it-IT'] as const;
type Code = (typeof LOCALES)[number];

// Real messages, plural forms picked like vue-i18n, so every template runs in all ten languages.
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
    sourceUrl: 'https://github.com/Homebrew/homebrew-cask/blob/HEAD/Casks/d/docker-desktop.rb',
    names: ['Docker Desktop'],
    isFont: false,
    autoUpdates: true,
    installs: { d30: 28210, d90: 115223, d365: 527555 },
    downloadUrl: 'https://desktop.docker.com/mac/main/arm64/242831/Docker.dmg',
    downloadSha256: 'a'.repeat(64),
    homebrewStatus: { state: 'active', reason: null, date: null },
    supports: { status: 'known', arm64: true, x86_64: true, requiresRosetta: false },
    platforms: [
      // Each variant carries the minimum from its own depends_on, as the API's platforms do.
      {
        tag: 'arm64_sonoma',
        macos: '14',
        arch: 'arm64',
        version: '4.95.0,242831',
        minMacos: '14',
        downloadUrl: 'https://desktop.docker.com/arm64/Docker.dmg'
      },
      {
        tag: 'sonoma',
        macos: '14',
        arch: 'x86_64',
        version: '4.95.0,242831',
        minMacos: '14',
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
    const items = faqItems(pageOf(docker()), translator('en-US'), 'en-US');
    expect(items.map(item => item.key)).toEqual(['safety', 'uninstall', 'chips', 'data', 'version', 'extras']);
    const answer = (key: string) => items.find(item => item.key === key)!.answer;
    expect(items[0]!.question).toBe('Is Docker Desktop safe?');
    expect(answer('safety')).toBe(
      'Docker Desktop is in Homebrew’s official catalog and has not been disabled. Since September 2026, Homebrew disables apps that fail macOS Gatekeeper checks. When installing, Homebrew checks the download from desktop.docker.com against its SHA-256 checksum. It was installed 28,210 times through Homebrew in the last 30 days.'
    );
    expect(answer('uninstall')).toBe(
      'Run `brew uninstall --cask docker-desktop`. It quits the app, stops 3 background services, and deletes 2 privileged helpers. To delete its settings and caches as well, run `brew uninstall --zap --cask docker-desktop`. ' +
        translator('en-US')('package.guide.zap') +
        ' That also deletes 11 files and folders, including `~/.docker`.'
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
    const items = faqItems(pageOf(full), translator(code), code);
    expect(items).toHaveLength(FAQ_LIMIT);
    for (const item of items) {
      expect(item.question).toContain('Docker Desktop');
      expect(item.answer).not.toMatch(/\{\w+\}|\||\.\.(\s|$)/);
    }
    // English pages do not ask about English.
    expect(items.some(item => item.key === 'language')).toBe(code !== 'en-US');
    // Disabled, deprecated and Rosetta wording, and the one-file clean-up.
    const disabled = faqItems(
      pageOf(
        docker({
          homebrewStatus: { state: 'disabled', reason: 'fails_gatekeeper_check', date: '2026-09-01' }
        })
      ),
      translator(code),
      code
    );
    expect(disabled[0]!.answer).not.toContain('desktop.docker.com');
    const deprecated = faqItems(
      pageOf(docker({ homebrewStatus: { state: 'deprecated', reason: 'something custom', date: null } })),
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
    expect(faqItems(pageOf(single), translator(code), code).map(item => item.key)).toContain('uninstall');
  });

  it('Says why Homebrew disabled an app and nothing it no longer checks', () => {
    const t = translator('en-US');
    // A past year, so the formatted date always includes it.
    const [safety] = faqItems(
      pageOf(
        docker({
          homebrewStatus: { state: 'disabled', reason: 'fails_gatekeeper_check', date: '2025-09-01' }
        })
      ),
      t,
      'en-US'
    );
    expect(safety!.answer).toBe(
      'Homebrew has disabled Docker Desktop (it fails macOS Gatekeeper checks), so it can no longer be installed with Homebrew. It was disabled on Sep 1, 2025.'
    );
    const [custom] = faqItems(
      pageOf(docker({ homebrewStatus: { state: 'disabled', reason: 'is 32-bit only', date: null } })),
      t,
      'en-US'
    );
    expect(custom!.answer).toContain('(the Homebrew definition explains why)');
    // Third-party taps and records without a status get no safety answer.
    expect(faqItems(pageOf(docker({ tap: 'someone/tap' })), t, 'en-US')[0]!.key).toBe('uninstall');
    expect(faqItems(pageOf(docker({ homebrewStatus: undefined })), t, 'en-US')[0]!.key).toBe('uninstall');
    const noChecksum = faqItems(
      pageOf(docker({ downloadSha256: null, installs: { d30: 0, d90: 0, d365: 0 } })),
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
    const items = faqItems(pageOf(docker({ appStore })), t, 'zh-CN');
    const price = items.find(item => item.key === 'price')!;
    expect(price.question).toBe('Docker Desktop 免费吗？');
    expect(price.answer).toBe(
      'Mac App Store（美国）上的“Keka”售价 US$6.49。Homebrew 安装的是开发者网站提供的版本，价格和功能可能与 App Store 版不同。来源：apps.apple.com，核查于 2025年12月1日。'
    );
    const free = faqItems(pageOf(docker({ appStore: { ...appStore, price: 0 } })), translator('en-US'), 'en-US');
    expect(free.find(item => item.key === 'price')!.answer).toMatch(
      /^“Keka” is free to download on the Mac App Store\. Homebrew installs the version/
    );
    expect(items.find(item => item.key === 'language')!.answer).toBe(
      'Mac App Store 页面列出的界面语言包括中文。来源：apps.apple.com，核查于 2025年12月1日。'
    );
    const ru = faqItems(
      pageOf(docker({ appStore: { ...appStore, languages: ['EN', 'DE', 'FR'] } })),
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
    const priced = faqItems(pageOf(docker({ appStore, price: verified })), translator('en-US'), 'en-US');
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
    const items = faqItems(pageOf(keka), t, 'en-US');
    expect(items.map(item => item.key)).not.toContain('extras');
    expect(items.find(item => item.key === 'version')!.answer).toBe(
      'The latest version is 4.95.0. Docker Desktop updates itself; to update it with Homebrew, run `brew upgrade --cask --greedy docker-desktop`. Homebrew also offers `docker-desktop@beta`, which cannot be installed alongside it.'
    );
    // Chinese lists the uninstall steps as a plain enumeration.
    const zh = faqItems(pageOf(docker()), translator('zh-CN'), 'zh-CN');
    expect(zh.find(item => item.key === 'uninstall')!.answer).toContain(
      '它会退出应用、停止 3 个后台服务、删除 2 个特权辅助程序。'
    );
    expect(zh.find(item => item.key === 'version')!.answer).toMatch(/^最新版本是 4\.95\.0。/);
  });

  it('Publishes the license GitHub reports, with archived repositories marked', () => {
    const items = faqItems(
      pageOf(
        Object.assign(
          docker({
            openSource: {
              repo: 'gnachman/iTerm2',
              url: 'https://github.com/gnachman/iTerm2',
              license: 'GPL-2.0',
              archived: false,
              checkedAt: '2025-12-01T08:00:00Z'
            }
          }),
          {
            repository: {
              url: 'https://github.com/gnachman/iTerm2',
              stars: 12345,
              pushedAt: '2026-10-01T00:00:00Z',
              archived: false,
              signalsCheckedAt: '2026-10-02T00:00:00Z'
            }
          }
        )
      ),
      translator('en-US'),
      'en-US'
    );
    expect(items.find(item => item.key === 'openSource')!.answer).toBe(
      'Docker Desktop’s source code is public on GitHub (gnachman/iTerm2) under the GPL-2.0 license.'
    );
  });

  it('distinguishes OSI repositories, public repositories without an OSI fact, and no repository', () => {
    const osi = faqItems(
      pageOf(
        Object.assign(
          docker({
            openSource: {
              repo: 'acme/osi',
              url: 'https://github.com/acme/osi',
              license: 'MIT',
              archived: false,
              checkedAt: '2025-12-01T08:00:00Z'
            }
          }),
          { repository: { url: 'https://github.com/acme/osi', stars: 999, archived: false } }
        )
      ),
      translator('en-US'),
      'en-US'
    );
    expect(osi.find(item => item.key === 'openSource')!.answer).toContain('under the MIT license.');
    expect(osi.find(item => item.key === 'openSource')!.answer).not.toMatch(/★|999/);

    const publicOnly = faqItems(
      pageOf(
        Object.assign(docker({ openSource: null }), {
          repository: {
            url: 'https://github.com/acme/public-only',
            stars: 1000,
            archived: true,
            signalsCheckedAt: '2026-10-02T00:00:00Z'
          }
        })
      ),
      translator('en-US'),
      'en-US'
    );
    expect(publicOnly.find(item => item.key === 'openSource')!.answer).toContain(
      'has a public GitHub repository (https://github.com/acme/public-only), but OpenNavo has not verified an OSI license.'
    );
    expect(publicOnly.find(item => item.key === 'openSource')!.answer).toContain('repository is archived');

    const neither = faqItems(pageOf(docker({ openSource: null })), translator('en-US'), 'en-US');
    expect(neither.some(item => item.key === 'openSource')).toBe(false);
  });

  it('preserves main’s OSI archived answer independently of new repository signals', () => {
    const sample = docker({
      openSource: {
        repo: 'acme/osi',
        url: 'https://github.com/acme/osi',
        license: 'MIT',
        archived: true,
        checkedAt: '2025-12-01T08:00:00Z'
      }
    });
    const answer = faqItems(pageOf(sample), translator('en-US'), 'en-US').find(
      item => item.key === 'openSource'
    )!.answer;
    expect(answer).toContain('repository is archived');
    expect(answer).not.toContain('Repository data checked');
    const current = Object.assign(sample, { repository: { archived: false } });
    expect(
      faqItems(pageOf(current), translator('en-US'), 'en-US').find(item => item.key === 'openSource')!.answer
    ).toContain('repository is archived');
  });

  it.each([
    {
      status: 'stale',
      checkedAt: '2025-12-01T08:00:00Z',
      openSource: 'yes',
      repositoryUrl: 'https://github.com/acme/osi'
    },
    {
      status: 'needs_review',
      checkedAt: '2025-12-01T08:00:00Z',
      openSource: 'yes',
      repositoryUrl: 'https://github.com/acme/osi'
    },
    { status: 'verified', checkedAt: null, openSource: 'yes', repositoryUrl: 'https://github.com/acme/osi' },
    {
      status: 'verified',
      checkedAt: '2025-12-01T08:00:00Z',
      openSource: 'unknown',
      repositoryUrl: 'https://github.com/acme/osi'
    },
    {
      status: 'verified',
      checkedAt: '2025-12-01T08:00:00Z',
      openSource: 'yes',
      repositoryUrl: 'https://github.com/acme/other'
    }
  ] as const)('rejects OSI data without a matching verified identity: %j', proof => {
    const pkg = pageOf(
      docker({
        openSource: {
          repo: 'acme/osi',
          url: 'https://github.com/acme/osi',
          license: 'MIT',
          archived: false,
          checkedAt: '2025-12-01T08:00:00Z'
        }
      })
    );
    const identity = pkg.facts.security!.identities[0]!;
    Object.assign(identity, { openSource: proof.openSource, repositoryUrl: proof.repositoryUrl });
    Object.assign(identity.evidence, { status: proof.status, checkedAt: proof.checkedAt });
    pkg.repository = { url: 'https://github.com/acme/osi', stars: 12345 };
    expect(faqItems(pkg, translator('en-US'), 'en-US').some(item => item.key === 'openSource')).toBe(false);
    pkg.facts.security!.identities = [];
    expect(faqItems(pkg, translator('en-US'), 'en-US').some(item => item.key === 'openSource')).toBe(false);
  });

  it.each(LOCALES)('keeps only the new repository Q&A free of check dates in %s', code => {
    const pkg = pageOf(
      docker({
        appStore,
        price: {
          type: 'free',
          amount: null,
          currency: null,
          region: null,
          plan: null,
          period: null,
          appVersion: null,
          sourceUrl: 'https://vendor.example/pricing',
          method: 'vendor_site',
          verifiedAt: '2025-12-01T08:00:00Z',
          stale: false,
          offer: true
        },
        openSource: {
          repo: 'acme/osi',
          url: 'https://github.com/acme/osi',
          license: 'MIT',
          archived: false,
          checkedAt: '2025-12-01T08:00:00Z'
        },
        macosSupplement: {
          ranges: [{ arch: 'arm64', support: 'native', minimum: '12', maximum: null }],
          appVersion: '4.95.0,242831',
          sourceUrl: 'https://vendor.example/requirements',
          method: 'vendor_docs',
          verifiedAt: '2025-12-01T08:00:00Z',
          stale: false,
          conflicts: []
        }
      })
    );
    pkg.repository = { url: 'https://github.com/acme/osi', stars: 12345, signalsCheckedAt: '2026-10-02T00:00:00Z' };
    const t = translator(code);
    const verified = faqItems(pkg, t, code);
    expect(verified.find(item => item.key === 'openSource')?.question).toBe(
      t('package.faq.openSourceQ', { name: pkg.displayName })
    );
    expect(verified.find(item => item.key === 'chips')?.answer).toContain('vendor.example/requirements');
    const checkedDate = formatDate('2025-12-01T08:00:00Z', { locale: code });
    for (const key of ['price', 'chips', ...(code === 'en-US' ? [] : ['language'])])
      expect(verified.find(item => item.key === key)?.answer).toContain(checkedDate);
    const prices = faqItems(
      { ...pkg, facts: { ...pkg.facts, prices: pkg.facts.prices.filter(price => price.channel === 'app_store') } },
      t,
      code
    );
    expect(prices.find(item => item.key === 'price')?.answer).toContain(checkedDate);
    expect(prices.find(item => item.key === 'price')?.answer).toContain('apps.apple.com');
    pkg.openSource = null;
    pkg.facts.overview.artifacts.binaries = [];
    pkg.conflictsWith = { casks: [], formulae: [] };
    const publicOnly = faqItems(pkg, t, code);
    expect(publicOnly.find(item => item.key === 'openSource')?.question).toBe(
      t('package.faq.repositoryQ', { name: pkg.displayName })
    );
    const repositoryAnswer = publicOnly.find(item => item.key === 'openSource')!.answer;
    expect(repositoryAnswer).not.toContain(checkedDate);
    for (const item of [...verified, ...prices, ...publicOnly]) {
      expect(item.answer).not.toMatch(/★|12[,.]?345|12[,.]3K|\{\w+\}/);
      expect(item.answer).not.toContain(formatDate('2026-10-02T00:00:00Z', { locale: code }));
    }
  });

  it('omits repository-only questions without a repository URL', () => {
    const pkg = pageOf(docker());
    pkg.repository = { stars: 12345, signalsCheckedAt: '2026-10-02T00:00:00Z' };
    expect(faqItems(pkg, translator('en-US'), 'en-US').some(item => item.key === 'openSource')).toBe(false);
  });

  it.each(LOCALES)('preserves main’s questions at the FAQ limit in %s', locale => {
    const pkg = pageOf(
      docker({
        appStore,
        price: {
          type: 'free',
          amount: null,
          currency: null,
          region: null,
          plan: null,
          period: null,
          appVersion: null,
          sourceUrl: 'https://vendor.example/pricing',
          method: 'vendor_site',
          verifiedAt: '2025-12-01T08:00:00Z',
          stale: false,
          offer: true
        }
      })
    );
    const t = translator(locale);
    const expected = mainFaqItems(pkg, t, locale);
    pkg.repository = { url: 'https://github.com/acme/public-only' };
    const result = faqItems(pkg, t, locale);
    expect(result.length).toBeLessThanOrEqual(FAQ_LIMIT);
    expect(result.filter(item => item.question !== t('package.faq.repositoryQ', { name: pkg.displayName }))).toEqual(
      expected
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
    const items = faqItems(pageOf(bare), t, 'en-US');
    expect(items.map(item => item.key)).toEqual(['uninstall']);
    expect(items[0]!.answer).toBe('Run `brew uninstall --cask docker-desktop`.');
    const font = docker({ isFont: true });
    expect(faqItems(pageOf(font), t, 'en-US').map(item => item.key)).toEqual(['uninstall', 'data', 'version']);
  });
});

// Public production records, projected to FAQ inputs without changing their facts. The frozen
// implementation and existing translations come from main; only the new repository Q&A may differ.
const production = JSON.parse(readFileSync(new URL('./fixtures/packageFaq.production.json', import.meta.url), 'utf8'))
  .packages as Record<string, PackageDetail>;

describe.each(['rectangle', 'iterm2', 'discord', 'ely-office'])('%s FAQ parity with main', token => {
  it.each(LOCALES)('preserves every existing question and answer byte-for-byte in %s', locale => {
    const pkg = slimPackage(production[token]!);
    const t = translator(locale);
    const expected = mainFaqItems(pkg, t, locale);
    expect(expected.length).toBeGreaterThan(0);
    expect(JSON.stringify(faqItems(pkg, t, locale))).toBe(JSON.stringify(expected));

    // Simulate the future repository contract using only the record's real repository URL.
    if (pkg.repoUrl) {
      pkg.repository = {
        url: pkg.repoUrl,
        stars: 12345,
        archived: !pkg.openSource?.archived,
        signalsCheckedAt: '2026-10-02T00:00:00Z'
      };
      const result = faqItems(pkg, t, locale);
      const addedQuestion = t('package.faq.repositoryQ', { name: pkg.displayName });
      const unchanged = result.filter(item => item.question !== addedQuestion);
      expect(JSON.stringify(unchanged)).toBe(JSON.stringify(expected));
      if (!pkg.openSource && expected.length < FAQ_LIMIT) {
        const added = result.find(item => item.question === addedQuestion)!;
        expect(added).toBeDefined();
        expect(added.answer).toContain(pkg.repoUrl);
        expect(added.answer).not.toMatch(/★|12[,.]?345|12[,.]3K/);
        expect(added.answer).not.toContain(formatDate('2026-10-02T00:00:00Z', { locale }));
      }
    }
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
    expect(chipSupport(pageOf(pkg))).toBeNull();
    expect(faqItems(pageOf(pkg), translator('zh-CN'), 'zh-CN').find(item => item.key === 'chips')).toBeUndefined();
  });

  it('Requires explicit matching downloads on both chips before claiming a shared installer', () => {
    const [arm, intel] = docker().platforms!;
    expect(chipSupport(pageOf(docker({ platforms: [arm!] })))).toBeNull();
    expect(chipSupport(pageOf(docker({ platforms: [intel!] })))).toBeNull();
    expect(chipSupport(pageOf(docker({ platforms: [{ ...arm!, downloadUrl: null }, intel!] })))).toBeNull();
    // One matching variant does not make every installer shared.
    expect(
      chipSupport(pageOf(docker({ platforms: [arm!, intel!, { ...intel!, downloadUrl: arm!.downloadUrl }] })))
    ).toBeNull();
    expect(
      chipSupport(
        pageOf(
          docker({
            platforms: [arm!, { ...intel!, downloadUrl: arm!.downloadUrl }, { ...arm!, downloadUrl: null }]
          })
        )
      )
    ).toBeNull();
  });

  it('Reads uninstall steps and clean-up paths', () => {
    expect(uninstallSteps(pageOf(docker()))).toEqual({
      quits: true,
      services: 3,
      loginItems: 0,
      helpers: 2,
      receipts: 0,
      kexts: 0
    });
    expect(zapPaths(pageOf(docker()))).toHaveLength(11);
    expect(dataLocations(pageOf(docker()))).toEqual({
      settings: '~/Library/Preferences/com.docker.docker.plist',
      folders: ['~/.docker', '~/Library/Application Support/Docker Desktop', '~/Library/Containers/com.docker.docker']
    });
    expect(dataLocations(pageOf(docker({ artifacts: { apps: [], binaries: [], pkgs: [] } })))).toEqual({
      settings: undefined,
      folders: []
    });
  });

  it('Says “separate builds” only when each chip has its own download', () => {
    expect(chipSupport(pageOf(docker()))).toBe('separate');
    expect(chipSupport(pageOf(docker({ platforms: [] })))).toBeNull();
    const same = docker().platforms!.map(platform => ({
      ...platform,
      downloadUrl: 'https://desktop.docker.com/universal/Docker.dmg'
    }));
    expect(chipSupport(pageOf(docker({ platforms: same })))).toBe('shared');
    const old = docker().platforms!.map(platform => ({ ...platform, version: '4.0.0' }));
    expect(chipSupport(pageOf(docker({ platforms: old })))).toBeNull();
    expect(
      chipSupport(pageOf(docker({ supports: { status: 'known', arm64: true, x86_64: true, requiresRosetta: true } })))
    ).toBe('rosetta');
    const [arm, intel] = docker().platforms!;
    expect(
      chipSupport(pageOf(docker({ supports: { status: 'known', arm64: true, x86_64: false }, platforms: [arm!] })))
    ).toBe('armOnly');
    expect(
      chipSupport(pageOf(docker({ supports: { status: 'known', arm64: false, x86_64: true }, platforms: [intel!] })))
    ).toBe('intelOnly');
    // Homebrew's declared support and its rows must agree before a chip answer.
    expect(chipSupport(pageOf(docker({ supports: { status: 'known', arm64: true, x86_64: false } })))).toBeNull();
    expect(chipSupport(pageOf(docker({ supports: { status: 'unknown', arm64: true, x86_64: true } })))).toBeNull();
  });
});

describe('Answers from the facts model (T2, T3)', () => {
  const t = translator('en-US');
  const brew = /\bbrew\b/;

  it('Gives link apps facts and questions without any Homebrew command', () => {
    const link = pageOf(
      docker({
        kind: 'link',
        tap: '',
        sourceUrl: '',
        homebrewStatus: undefined,
        platforms: [],
        appStore,
        openSource: {
          repo: 'acme/lumen',
          url: 'https://github.com/acme/lumen',
          license: 'MIT',
          archived: false,
          checkedAt: '2025-12-01T08:00:00Z'
        },
        alternatives: [{ package: { displayName: 'OrbStack' }, reasons: [], limits: [], bestFor: null }]
      } as Partial<PackageDetail>)
    );
    const items = faqItems(link, t, 'en-US');
    expect(items.map(item => item.key)).toEqual(['price', 'openSource', 'version', 'alternatives']);
    for (const item of items) expect(item.answer).not.toMatch(brew);
    // The App Store note about Homebrew's edition is for casks only.
    expect(items[0]!.answer).not.toContain('Homebrew');
    expect(items.find(item => item.key === 'version')!.answer).toBe('The latest version is 4.95.0.');
  });

  it('States only verified prices, and only when the vendor prices agree on how it is paid', () => {
    const verified = {
      type: 'subscription' as const,
      amount: 9.99,
      currency: 'USD',
      region: 'US',
      plan: 'Pro',
      period: 'month' as const,
      appVersion: null,
      sourceUrl: 'https://vendor.example/pricing',
      method: 'vendor_site' as const,
      verifiedAt: '2025-12-01T08:00:00Z',
      stale: false,
      offer: true
    };
    const answer = (pkg: ReturnType<typeof pageOf>) => faqItems(pkg, t, 'en-US').find(item => item.key === 'price');
    expect(answer(pageOf(docker({ price: verified })))!.answer).toBe(
      'No, Docker Desktop requires a subscription. Pro · United States · $9.99 per month Source: vendor.example/pricing, checked Dec 1, 2025.'
    );
    // Stale or disputed evidence keeps its block but answers nothing here.
    expect(answer(pageOf(docker({ price: { ...verified, stale: true } })))).toBeUndefined();
    const prices = pageOf(docker({ price: verified })).facts.prices!;
    expect(
      answer(
        pageOf(docker(), { prices: prices.map(p => ({ ...p, evidence: { ...p.evidence, status: 'needs_review' } })) })
      )
    ).toBeUndefined();
    // A free edition beside a subscription is not a yes or a no.
    const mixed = [...prices, { ...prices[0]!, edition: 'Free', usage: 'free' as const, amount: '0' }];
    expect(answer(pageOf(docker(), { prices: mixed }))).toBeUndefined();
  });

  it('Answers chips from Homebrew while the vendor statement is under review', () => {
    const supplement = {
      ranges: [{ arch: 'arm64' as const, support: 'native' as const, minimum: '12', maximum: null }],
      appVersion: '4.95.0,242831',
      sourceUrl: 'https://vendor.example/requirements',
      method: 'vendor_docs' as const,
      verifiedAt: '2025-12-01T08:00:00Z',
      stale: false,
      conflicts: [] as ('minimum' | 'maximum' | 'arm64' | 'x86_64')[]
    };
    const chips = (pkg: ReturnType<typeof pageOf>) =>
      faqItems(pkg, t, 'en-US').find(item => item.key === 'chips')!.answer;
    expect(chips(pageOf(docker({ macosSupplement: supplement })))).toBe(
      'Yes. The vendor states it runs natively on Apple silicon. Source: vendor.example/requirements, checked Dec 1, 2025.'
    );
    // Any disagreement with Homebrew puts the whole statement under review.
    expect(chips(pageOf(docker({ macosSupplement: { ...supplement, conflicts: ['minimum'] } })))).toBe(
      'Yes. Homebrew downloads separate builds for Apple silicon and Intel Macs. It needs macOS 14 or later.'
    );
    // A statement about another version is not about the one on offer.
    expect(chips(pageOf(docker({ macosSupplement: { ...supplement, appVersion: '4.0' } })))).toContain('Homebrew');
  });

  it('Asks about the page language in the new site languages too', () => {
    const keys: FaqTranslate = key => key;
    for (const [locale, code] of [
      ['de-DE', 'DE'],
      ['fr-FR', 'FR'],
      ['it-IT', 'IT'],
      ['zh-Hant', 'ZH']
    ] as const) {
      const listed = faqItems(pageOf(docker({ appStore: { ...appStore, languages: ['EN', code] } })), keys, locale);
      expect(listed.find(item => item.key === 'language')?.answer).toContain('package.faq.languageYes');
      const missing = faqItems(pageOf(docker({ appStore: { ...appStore, languages: ['EN'] } })), keys, locale);
      expect(missing.find(item => item.key === 'language')?.answer).toContain('package.faq.languageNo');
    }
  });

  it('Reads uninstall facts only while their evidence is verified', () => {
    const stale = pageOf(docker());
    const aged = {
      ...stale,
      facts: {
        ...stale.facts,
        uninstall: stale.facts.uninstall.map(item => ({
          ...item,
          evidence: evidence(item.evidence.sourceUrl, { status: 'stale' })
        }))
      }
    };
    expect(zapPaths(aged)).toEqual([]);
    expect(faqItems(aged, t, 'en-US').find(item => item.key === 'uninstall')!.answer).toBe(
      'Run `brew uninstall --cask docker-desktop`. It quits the app.'
    );
  });
});
