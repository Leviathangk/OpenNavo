// App-page questions, pure for testing. Questions follow what people search for about a Mac app
// (is it safe, is it free, how to remove it completely, does it run on Apple silicon, where it
// keeps its data); answers are fixed sentences filled with facts from the Homebrew definition,
// OpenNavo's install counts and the facts:sources job (GitHub license, Mac App Store listing). A
// question without a page-specific fact is left out, so no two apps share a boilerplate answer.
import type { PackageDetail } from '@opennavo/api';
import { formatDate, formatVersion } from '@opennavo/shared';
import type { Locale } from '@opennavo/shared';
import { evidenceLine, joinSentences, minimumOnly } from './appAnswer';

/** vue-i18n's t with an optional plural count. */
export type FaqTranslate = (key: string, named?: Record<string, unknown>, plural?: number) => string;

export type FaqKey =
  | 'safety'
  | 'price'
  | 'openSource'
  | 'uninstall'
  | 'chips'
  | 'language'
  | 'data'
  | 'version'
  | 'extras'
  | 'alternatives';

export interface FaqItem {
  key: FaqKey;
  question: string;
  answer: string;
}

/** Collapsed questions take one line each; beyond this the list stops helping. */
export const FAQ_LIMIT = 8;

type Entry = NonNullable<PackageDetail['artifacts']['entries']>[number];
type Stanza = Record<string, unknown>;

const strings = (value: unknown): string[] =>
  typeof value === 'string' ? [value] : Array.isArray(value) ? value.filter(item => typeof item === 'string') : [];

function stanzas(entries: Entry[] | undefined, type: 'uninstall' | 'zap'): Stanza[] {
  return (entries ?? [])
    .filter(entry => entry.type === type)
    .flatMap(entry => {
      const value = entry.declaration?.[type];
      const list = Array.isArray(value) ? value : [value];
      return list.filter((item): item is Stanza => Boolean(item) && typeof item === 'object' && !Array.isArray(item));
    });
}

const unique = (values: string[]) => [...new Set(values)];

/** What `brew uninstall` does beyond moving the app to the Trash, read from the uninstall stanza. */
export function uninstallSteps(pkg: Pick<PackageDetail, 'artifacts'>) {
  const all = stanzas(pkg.artifacts.entries, 'uninstall');
  const pick = (key: string) => unique(all.flatMap(stanza => strings(stanza[key])));
  return {
    quits: pick('quit').length > 0,
    services: pick('launchctl').length,
    loginItems: pick('login_item').length,
    helpers: pick('delete').filter(path => path.startsWith('/Library/PrivilegedHelperTools/')).length,
    receipts: pick('pkgutil').length,
    kexts: pick('kext').length
  };
}

/** Files and folders `brew uninstall --zap` removes. */
export function zapPaths(pkg: Pick<PackageDetail, 'artifacts'>): string[] {
  return unique(
    stanzas(pkg.artifacts.entries, 'zap').flatMap(stanza =>
      ['trash', 'delete', 'rmdir'].flatMap(key => strings(stanza[key]))
    )
  );
}

// Shared lists, crash reporters, logs and caches say nothing about where an app keeps its data.
const NOISE =
  /sharedfilelist|bugsnag|crashreporter|KSCrash|\.sfl\d?|\/Logs\/|\/Caches\/|\/Saved Application State\/|\/HTTPStorages\/|\/WebKit\/|\/Cookies\/|\/com\.apple\./i;

