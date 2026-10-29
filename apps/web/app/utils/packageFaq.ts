import { formatDate } from './packageDate';
// App-page questions, pure for testing. Questions follow what people search for about a Mac app
// (is it safe, is it free, how to remove it completely, does it run on Apple silicon, where it
// keeps its data); answers are fixed sentences filled from the same facts model the page's blocks
// show (T2): verified prices, the security facts, Homebrew's requirement rows and uninstall
// declarations, the declared artifacts and the confirmed alternatives, plus Homebrew's own status,
// OpenNavo's install counts and the Mac App Store listing. Only verified evidence answers a question;
// a stale or disputed fact stays in its block with its date and gets no answer here. A question
// without a page-specific fact is left out, so no two apps share a boilerplate answer. Link apps
// (T3) never get a Homebrew command.
import { formatVersion } from '@opennavo/shared';
import { formatRepoStars, toPackageRepoSignals } from '@opennavo/ui';
import type { SiteLocale as Locale } from '@opennavo/shared';
import { evidenceLine, joinSentences } from './appAnswer';
import {
  capabilities,
  currentVersion,
  homebrewRows,
  isVerified,
  macosRange,
  priceAmount,
  priceEdition,
  priceRegionName,
  verifiedPrices
} from './packageFacts';
import type { PackagePage } from './packageFacts';

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

type Entry = NonNullable<PackagePage['facts']['overview']['artifacts']['entries']>[number];
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

/** Verified uninstall declarations of one phase and kind. */
function declared(pkg: Pick<PackagePage, 'facts'>, phase: 'uninstall' | 'zap', kind: string): string[] {
  return unique(
    pkg.facts.uninstall
      .filter(item => item.phase === phase && item.kind === kind && isVerified(item.evidence))
      .map(item => item.value)
  );
}

/** What `brew uninstall` does beyond moving the app to the Trash. */
export function uninstallSteps(pkg: Pick<PackagePage, 'facts'>) {
  // Quitting and kernel extensions are not part of the uninstall facts; the declaration still says them.
  const stanza = stanzas(pkg.facts.overview.artifacts.entries, 'uninstall');
  const pick = (key: string) => unique(stanza.flatMap(item => strings(item[key])));
  return {
    quits: pick('quit').length > 0,
    services: declared(pkg, 'uninstall', 'launchctl').length,
    loginItems: declared(pkg, 'uninstall', 'login_item').length,
    helpers: declared(pkg, 'uninstall', 'zap').filter(path => path.startsWith('/Library/PrivilegedHelperTools/'))
      .length,
    receipts: declared(pkg, 'uninstall', 'pkgutil').length,
    kexts: pick('kext').length
  };
}

/** Files and folders `brew uninstall --zap` removes. */
export function zapPaths(pkg: Pick<PackagePage, 'facts'>): string[] {
  return declared(pkg, 'zap', 'zap');
}

// Shared lists, crash reporters, logs and caches say nothing about where an app keeps its data.
const NOISE =
  /sharedfilelist|bugsnag|crashreporter|KSCrash|\.sfl\d?|\/Logs\/|\/Caches\/|\/Saved Application State\/|\/HTTPStorages\/|\/WebKit\/|\/Cookies\/|\/com\.apple\./i;

