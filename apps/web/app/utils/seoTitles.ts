// Page titles and descriptions (WP1), pure for testing. Titles keep the full software name and state
// the intent the page serves; they never add "best" or years, and only claim a download
// when Homebrew can still install the package. Descriptions say what the page answers instead of
// stacking summary, command, version and install counts.
import { disabledByHomebrew, formatVersion, type HomebrewLifecycle } from '@opennavo/shared';
import {
  capabilities,
  shownRequirements,
  uninstallFacts,
  verifiedAlternatives,
  verifiedPrices,
  type PackagePage
} from './packageFacts';

/** vue-i18n's t; the third argument picks the plural form, as in packageFaq.ts. */
export type Translate = (key: string, named?: Record<string, unknown>, plural?: number) => string;

/** Every page title ends with the brand; paginated titles put the page label before it. */
export const TITLE_SUFFIX = ' · OpenNavo';

export function pagedTitle(title: string, page: number | undefined, label: string): string {
  if (!page || page <= 1) return title;
  return title.endsWith(TITLE_SUFFIX)
    ? `${title.slice(0, -TITLE_SUFFIX.length)} · ${label}${TITLE_SUFFIX}`
    : `${title} · ${label}`;
}

/** Later pages of a list lead their description with the page label, so no two pages share one. */
export function pagedDescription(description: string | undefined, page: number | undefined, label: string) {
  if (!description || !page || page <= 1) return description;
  return `${label} · ${description}`;
}

/** A title without the brand suffix, for share images that show the brand themselves. */
export function brandlessTitle(title: string): string {
  return title.endsWith(TITLE_SUFFIX) ? title.slice(0, -TITLE_SUFFIX.length) : title;
}

/** Homebrew writes "latest" for unversioned casks; only real version numbers go into titles. */
export function reliableVersion(version: string | null | undefined): string {
  const value = formatVersion(version ?? '');
  return value && value.toLowerCase() !== 'latest' ? value : '';
}

/** Summary without closing punctuation; a summary that only repeats the name counts as missing. */
export function usableSummary(summary: string | null | undefined, name: string): string {
  const value = (summary ?? '').trim().replace(/[。．.!！?？]+$/u, '');
  return value.toLowerCase() === name.trim().toLowerCase() ? '' : value;
}

const NAME_MARK = '\ue000';
const SUMMARY_END = '\ue001';
/** Han and kana, which Chinese and Japanese write without spaces between them. */
const CJK_LETTER = /[\p{Script=Han}\u3040-\u30ff\u31f0-\u31ff]/u;

function isCjkLetter(char: string | undefined): boolean {
  return char !== undefined && CJK_LETTER.test(char);
}

/**
 * A template filled in with an app name. Chinese templates space {name} off, as Chinese text does
 * Latin words (查看 Visual Studio Code 在 Mac 上); a name that starts or ends in Chinese or Japanese
 * script joins the text beside it instead (查看微信 Mac 版在 Mac 上).
 */
function withName(t: Translate, key: string, name: string, named: Record<string, unknown> = {}): string {
  const letters = Array.from(name);
  const [first = '', ...rest] = t(key, { ...named, name: NAME_MARK }).split(NAME_MARK);
  return rest.reduce((text, after) => {
    const joinBefore = text.endsWith(' ') && isCjkLetter(Array.from(text).at(-2)) && isCjkLetter(letters[0]);
    const joinAfter = after.startsWith(' ') && isCjkLetter(Array.from(after)[1]) && isCjkLetter(letters.at(-1));
    return `${joinBefore ? text.slice(0, -1) : text}${name}${joinAfter ? after.slice(1) : after}`;
  }, first);
}

/**
 * A platform phrase ending an app's name, in any of the site's languages, because names often keep
 * their English form (WeChat for Mac, 微信 Mac 版, … para Mac, … для macOS). "Mac" has to stand on
 * its own: iMac版 and Mac Mouse Fix are names, not platform phrases.
 */
const PLATFORM_SUFFIX =
  /(?:\s+(?:for|para|для|für|pour|per)\s+(?:mac(?:\s?os(?:\s?x)?)?|os\s?x)|(?<![a-z\d])\s*mac(?:\s?os)?\s*版)$/iu;

/**
 * The name a title puts before its own platform phrase, so the platform is named once: "WeChat for
 * Mac" → "WeChat for Mac – Download & Install", "微信 Mac 版" → "微信 Mac 版下载安装".
 */
export function titleName(name: string): string {
  const full = name.trim();
  const rest = full.replace(PLATFORM_SUFFIX, '').trim();
  return /[\p{L}\p{N}]/u.test(rest) ? rest : full;
}

