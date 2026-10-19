// Page titles and descriptions (WP1), pure for testing. Titles keep the full software name and state
// the intent the page serves; they never add "best", years or "free", and only claim a download
// when Homebrew can still install the package. Descriptions say what the page answers instead of
// stacking summary, command, version and install counts.
import { disabledByHomebrew, formatVersion, type HomebrewLifecycle } from '@opennavo/shared';

export type Translate = (key: string, named?: Record<string, unknown>) => string;

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
  /(?:\s+(?:for|para|для)\s+(?:mac(?:\s?os(?:\s?x)?)?|os\s?x)|(?<![a-z\d])\s*mac(?:\s?os)?\s*版)$/iu;

/**
 * The name a title puts before its own platform phrase, so the platform is named once: "WeChat for
 * Mac" → "WeChat for Mac – Download & Install", "微信 Mac 版" → "微信 Mac 版下载安装".
 */
export function titleName(name: string): string {
  const full = name.trim();
  const rest = full.replace(PLATFORM_SUFFIX, '').trim();
  return /[\p{L}\p{N}]/u.test(rest) ? rest : full;
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
  /(?:[\s,.;:、，。；：–—-]+|\s+(?:a|an|and|as|at|by|for|from|in|into|its|of|on|or|than|that|the|their|to|via|with|your|al|ao|aos|com|con|da|das|de|del|do|dos|e|el|em|en|la|las|los|na|no|o|para|pela|pelo|por|que|seu|seus|su|sua|suas|sus|tu|tus|u|um|uma|y|а|в|во|ваш|ваши|для|его|и|из|или|их|к|на|о|об|от|по|с|со|что))$/iu;

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
  kind?: string;
  displayName: string;
  summary?: string | null;
  isFont: boolean;
  /** The catalog flag, which also covers apps paused by the 90-day rule that Homebrew still installs. */
  disabled: boolean;
  homebrewStatus?: HomebrewLifecycle | null;
}

export function overviewTitle(pkg: OverviewFacts, t: Translate): string {
  const name = titleName(pkg.displayName);
  if (disabledByHomebrew(pkg)) return withName(t, 'package.seo.titleUnavailable', name);
  // Names such as "FiraCode Nerd Font" already say they are fonts.
  if (pkg.isFont && !/\bfonts?\b|字体/iu.test(pkg.displayName)) return withName(t, 'package.seo.titleFont', name);
  return withName(t, 'package.seo.titleApp', name);
}

/**
 * The summary, then a sentence on what the page answers. When both do not fit the columns search
 * results show, the summary is shortened (fitSummary), so the closing sentence is not cut off.
 */
export function overviewDescription(pkg: OverviewFacts, t: Translate): string {
  const name = pkg.displayName;
  const summary = usableSummary(pkg.summary, name);
  // Link apps have no install method on the page, only the developer's download (store/security plan §4).
  const key = disabledByHomebrew(pkg)
    ? 'package.seo.descriptionUnavailable'
    : pkg.kind === 'link'
      ? 'package.seo.descriptionLink'
      : 'package.seo.description';
  const fitted = summary && fitSummary(summary, DESCRIPTION_WIDTH - textWidth(withName(t, key, name, { summary: '' })));
  if (!fitted) return withName(t, `${key}NoSummary`, name);
  const text = withName(t, key, name, { summary: `${fitted}${SUMMARY_END}` });
  // An ellipsis ends a shortened summary in place of a Latin full stop ("voice… See how"); CJK text
  // keeps its 。 to separate the sentences.
  return text.replace(fitted.endsWith('…') ? /\ue001\.?/u : SUMMARY_END, '');
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