/** The settings file and up to three data folders Homebrew lists for clean-up. */
export function dataLocations(pkg: Pick<PackagePage, 'facts'>): {
  settings?: string;
  folders: string[];
} {
  const paths = zapPaths(pkg).filter(path => !/[*?[{]/.test(path) && !NOISE.test(path));
  const quit = stanzas(pkg.facts.overview.artifacts.entries, 'uninstall')
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

/**
 * How Homebrew installs the current version on each chip, from its verified requirement rows; null
 * when Homebrew does not say, or when its declared chip support and the rows disagree. Any version
 * counts for which chips it installs on; only the current version's downloads decide between separate
 * and shared builds.
 */
export function chipSupport(
  pkg: Pick<PackagePage, 'facts' | 'sourceUrl' | 'version' | 'supports'>
): ChipSupport | null {
  const { supports } = pkg;
  if (supports.status !== 'known') return null;
  const rows = homebrewRows(pkg).filter(row => isVerified(row.evidence));
  const on = (arch: string) => rows.filter(row => row.architectures.includes(arch));
  const arm = on('arm64').length > 0;
  const intel = on('x86_64').length > 0;
  if (arm !== supports.arm64 || intel !== supports.x86_64) return null;
  if (!arm) return intel ? 'intelOnly' : null;
  if (on('arm64').some(row => row.appleSilicon === 'no')) return 'rosetta';
  if (!intel) return 'armOnly';
  const version = pkg.facts.overview.version || pkg.version;
  const current = rows.filter(row => row.versions.includes(version));
  if (current.some(row => !row.downloadUrl)) return null;
  const urls = (arch: string) =>
    new Set(current.filter(row => row.architectures.includes(arch)).map(row => row.downloadUrl));
  const armUrls = urls('arm64');
  const intelUrls = urls('x86_64');
  if (!armUrls.size || !intelUrls.size) return null;
  if ([...armUrls].every(url => !intelUrls.has(url))) return 'separate';
  // A missing download or partially overlapping variants do not prove one shared installer.
  return armUrls.size === 1 && intelUrls.size === 1 ? 'shared' : null;
}

// Language each page asks about, as Apple's lookup lists interface languages (ISO 639-1). Apple
// writes one code for Chinese, so Simplified and Traditional pages both ask about ZH, which the
// question words as "Chinese" in either script.
const PAGE_LANGUAGE: Partial<Record<Locale, string>> = {
  'zh-CN': 'ZH',
  'zh-Hant': 'ZH',
  'ja-JP': 'JA',
  'es-ES': 'ES',
  'pt-BR': 'PT',
  'ru-RU': 'RU',
  'de-DE': 'DE',
  'fr-FR': 'FR',
  'it-IT': 'IT'
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

export function faqItems(pkg: PackagePage, t: FaqTranslate, locale: Locale): FaqItem[] {
  const name = pkg.displayName;
  const token = pkg.token;
  const facts = pkg.facts;
  const artifacts = facts.overview.artifacts;
  const can = capabilities(pkg);
  const code = (value: string) => `\`${value}\``;
  const list = (values: string[]) => new Intl.ListFormat(locale, { type: 'conjunction' }).format(values);
  // Steps read better in Chinese as a plain enumeration than with 和 before the last one.
  const series = (values: string[]) => (locale === 'zh-CN' || locale === 'zh-Hant' ? values.join('、') : list(values));
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
  if (can.homebrew && can.safety && pkg.tap === 'homebrew/cask' && status) {
    const reason = () => t(`package.faq.reasons.${REASONS.includes(status.reason ?? '') ? status.reason : 'other'}`);
    const download = facts.overview.downloadUrl;
    const host = download ? hostOf(download) : '';
    const installs =
      pkg.installs.d30 > 0 && t('package.faq.safetyInstalls', { count: count(pkg.installs.d30) }, pkg.installs.d30);
    const checksum =
      host &&
      (facts.overview.sha256 ? t('package.faq.safetyChecksum', { host }) : t('package.faq.safetyNoChecksum', { host }));
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
          checksum,
          installs
        ])
      );
  }

  // Is it free? Verified vendor prices answer when they agree on how it is paid; otherwise the Mac App
  // Store edition, marked as such. A free download is never read as free to use.
  const prices = verifiedPrices(pkg);
  const usages = unique(
    prices.filter(price => price.channel === 'vendor' && price.usage !== 'unknown').map(price => price.usage)
  );
  const vendor =
    usages.length === 1 ? prices.find(price => price.channel === 'vendor' && price.usage === usages[0]) : null;
  const store = prices.find(price => price.channel === 'app_store' && price.amount != null && price.currency);
  if (vendor && vendor.evidence.checkedAt)
    add(
      'price',
      t('package.faq.priceQ', { name }),
      sentences([
        t(`package.faq.priceA.${vendor.usage}`, { name }),
        // Keep every verified plan/region beside the general answer, never turn one plan into a global price.
        ...prices
          .filter(price => price.channel === 'vendor' && price.usage === vendor.usage)
          .map(price =>
            [priceEdition(price), priceRegionName(price.region, locale), priceAmount(price, t, locale)]
              .filter(Boolean)
              .join(' · ')
          ),
        evidenceLine({ sourceUrl: vendor.evidence.sourceUrl, verifiedAt: vendor.evidence.checkedAt }, t, locale)
      ])
    );
  else if (!usages.length && store?.evidence.checkedAt) {
    const listing = pkg.appStore && pkg.appStore.url === store.purchaseUrl ? pkg.appStore.name : name;
    const amount = Number(store.amount);
    const price =
      amount === 0
        ? t('package.faq.priceStoreFree', { store: listing })
        : t('package.faq.priceStorePaid', {
            store: listing,
            price: money(amount, store.currency!, locale),
            region: regionName(store.region, locale)
          });
    add(
      'price',
      t('package.faq.priceQ', { name }),
      sentences([
        price,
        can.homebrew && t('package.faq.priceStoreNote'),
        appStoreSource(store.evidence.checkedAt, t, locale)
      ])
    );
  }

  // Is it open source? OSI facts remain separate from the optional repository signals.
  const repository = pkg.repository;
  const openSource = pkg.openSource;
  const repoSignals = toPackageRepoSignals(pkg);
  if (openSource)
    add(
      'openSource',
      t('package.faq.openSourceQ', { name }),
      sentences([
        t('package.faq.openSourceA', { name, repo: openSource.repo, license: openSource.license }),
        repoSignals?.stars === undefined
          ? undefined
          : t('package.faq.openSourceStars', { stars: `★${formatRepoStars(repoSignals.stars)}` }),
        repoSignals?.checkedAt ? t('package.faq.openSourceChecked', { date: date(repoSignals.checkedAt) }) : undefined,
        (repoSignals?.archived ?? openSource.archived) && t('package.faq.openSourceArchived')
      ])
    );
  else if (repository)
    add(
      'openSource',
      t('package.faq.openSourceQ', { name }),
      sentences([
        t('package.faq.openSourceNoLicenseA', { name, repo: repository.url ?? '' }),
        repoSignals?.stars === undefined
          ? undefined
          : t('package.faq.openSourceStars', { stars: `★${formatRepoStars(repoSignals.stars)}` }),
        repoSignals?.checkedAt ? t('package.faq.openSourceChecked', { date: date(repoSignals.checkedAt) }) : undefined,
        repoSignals?.archived && t('package.faq.openSourceArchived')
      ])
    );

  // How to remove it completely: what uninstall does, then --zap with how much it clears.
  const steps = uninstallSteps(pkg);
  const zap = zapPaths(pkg);
  const locations = dataLocations(pkg);
  if (can.homebrew) {
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
        zap.length > 0 && t('package.guide.zap'),
        zap.length === 1 && example && t('package.faq.uninstallZapOne', { path: code(example) }),
        zap.length > 1 &&
          example &&
          t('package.faq.uninstallZapMany', { count: zap.length, path: code(example) }, zap.length)
      ])
    );
  }

  // Apple silicon: the vendor's statement while it is verified for this version, else Homebrew's builds per chip.
  const supplement = facts.macosSupplement;
  const arm = supplement?.ranges.find(range => range.arch === 'arm64');
  const vendorRow =
    supplement &&
    facts.requirements.find(
      row =>
        row.evidence.sourceUrl === supplement.sourceUrl &&
        row.architectures.includes('arm64') &&
        row.versions.includes(currentVersion(pkg))
    );
  const chips = chipSupport(pkg);
  const minimum = macosRange(pkg).minimum;
  if (!pkg.isFont && arm && vendorRow && isVerified(vendorRow.evidence) && vendorRow.evidence.checkedAt)
    add(
      'chips',
      t('package.faq.chipsQ', { name }),
      sentences([
        t(`package.faq.siliconA.${arm.support}`),
        evidenceLine({ sourceUrl: vendorRow.evidence.sourceUrl, verifiedAt: vendorRow.evidence.checkedAt }, t, locale)
      ])
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
  if (can.homebrew && (locations.settings || locations.folders.length))
    add(
      'data',
      t('package.faq.dataQ', { name }),
      sentences([
        locations.settings && t('package.faq.dataSettings', { path: code(locations.settings) }),
        locations.folders.length > 0 && t('package.faq.dataFolders', { paths: list(locations.folders.map(code)) }),
        t('package.faq.dataSource')
      ])
    );

  // Latest version, how it updates, and the beta or nightly casks Homebrew offers beside it. Link apps
  // state only the version: their updates come from the developer.
  const version = formatVersion(facts.overview.version || pkg.version);
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
        can.homebrew &&
          (pkg.autoUpdates ? t('package.faq.versionAuto', { name, token }) : t('package.faq.versionBrew', { token })),
        can.homebrew && channels.length > 0 && t('package.faq.versionChannels', { list: list(channels.map(code)) })
      ])
    );

  // What else it puts on the Mac (command-line tools, a .pkg installer, plug-ins), and the other apps
  // it cannot share a Mac with. A conflict alone does not answer this question.
  const tools = artifacts.binaries.map(path => path.split('/').pop() ?? path).filter(Boolean);
  const plugins = unique(
    (artifacts.entries ?? []).flatMap(entry => (PLUGINS[entry.type] ? [PLUGINS[entry.type]!] : []))
  ).map(kind => t(`package.faq.plugins.${kind}`));
  const others = conflicts.filter(other => !channels.includes(other));
  if (can.homebrew && !pkg.isFont && (tools.length || artifacts.pkgs.length || plugins.length))
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
        artifacts.pkgs.length > 0 && t('package.faq.extrasPkg'),
        plugins.length > 0 && t('package.faq.extrasPlugins', { list: list(plugins) }),
        others.length > 0 && t('package.faq.extrasConflicts', { list: list(others.map(code)) })
      ])
    );

  // Alternatives: only the confirmed ones, as the alternatives block lists them.
  const alternatives = facts.alternatives.map(item => item.package.displayName);
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