/** How a name says the common channel suffixes; numbers and other suffixes stay as the token spells them. */
const VARIANT_LABELS: Record<string, string> = {
  beta: 'Beta',
  nightly: 'Nightly',
  preview: 'Preview',
  dev: 'Dev',
  canary: 'Canary'
};

/** What a variant cask's token adds after its @ (aerial@beta → Beta, parallels@19 → 19); empty without one. */
export function variantLabel(token: string): string {
  const at = token.indexOf('@');
  if (at < 0) return '';
  const suffix = token.slice(at + 1);
  return VARIANT_LABELS[suffix] ?? suffix;
}

/** A letter or digit of the word around a match; Han and kana adjoin Latin words without a space. */
function isWordChar(char: string | undefined): boolean {
  return char !== undefined && /[\p{L}\p{N}]/u.test(char) && !isCjkLetter(char);
}

/**
 * Whether a name already says its variant: the label as a whole word in any case, with any separator
 * between its parts (Developer Edition for developer-edition), and a number also as v2 or @11.
 */
function carriesVariant(name: string, label: string): boolean {
  const parts = label.split(/[\s_-]+/u).map(part => part.replace(/[.*+?^${}()|[\]\\]/gu, '\\$&'));
  const pattern = new RegExp(`${/^\d/u.test(label) ? 'v?' : ''}${parts.join('[\\s_-]+')}`, 'giu');
  for (const match of name.matchAll(pattern)) {
    const before = Array.from(name.slice(0, match.index)).at(-1);
    const after = Array.from(name.slice(match.index + match[0].length))[0];
    if (!isWordChar(before) && !isWordChar(after)) return true;
  }
  return false;
}

/**
 * A variant cask's name with its variant added, ahead of a platform phrase so titles still find that
 * ("Aerial" → "Aerial Beta", "Telegram for macOS" → "Telegram Beta for macOS"). A name that already says
 * it stays as it is ("Firefox Nightly", "Kaleidoscope v2", "Example App @11"), and so does any other token.
 */
export function variantName(name: string, token: string): string {
  const label = variantLabel(token);
  const full = name.trim();
  if (!label || !full || carriesVariant(full, label)) return name;
  const platform = PLATFORM_SUFFIX.exec(full);
  const at = platform && /[\p{L}\p{N}]/u.test(full.slice(0, platform.index)) ? platform.index : full.length;
  const rest = full.slice(at);
  return `${full.slice(0, at).trimEnd()} ${label}${rest && !/^\s/u.test(rest) ? ' ' : ''}${rest}`;
}

/**
 * Variant casks (aerial@beta, parallels@19) often have the main app's name, so their pages would repeat
 * its titles. The detail's display name says the variant before any page reads it, so titles,
 * descriptions, headings, breadcrumbs and structured data all name it, as they already do when the catalog
 * name says it. Applying this twice changes nothing.
 */
export function withVariantName<T extends { token: string; displayName: string }>(detail: T): T {
  const displayName = variantName(detail.displayName, detail.token);
  return displayName === detail.displayName ? detail : { ...detail, displayName };
}

/**
 * withVariantName for every app in an API response: list records, a detail's alternatives, a collection's
 * items, anything with a token and a display name. Only the objects on the way to a renamed app are copied;
 * the response itself is left as it is, as other readers may share it.
 */
export function withVariantNames<T>(value: T): T {
  if (Array.isArray(value)) {
    const items = value.map(item => withVariantNames(item));
    return (items.some((item, index) => item !== value[index]) ? items : value) as T;
  }
  if (!value || typeof value !== 'object') return value;
  const source = value as Record<string, unknown>;
  let copy: Record<string, unknown> | undefined;
  for (const [key, item] of Object.entries(source)) {
    const next = withVariantNames(item);
    if (next !== item) (copy ??= { ...source })[key] = next;
  }
  const node = copy ?? source;
  return (
    typeof node.token === 'string' && typeof node.displayName === 'string'
      ? withVariantName(node as { token: string; displayName: string })
      : node
  ) as T;
}

/** Search results show about this many columns of a description, a CJK character taking two. */
export const DESCRIPTION_WIDTH = 160;
/** Less room than this leaves no useful part of a summary, so the description goes without one. */
const MIN_SUMMARY_WIDTH = 24;

const WIDE =
  /[\u1100-\u115f\u2e80-\u303e\u3041-\u33ff\u3400-\u4dbf\u4e00-\u9fff\ua960-\ua97f\uac00-\ud7a3\uf900-\ufaff\ufe30-\ufe4f\uff00-\uff60\uffe0-\uffe6\u{20000}-\u{3fffd}]/u;

