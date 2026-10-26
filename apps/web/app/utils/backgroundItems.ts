// Background items encyclopedia (F1), pure for testing. A label is only a name: apps can share one and
// malware can copy one, so these helpers never turn a declaration into ownership. The API returns what
// Homebrew casks declare (launchctl, login_item, pkgutil) and, once Hermes has written it, a description
// with its source; the page shows both, with where each came from.
import type { PublicComponents } from '@opennavo/api';
import { BRAND } from '@opennavo/shared';
import type { SiteLocale } from '@opennavo/shared';

type Schemas = PublicComponents['schemas'];
export type BackgroundItem = Schemas['BackgroundItem'];
export type BackgroundOwner = Schemas['BackgroundOwner'];
export type BackgroundContent = Schemas['BackgroundContent'];
type PageDecision = Schemas['PageDecision'];

/** Items per index page: the API's default page size, as on the other lists. */
export const BACKGROUND_PAGE_SIZE = 24;
/** The API rejects longer search text (255 bytes, counted in UTF-8). */
export const BACKGROUND_QUERY_BYTES = 255;

const encoder = new TextEncoder();
const byteLength = (text: string) => encoder.encode(text).length;

export { isValidLabel, labelPath } from '#shared/utils/backgroundLabels';

/**
 * Search text as the API takes it: trimmed, and cut to whole characters within 255 bytes. A repeated
 * ?q= is undefined, which the page answers with 404 like other repeated list parameters.
 */
export function readLabelQuery(value: unknown): string | undefined {
  if (value === undefined || value === null) return '';
  if (typeof value !== 'string') return undefined;
  let text = value.trim();
  while (byteLength(text) > BACKGROUND_QUERY_BYTES) text = Array.from(text).slice(0, -1).join('');
  return text;
}

/** The description is shown while it is verified, or stale with its check date; never while under review. */
export function usableContent(item: Pick<BackgroundItem, 'content'>): BackgroundContent | null {
  const content = item.content;
  if (!content || !content.description.trim()) return null;
  return content.evidence.status === 'verified' || content.evidence.status === 'stale' ? content : null;
}

/** Type labels: the verified type when there is one, else what the declarations say. */
export type BackgroundTypeKey = BackgroundContent['itemType'] | 'launchd';

const DECLARED_TYPE: Record<BackgroundOwner['kind'], BackgroundTypeKey> = {
  // launchctl names a launchd job, which may be a LaunchAgent or a LaunchDaemon.
  launchctl: 'launchd',
  login_item: 'login_item',
  pkgutil: 'package_receipt'
};

export function itemTypes(item: Pick<BackgroundItem, 'content' | 'apps'>): BackgroundTypeKey[] {
  const content = usableContent(item);
  if (content && content.itemType !== 'unknown') return [content.itemType];
  const kinds = [...new Set(item.apps.map(owner => DECLARED_TYPE[owner.kind]))];
  return kinds.length ? kinds : ['unknown'];
}

/** Whether launchd runs it from a property list, so the usual LaunchAgents/LaunchDaemons folders apply. */
export function isLaunchdItem(types: readonly BackgroundTypeKey[]): boolean {
  return types.some(type => type === 'launchd' || type === 'launch_agent' || type === 'launch_daemon');
}

/** Where launchd property lists usually live; the file is usually named after the label. */
export function launchdFolders(types: readonly BackgroundTypeKey[]): string[] {
  const agents = ['~/Library/LaunchAgents', '/Library/LaunchAgents'];
  const daemons = ['/Library/LaunchDaemons'];
  if (types.includes('launch_agent') && !types.includes('launch_daemon') && !types.includes('launchd')) return agents;
  if (types.includes('launch_daemon') && !types.includes('launch_agent') && !types.includes('launchd')) return daemons;
  return [...agents, ...daemons];
}

/** One app that declares the label, with every declaration it makes (uninstall and zap, several kinds). */
export interface DeclaringApp {
  package: BackgroundOwner['package'];
  declarations: BackgroundOwner[];
}