/** The settings file and up to three data folders Homebrew lists for clean-up. */
export function dataLocations(pkg: Pick<PackageDetail, 'artifacts'>): {
  settings?: string;
  folders: string[];
} {
  const paths = zapPaths(pkg).filter(path => !/[*?[{]/.test(path) && !NOISE.test(path));
  const quit = stanzas(pkg.artifacts.entries, 'uninstall')
    .flatMap(stanza => strings(stanza.quit))[0]
    ?.toLowerCase();
  const plists = paths.filter(path => /^~\/Library\/Preferences\/[^/]+\.plist$/.test(path));
  const settings = plists.find(path => quit && path.toLowerCase().endsWith(`/${quit}.plist`)) ?? plists[0];
  const folders = paths
    .filter(path =>
      /^~\/Library\/(Application Support|Containers|Group Containers)\/[^/]+$|^~\/\.[^/]+(\/[^/]+)?$/.test(path)
    )
    .slice(0, 3);
  return { settings, folders };
}

export type ChipSupport = 'separate' | 'shared' | 'rosetta' | 'armOnly' | 'intelOnly';

/** How Homebrew installs the current version on each chip; null when Homebrew does not say. */
export function chipSupport(pkg: Pick<PackageDetail, 'supports' | 'platforms' | 'version'>): ChipSupport | null {
  const { supports } = pkg;
  if (supports.status !== 'known' || (!supports.arm64 && !supports.x86_64)) return null;
  if (!supports.arm64) return 'intelOnly';
  if (supports.requiresRosetta) return 'rosetta';
  if (!supports.x86_64) return 'armOnly';
  const current = (pkg.platforms ?? []).filter(platform => platform.version === pkg.version);
  if (current.some(platform => ['arm64', 'x86_64'].includes(platform.arch) && !platform.downloadUrl)) return null;
  const urls = (arch: string) =>
    new Set(current.filter(platform => platform.arch === arch).map(platform => platform.downloadUrl));
  const arm = urls('arm64');
  const intel = urls('x86_64');
  if (!arm.size || !intel.size) return null;
  if ([...arm].every(url => !intel.has(url))) return 'separate';
  // A missing download or partially overlapping variants do not prove one shared installer.
  return arm.size === 1 && intel.size === 1 ? 'shared' : null;
}

// Language each page asks about, as Apple's lookup lists interface languages.
const PAGE_LANGUAGE: Partial<Record<Locale, string>> = {
  'zh-CN': 'ZH',
  'ja-JP': 'JA',
  'es-ES': 'ES',
  'pt-BR': 'PT',
  'ru-RU': 'RU'
};

// Homebrew's lifecycle reason codes with their own wording; anything else is described generically.
const REASONS = [
  'fails_gatekeeper_check',
  'unsigned',
  'discontinued',
  'unmaintained',
  'no_longer_available',
  'no_longer_meets_criteria',
  'moved_to_mas'
];

// Artifact types that put something besides the app on the Mac.
const PLUGINS: Record<string, string> = {
  qlplugin: 'quickLook',
  prefpane: 'prefPane',
  screen_saver: 'screenSaver',
  input_method: 'inputMethod',
  audio_unit_plugin: 'audio',
  vst_plugin: 'audio',
  vst3_plugin: 'audio',
  mdimporter: 'spotlight',
  dictionary: 'dictionary',
  colorpicker: 'colorPicker',
  keyboard_layout: 'keyboardLayout',
  service: 'service',
  internet_plugin: 'browser'
};

const MAX_TOOLS = 6;

export function faqItems(pkg: PackageDetail, t: FaqTranslate, locale: Locale): FaqItem[] {
  const name = pkg.displayName;
  const token = pkg.token;
  const code = (value: string) => `\`${value}\``;
  const list = (values: string[]) => new Intl.ListFormat(locale, { type: 'conjunction' }).format(values);
  // Steps read better in Chinese as a plain enumeration than with 和 before the last one.
  const series = (values: string[]) => (locale === 'zh-CN' ? values.join('、') : list(values));
  const count = (value: number) => new Intl.NumberFormat(locale).format(value);
  const date = (value: string) => formatDate(value, { locale });
  // Abbreviated dates already end with a period ("14 окт."), so a sentence ending in one keeps one.
  const sentences = (values: (string | false | null | undefined)[]) =>
    joinSentences(
      values.filter((value): value is string => Boolean(value)).map(value => value.replace(/\.\.$/, '.')),
      locale
    );
  const items: FaqItem[] = [];
  const add = (key: FaqKey, question: string, answer: string) => {
    if (answer) items.push({ key, question, answer });
  };

  // Is it safe? Homebrew's own lifecycle, the checksum, where the file comes from and how many install it.
  const status = pkg.homebrewStatus;
  if (pkg.tap === 'homebrew/cask' && status) {
    const reason = () => t(`package.faq.reasons.${REASONS.includes(status.reason ?? '') ? status.reason : 'other'}`);
    const host = pkg.downloadUrl ? hostOf(pkg.downloadUrl) : '';
    const installs =
      pkg.installs.d30 > 0 && t('package.faq.safetyInstalls', { count: count(pkg.installs.d30) }, pkg.installs.d30);
    const download =
      host &&
      (pkg.downloadSha256 ? t('package.faq.safetyChecksum', { host }) : t('package.faq.safetyNoChecksum', { host }));
    if (status.state === 'disabled')
      add(
        'safety',
        t('package.faq.safetyQ', { name }),
        sentences([
          t('package.faq.safetyDisabled', { name, reason: reason() }),
          status.date && t('package.faq.disabledOn', { date: date(status.date) })
        ])
      );
    else
      add(
        'safety',
        t('package.faq.safetyQ', { name }),
        sentences([
          status.state === 'deprecated'
            ? t('package.faq.safetyDeprecated', { name, reason: reason() })
            : t('package.faq.safetyListed', { name }),
          download,
          installs
        ])
      );
  }

  // Is it free? Verified price evidence first; else the Mac App Store edition, marked as such.
  if (pkg.price)
    add(
      'price',
      t('package.faq.priceQ', { name }),
      sentences([t(`package.faq.priceA.${pkg.price.type}`, { name }), evidenceLine(pkg.price, t, locale)])
    );
  else if (pkg.appStore) {
    const store = pkg.appStore;
    const price =
      store.price === 0
        ? t('package.faq.priceStoreFree', { store: store.name })
        : t('package.faq.priceStorePaid', {
            store: store.name,
            price: money(store.price, store.currency, locale),
            region: regionName(store.region, locale)
          });
    add(
      'price',
      t('package.faq.priceQ', { name }),
      sentences([price, t('package.faq.priceStoreNote'), appStoreSource(store.checkedAt, t, locale)])
    );
  }

  // Is it open source? Only an OSI license GitHub recognized for a repository with code.
  if (pkg.openSource)
    add(
      'openSource',
      t('package.faq.openSourceQ', { name }),
      sentences([
        t('package.faq.openSourceA', {
          name,
          repo: pkg.openSource.repo,
          license: pkg.openSource.license
        }),
        pkg.openSource.archived && t('package.faq.openSourceArchived')
      ])
    );

  // How to remove it completely: what uninstall does, then --zap with how much it clears.
  const steps = uninstallSteps(pkg);
  const zap = zapPaths(pkg);
  const locations = dataLocations(pkg);
  const actions = [
    steps.quits && t('package.faq.actions.quit'),
    steps.services && t('package.faq.actions.services', { count: steps.services }, steps.services),
    steps.loginItems && t('package.faq.actions.loginItems', { count: steps.loginItems }, steps.loginItems),
    steps.helpers && t('package.faq.actions.helpers', { count: steps.helpers }, steps.helpers),
    steps.receipts && t('package.faq.actions.receipts', { count: steps.receipts }, steps.receipts),
    steps.kexts && t('package.faq.actions.kexts', { count: steps.kexts }, steps.kexts)
  ].filter((value): value is string => Boolean(value));
  const example = locations.folders[0] ?? locations.settings ?? zap.find(path => !/[*?[{]/.test(path));
  add(
    'uninstall',
    t('package.faq.uninstallQ', { name }),
    sentences([
      t('package.faq.uninstallRun', { token }),
      actions.length > 0 && t('package.faq.uninstallDoes', { actions: series(actions) }),
      zap.length > 0 && t('package.faq.uninstallZap', { token }),
      zap.length === 1 && example && t('package.faq.uninstallZapOne', { path: code(example) }),
      zap.length > 1 &&
        example &&
        t('package.faq.uninstallZapMany', { count: zap.length, path: code(example) }, zap.length)
    ])
  );

  // Apple silicon: Homebrew's builds per chip, or the vendor's statement when one is verified.
  const supplement = pkg.macosSupplement;
  const arm = supplement?.ranges.find(range => range.arch === 'arm64');
  const chips = chipSupport(pkg);
  const minimum = minimumOnly(pkg);
  if (!pkg.isFont && supplement && arm && !supplement.conflicts.includes('arm64'))
    add(
      'chips',
      t('package.faq.chipsQ', { name }),
      sentences([t(`package.faq.siliconA.${arm.support}`), evidenceLine(supplement, t, locale)])
    );
  else if (!pkg.isFont && chips)
    add(
      'chips',
      t('package.faq.chipsQ', { name }),
      sentences([t(`package.faq.chips.${chips}`), minimum && t('package.answer.requires', { version: minimum })])
    );

  // Interface language, from the App Store listing (English pages do not ask).
  const language = PAGE_LANGUAGE[locale];
  if (language && pkg.appStore?.languages.length) {
    const languages = pkg.appStore.languages;
    add(
      'language',
      t('package.faq.languageQ', { name }),
      sentences([
        languages.includes(language)
          ? t('package.faq.languageYes')
          : t('package.faq.languageNo', { count: languages.length }, languages.length),
        appStoreSource(pkg.appStore.checkedAt, t, locale)
      ])
    );
  }

  // Where it keeps settings and data, from the clean-up list.
  if (locations.settings || locations.folders.length)
    add(
      'data',
      t('package.faq.dataQ', { name }),
      sentences([
        locations.settings && t('package.faq.dataSettings', { path: code(locations.settings) }),
        locations.folders.length > 0 && t('package.faq.dataFolders', { paths: list(locations.folders.map(code)) }),
        t('package.faq.dataSource')
      ])
    );

  // Latest version, how it updates, and the beta or nightly casks Homebrew offers beside it.
  const version = formatVersion(pkg.version);
  const conflicts = pkg.conflictsWith.casks;
  const channels = conflicts.filter(other => other.startsWith(`${token}@`));
  if (version && version.toLowerCase() !== 'latest')
    add(
      'version',
      t('package.faq.versionQ', { name }),
      sentences([
        pkg.versionCommittedAt
          ? t('package.faq.versionDated', { version, date: date(pkg.versionCommittedAt) })
          : t('package.faq.versionA', { version }),
        pkg.autoUpdates ? t('package.faq.versionAuto', { name, token }) : t('package.faq.versionBrew', { token }),
        channels.length > 0 && t('package.faq.versionChannels', { list: list(channels.map(code)) })
      ])
    );

  // What else it puts on the Mac (command-line tools, a .pkg installer, plug-ins), and the other apps
  // it cannot share a Mac with. A conflict alone does not answer this question.
  const tools = pkg.artifacts.binaries.map(path => path.split('/').pop() ?? path).filter(Boolean);
  const plugins = unique(
    (pkg.artifacts.entries ?? []).flatMap(entry => (PLUGINS[entry.type] ? [PLUGINS[entry.type]!] : []))
  ).map(kind => t(`package.faq.plugins.${kind}`));
  const others = conflicts.filter(other => !channels.includes(other));
  if (!pkg.isFont && (tools.length || pkg.artifacts.pkgs.length || plugins.length))
    add(
      'extras',
      t('package.faq.extrasQ', { name }),
      sentences([
        tools.length > 0 &&
          (tools.length <= MAX_TOOLS
            ? t('package.faq.extrasTools', { count: tools.length, list: list(tools.map(code)) }, tools.length)
            : t(
                'package.faq.extrasSomeTools',
                { count: tools.length, list: list(tools.slice(0, MAX_TOOLS - 1).map(code)) },
                tools.length
              )),
        pkg.artifacts.pkgs.length > 0 && t('package.faq.extrasPkg'),
        plugins.length > 0 && t('package.faq.extrasPlugins', { list: list(plugins) }),
        others.length > 0 && t('package.faq.extrasConflicts', { list: list(others.map(code)) })
      ])
    );

  // Alternatives: only the confirmed ones.
  const alternatives = (pkg.alternatives ?? []).map(item => item.package.displayName);
  if (alternatives.length)
    add(
      'alternatives',
      t('package.faq.alternativesQ', { name }),
      t('package.faq.alternativesA', { list: list(alternatives) })
    );

  return items.slice(0, FAQ_LIMIT);
}

function hostOf(url: string): string {
  try {
    return new URL(url).host;
  } catch {
    return '';
  }
}

function money(amount: number, currency: string, locale: Locale): string {
  try {
    return new Intl.NumberFormat(locale, { style: 'currency', currency }).format(amount);
  } catch {
    return `${amount} ${currency}`;
  }
}

function regionName(region: string, locale: Locale): string {
  try {
    return new Intl.DisplayNames([locale], { type: 'region' }).of(region) ?? region;
  } catch {
    return region;
  }
}

function appStoreSource(checkedAt: string, t: FaqTranslate, locale: Locale): string {
  return evidenceLine({ sourceUrl: 'https://apps.apple.com', verifiedAt: checkedAt }, t, locale);
}