/** Display width: two columns for each wide (CJK) character, one for any other. */
export function textWidth(text: string): number {
  let width = 0;
  for (const char of text) width += WIDE.test(char) ? 2 : 1;
  return width;
}

// Sentence ends skip abbreviations such as "e.g. PDF"; clause ends skip Latin commas, which also
// separate list items ("text, files, voice").
const SENTENCE_END = /(?<!\b\p{L})[.!?](?=\s)|[。！？]/gu;
const CLAUSE_END = /[;:](?=\s)|\s[–—]\s|[，；：]/gu;
/** Punctuation, and English, Spanish, Portuguese or Russian words that only lead into what was cut off. */
const DANGLING_END =
  /(?:[\s,.;:、，。；：–—-]+|\s+(?:a|an|and|as|at|by|for|from|in|into|its|of|on|or|than|that|the|their|to|via|with|your|al|ao|aos|com|con|da|das|de|del|do|dos|e|el|em|en|la|las|los|na|no|o|para|pela|pelo|por|que|seu|seus|su|sua|suas|sus|tu|tus|u|um|uma|y|а|в|во|ваш|ваши|для|его|и|из|или|их|к|на|о|об|от|по|с|со|что|aux|au|avec|dans|des|du|et|les|leur|leurs|ou|par|pour|qui|sans|ses|son|sur|une|un|vos|votre|à|l[’']|d[’']|qu[’']|als|auf|aus|bei|dem|den|der|des|die|das|ein|eine|einem|einen|einer|für|ihr|ihre|im|mit|nach|oder|und|von|vom|zu|zum|zur|über|alla|alle|ai|agli|che|degli|dei|del|della|delle|di|gli|il|lo|nel|nella|per|sul|sulla|tra|fra|tuo|tua|tuoi|tue|suo|sue|una|uno|all[’']|dell[’']|nell[’']|un[’']))$/iu;

/**
 * The start of a summary that fits `room` columns. A whole sentence or clause wins when it fills at
 * least half the room; otherwise the cut falls between words (anywhere in CJK text) and ends in an
 * ellipsis. Empty when the room is too small to say anything useful.
 */
export function fitSummary(summary: string, room: number): string {
  if (textWidth(summary) <= room) return summary;
  if (room < MIN_SUMMARY_WIDTH) return '';
  for (const end of [SENTENCE_END, CLAUSE_END]) {
    let head = '';
    for (const match of summary.matchAll(end)) {
      const candidate = summary.slice(0, match.index).trimEnd();
      if (textWidth(candidate) > room) break;
      head = candidate;
    }
    if (textWidth(head) * 2 >= room) return head;
  }
  let head = '';
  for (const char of summary) {
    if (textWidth(head) + textWidth(char) > room - 1) break;
    head += char;
  }
  // A cut inside a Latin word moves back to the space before it.
  const next = summary.slice(head.length);
  if (/^[\p{L}\p{N}]/u.test(next) && /[\p{L}\p{N}]$/u.test(head) && !WIDE.test(Array.from(head).at(-1) ?? '')) {
    const space = head.lastIndexOf(' ');
    if (space > 0) head = head.slice(0, space);
  }
  while (DANGLING_END.test(head)) head = head.replace(DANGLING_END, '');
  return head ? `${head}…` : '';
}

export interface OverviewFacts {
  kind: PackagePage['kind'];
  displayName: string;
  summary?: string | null;
  isFont: boolean;
  /** The catalog flag, which also covers apps paused by the 90-day rule that Homebrew still installs. */
  disabled: boolean;
  homebrewStatus?: HomebrewLifecycle | null;
  facts: PackagePage['facts'];
  version: string;
  latestRelease?: PackagePage['latestRelease'];
  /** Catalog token; a variant cask's (aerial@beta) description always names it. */
  token?: string;
}

function hasUninstallFacts(pkg: OverviewFacts): boolean {
  return [...uninstallFacts(pkg, 'uninstall'), ...uninstallFacts(pkg, 'zap')].some(item => item.value.trim());
}

export function overviewTitle(pkg: OverviewFacts, t: Translate): string {
  // Names such as "FiraCode Nerd Font" already say they are fonts.
  const key = disabledByHomebrew(pkg)
    ? 'package.seo.titleUnavailable'
    : pkg.isFont && !/\bfonts?\b|字体/iu.test(pkg.displayName)
      ? 'package.seo.titleFont'
      : 'package.seo.titleApp';
  return withName(t, key, titleName(pkg.displayName));
}

/** An overview's own search title and description in the page's language, set by editors or agents. */
export interface SeoOverride {
  title?: string | null;
  description?: string | null;
}

type OverrideFacts = Parameters<typeof disabledByHomebrew>[0] & { seoOverride?: SeoOverride | null };

/**
 * The overview title from the override, branded once like an article's search title; undefined leaves
 * the template in charge. An override may promise a download, so an app Homebrew has disabled always keeps
 * the template's wording. Only the overview reads overrides: its other tabs keep their templates.
 */
export function overrideTitle(pkg: OverrideFacts): string | undefined {
  const title = pkg.seoOverride?.title?.trim();
  if (!title || disabledByHomebrew(pkg)) return undefined;
  return title.endsWith(TITLE_SUFFIX) ? title : `${title}${TITLE_SUFFIX}`;
}

/** The overview description from the override; undefined leaves the template in charge, as for the title. */
export function overrideDescription(pkg: OverrideFacts): string | undefined {
  if (disabledByHomebrew(pkg)) return undefined;
  return pkg.seoOverride?.description?.trim() || undefined;
}

/**
 * Fits the summary around a complete closing sentence. An ellipsis replaces a Latin full stop;
 * CJK punctuation stays to separate the two sentences.
 */
function overviewSentence(
  pkg: OverviewFacts,
  t: Translate,
  key: string,
  named: Record<string, unknown> = {},
  whole = false
): string {
  let name = pkg.displayName;
  const room = DESCRIPTION_WIDTH - textWidth(withName(t, `${key}NoSummary`, '', named));
  if (textWidth(name) > room) name = fitSummary(name, room);
  const summary = usableSummary(pkg.summary, pkg.displayName);
  const fitted =
    summary && fitSummary(summary, DESCRIPTION_WIDTH - textWidth(withName(t, key, name, { ...named, summary: '' })));
  // `whole` accepts the closing sentence only beside the complete summary and full name.
  if (whole && (!summary || fitted !== summary || name !== pkg.displayName)) return '';
  if (!fitted) return withName(t, `${key}NoSummary`, name, named);
  const text = withName(t, key, name, { ...named, summary: `${fitted}${SUMMARY_END}` });
  return text.replace(fitted.endsWith('…') ? /\ue001\.?/u : SUMMARY_END, '');
}

/**
 * Give an app summary the full description budget: it is never shortened for the generic
 * section sentence, which follows only when it fits completely after the whole summary.
 * Without a useful summary, name only sections the overview renders and drop whole list
 * items when needed. A variant cask often has its main app's summary as well, so its
 * description keeps the sentence with its name and shortens the summary instead.
 */
export function overviewDescription(pkg: OverviewFacts, t: Translate, list: ListFormat): string {
  // Preserve the existing distribution/lifecycle branches.
  if (disabledByHomebrew(pkg)) return overviewSentence(pkg, t, 'package.seo.descriptionUnavailable');
  if (!usableSummary(pkg.summary, pkg.displayName)) return sectionDescription(pkg, t, list);
  const whole = sectionDescription(pkg, t, list, true);
  if (whole) return whole;
  if (variantLabel(pkg.token ?? '')) return sectionDescription(pkg, t, list);
  return fitSummary(pkg.summary!.trim(), DESCRIPTION_WIDTH);
}

function sectionDescription(pkg: OverviewFacts, t: Translate, list: ListFormat, whole = false): string {
  if (pkg.kind === 'link') return overviewSentence(pkg, t, 'package.seo.descriptionLink', {}, whole);
  const can = capabilities(pkg);
  const available = {
    installation: can.homebrew,
    price: verifiedPrices(pkg).length > 0,
    requirements: shownRequirements(pkg).length > 0,
    uninstall: can.uninstall && (can.homebrew || hasUninstallFacts(pkg)),
    alternatives: verifiedAlternatives(pkg).length > 0,
    versions: Boolean(pkg.latestRelease)
  };
  type Part = keyof typeof available;
  const priority: Part[] = ['installation', 'price', 'requirements', 'uninstall', 'alternatives', 'versions'];
  const parts = priority.filter(part => available[part]).map(part => t(`package.seo.overviewParts.${part}`));
  if (!parts.length) return overviewSentence(pkg, t, 'package.seo.descriptionOverview', {}, whole);
  while (parts.length > 1) {
    const closing = withName(t, 'package.seo.descriptionFactsNoSummary', pkg.displayName, { parts: list(parts) });
    if (textWidth(closing) <= DESCRIPTION_WIDTH) break;
    parts.pop();
  }
  return overviewSentence(pkg, t, 'package.seo.descriptionFacts', { parts: list(parts) }, whole);
}

export interface VersionFacts {
  displayName: string;
  version: string;
  latestRelease?: { version: string; publishedAt?: string | null; brewCommittedAt?: string | null } | null;
}

export function versionsTitle(pkg: VersionFacts, t: Translate): string {
  const version = reliableVersion(pkg.version);
  return version
    ? withName(t, 'package.seo.versionsTitleLatest', pkg.displayName, { version })
    : withName(t, 'package.seo.versionsTitle', pkg.displayName);
}

/**
 * Release date of the current version: the vendor date, else the Homebrew commit time, and only when
 * the latest release entry is the current version. version_changed_at is when OpenNavo noticed the
 * change (bulk reset on 2026-10-07), so it is never shown as a release date.
 */
export function currentReleaseDate(pkg: VersionFacts): string | undefined {
  const latest = pkg.latestRelease;
  if (!latest || formatVersion(latest.version) !== formatVersion(pkg.version)) return undefined;
  return latest.publishedAt ?? latest.brewCommittedAt ?? undefined;
}

export function versionsDescription(pkg: VersionFacts, t: Translate, formatDate: (iso: string) => string): string {
  const name = pkg.displayName;
  const version = reliableVersion(pkg.version);
  if (!version) return withName(t, 'package.seo.versionsDescriptionNoVersion', name);
  const date = currentReleaseDate(pkg);
  return date
    ? withName(t, 'package.versionsMetaDescription', name, { version, date: formatDate(date) })
    : withName(t, 'package.seo.versionsDescriptionNoDate', name, { version });
}

/** Joins phrases the way the page's language lists them ("a, b and c", "a、b和c"). */
export type ListFormat = (values: string[]) => string;

export interface CategoryFacts {
  name: string;
  /** The category's own description from the catalog; often only a few words. */
  description?: string | null;
  /** Apps the category lists in its default view, and that number formatted for the page's language. */
  total: number;
  totalLabel: string;
  /** Names of the first apps on this page, in list order. */
  apps: readonly string[];
}

const SENTENCE_CLOSE = /[.!?。！？…]$/u;

/**
 * The category's own description, then how many Mac apps it lists and the first three of them (C2).
 * The app names make every category, and every page of one, read differently; when the whole does not
 * fit the columns search results show, the list shrinks first and the catalog description is shortened.
 */
export function categoryDescription(category: CategoryFacts, t: Translate, list: ListFormat): string | undefined {
  const intro = (category.description ?? '').trim();
  if (!category.total) return intro || undefined;
  let sentence = '';
  for (let size = Math.min(3, category.apps.length); size >= 0; size--) {
    const named = { name: category.name, count: category.totalLabel, apps: list(category.apps.slice(0, size)) };
    const key = size ? 'categories.seo.itemDescription' : 'categories.seo.itemDescriptionCount';
    sentence = t(key, named, category.total);
    if (textWidth(sentence) <= DESCRIPTION_WIDTH) break;
  }
  const fitted = intro && fitSummary(intro, DESCRIPTION_WIDTH - textWidth(sentence) - 1);
  if (!fitted) return sentence;
  // A catalog description without closing punctuation gets the full stop of its own script.
  const closed = SENTENCE_CLOSE.test(fitted)
    ? fitted
    : `${fitted}${isCjkLetter(Array.from(fitted).at(-1)) ? '。' : '.'}`;
  return WIDE.test(Array.from(closed).at(-1) ?? '') ? `${closed}${sentence}` : `${closed} ${sentence}`;
}

export interface DependencyFacts {
  displayName: string;
  runtime: number;
  build: number;
  other: number;
  conflicts: number;
  dependents: number;
}

const DEPENDENCY_PARTS = ['runtime', 'build', 'other', 'conflicts', 'dependents'] as const;

/**
 * What the dependencies page lists, counted (C2): the page used to repeat the overview summary, so its
 * description matched the details page word for word.
 */
export function dependenciesDescription(
  facts: DependencyFacts,
  t: Translate,
  list: ListFormat,
  count: (value: number) => string
): string {
  const parts = DEPENDENCY_PARTS.filter(part => facts[part] > 0).map(part =>
    t(`package.deps.meta.${part}`, { count: count(facts[part]) }, facts[part])
  );
  return parts.length
    ? withName(t, 'package.deps.metaDescription', facts.displayName, { parts: list(parts) })
    : withName(t, 'package.deps.metaDescriptionNone', facts.displayName);
}

/** The details page says what it holds instead of repeating the overview summary (C2). */
export function detailsDescription(displayName: string, t: Translate): string {
  return withName(t, 'package.details.metaDescription', displayName);
}