export function declaringApps(item: Pick<BackgroundItem, 'apps'>): DeclaringApp[] {
  const byToken = new Map<string, DeclaringApp>();
  for (const owner of item.apps) {
    const entry = byToken.get(owner.package.token);
    if (entry) entry.declarations.push(owner);
    else byToken.set(owner.package.token, { package: owner.package, declarations: [owner] });
  }
  return [...byToken.values()];
}

/** An index group: one named developer, several developers (a shared label), or none named. */
export interface DeveloperGroup {
  key: string;
  developer: string | null;
  several: boolean;
  items: BackgroundItem[];
}

const SEVERAL = '\u0000several';
const UNKNOWN = '\u0000unknown';

/**
 * Index groups by developer, named developers in the reader's alphabetical order, then labels several
 * developers' apps declare, then labels whose apps name no developer. Items keep the API's label order.
 */
export function groupByDeveloper(items: readonly BackgroundItem[], collator: Intl.Collator): DeveloperGroup[] {
  const groups = new Map<string, DeveloperGroup>();
  for (const item of items) {
    const names = [...new Set(item.developers.map(name => name.trim()).filter(Boolean))];
    const key = names.length > 1 ? SEVERAL : (names[0] ?? UNKNOWN);
    const group = groups.get(key) ?? {
      key,
      developer: key === SEVERAL || key === UNKNOWN ? null : key,
      several: key === SEVERAL,
      items: []
    };
    group.items.push(item);
    groups.set(key, group);
  }
  const rank = (group: DeveloperGroup) => (group.developer !== null ? 0 : group.several ? 1 : 2);
  return [...groups.values()].sort(
    (a, b) => rank(a) - rank(b) || collator.compare(a.developer ?? '', b.developer ?? '')
  );
}

const STARTUP_LINK = new RegExp(`^${BRAND.scheme}://startup\\?label=[^&#\\s]+$`);
const UNINSTALL_LINK = new RegExp(`^${BRAND.scheme}://uninstall/[a-z0-9][a-z0-9@._+-]{0,127}\\?preview=1$`);

/** The client's preview link for a label, when the API's link has the one shape the client accepts. */
export function startupLink(item: Pick<BackgroundItem, 'startupUrl'>): string | null {
  return STARTUP_LINK.test(item.startupUrl) ? item.startupUrl : null;
}

/** The client's uninstall preview for an app (never an uninstall itself), checked the same way. */
export function uninstallLink(owner: Pick<BackgroundOwner, 'uninstallUrl'>): string | null {
  return UNINSTALL_LINK.test(owner.uninstallUrl) ? owner.uninstallUrl : null;
}

/**
 * The page's decision from the API's `indexable` (description verified in this language, its source still
 * fresh and at least one catalog app declaring it): one canonical address per label and, while indexable,
 * the page itself as its only language alternate (the API does not say which other languages are ready).
 */
export function backgroundItemDecision(
  item: Pick<BackgroundItem, 'indexable'>,
  locale: SiteLocale,
  canonicalPath: string
): PageDecision {
  return {
    robots: item.indexable ? 'index, follow' : 'noindex, follow',
    canonicalPath,
    hreflang: item.indexable ? [locale] : [],
    inSitemap: item.indexable,
    announce: false,
    reason: item.indexable ? 'background_item' : 'background_item_not_ready'
  };
}

/** A search or a page of labels none of which is ready says nothing worth indexing (S4: noindex, follow). */
export function indexPageNoindex(records: readonly Pick<BackgroundItem, 'indexable'>[], query: string): boolean {
  return Boolean(query) || !records.some(item => item.indexable);
}

/** Plain text of a Markdown description, for the meta description: no markup, one line. */
export function plainText(markdown: string): string {
  return markdown
    .replace(/```[\s\S]*?```/g, ' ')
    .replace(/!\[[^\]]*\]\([^)]*\)/g, ' ')
    .replace(/\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replace(/[`*_~>#|]/g, '')
    .replace(/\s+/g, ' ')
    .trim();
}
