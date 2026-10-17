import { convertFileSrc, isTauri } from '@tauri-apps/api/core';
import type { OnChipTone } from '@opennavo/ui';
import type { PostureCheck, StartupCategory, StartupItem, StartupOwner } from '@/ipc/bindings';

export type SecurityTab = 'overview' | 'scan' | 'startup' | 'protection' | 'quarantine';
export const SECURITY_TABS: readonly SecurityTab[] = ['overview', 'scan', 'startup', 'protection', 'quarantine'];

/**
 * Startup items as people know them: one row per app, holding everything it starts. An item no app owns is a row of
 * its own; `owner` is null then.
 */
export interface StartupApp {
  key: string;
  owner: StartupOwner | null;
  items: [StartupItem, ...StartupItem[]];
  /** Bundle file name, when another app shows under the same name (two "ChatGPT"s: the app and a web app). */
  file?: string;
}

const RANK: Record<StartupItem['trust'], number> = { untrusted: 0, unknown: 1, known: 2, apple: 3 };
/** What starts on its own at login or boot. Extensions, plugins and configuration files load into other programs. */
const STARTS: readonly StartupCategory[] = ['launch_items', 'btm', 'login_items'];
export const running = (item: StartupItem) => item.state !== 'disabled' && item.state !== 'removed';
/** Rust offers Enable or Disable only where OpenNavo can actually switch the item. */
export const switchable = (item: StartupItem) => item.actions.includes('enable') || item.actions.includes('disable');
/** New and broken items are what this page is for; they lead wherever they are. */
export const needsAttention = (item: StartupItem) => item.isNew || item.trust === 'untrusted';
export const worstTrust = (items: StartupApp['items']) =>
  items.reduce((worst, item) => (RANK[item.trust] < RANK[worst] ? item.trust : worst), items[0].trust);
const byAttention = (a: StartupApp['items'], b: StartupApp['items']) =>
  Number(b.some(item => item.isNew)) - Number(a.some(item => item.isNew)) ||
  Number(worstTrust(a) !== 'untrusted') - Number(worstTrust(b) !== 'untrusted') ||
  Number(b.some(running)) - Number(a.some(running));
export const appTitle = (app: StartupApp) => app.owner?.name ?? app.items[0].name;

/**
 * macOS's own items are never listed. What an app starts goes under that app; anything else is set aside (`others`):
 * command-line tools and scripts no app owns, extensions, plugins and configuration files. New or broken items are
 * listed on their own wherever they belong.
 */
export function startupApps(
  all: readonly StartupItem[],
  collator: Intl.Collator
): { apps: StartupApp[]; others: StartupItem[] } {
  const owned = new Map<string, StartupApp>();
  const apps: StartupApp[] = [];
  const others: StartupItem[] = [];
  for (const item of all) {
    if (item.trust === 'apple') continue;
    if (item.owner && STARTS.includes(item.category)) {
      const app = owned.get(item.owner.path);
      if (app) app.items.push(item);
      else {
        const created: StartupApp = { key: item.owner.path, owner: item.owner, items: [item] };
        owned.set(item.owner.path, created);
        apps.push(created);
      }
    } else if (needsAttention(item)) apps.push({ key: item.id, owner: item.owner, items: [item] });
    else others.push(item);
  }
  const byName = (a: StartupItem, b: StartupItem) => byAttention([a], [b]) || collator.compare(a.name, b.name);
  for (const app of apps) app.items.sort(byName);
  const named = new Map<string, StartupApp[]>();
  for (const app of owned.values()) named.set(appTitle(app), [...(named.get(appTitle(app)) ?? []), app]);
  for (const same of named.values())
    if (same.length > 1) for (const app of same) app.file = app.owner?.path.split('/').pop();
  apps.sort((a, b) => byAttention(a.items, b.items) || collator.compare(appTitle(a), appTitle(b)));
  return { apps, others: others.sort(byName) };
}

/** Icon Rust exported into the asset-protocol cache; browser mode (dev:web) falls back to letter tiles. */
export const ownerIcon = (owner: StartupOwner | null) =>
  owner?.iconPath && isTauri() ? convertFileSrc(owner.iconPath) : null;

/** The program a row without an app runs, for its terminal tile: "redis-server", ".zshrc". */
export const programName = (item: StartupItem) => (item.program ?? item.path).split('/').pop() ?? item.name;

/** Developer as the signature names it, without the Team ID the details sheet shows on its own line. */
export const developer = (item: StartupItem) => {
  const { signer, teamId } = item.signature;
  return signer && teamId ? signer.replace(` (${teamId})`, '') : signer;
};

export const POSTURE_TONE: Record<PostureCheck['state'], OnChipTone> = {
  ok: 'success',
  warn: 'warning',
  bad: 'danger',
  info: 'info',
  unknown: 'neutral'
};

export const TRUST_TONE: Record<StartupItem['trust'], OnChipTone> = {
  apple: 'neutral',
  known: 'success',
  unknown: 'neutral',
  untrusted: 'warning'
};

/** Signature line under the signer chip: Team ID and notarization, or what is missing. */
// Every status is listed, with no fallback: a new one fails to compile here instead of reading as "Invalid signature".
export function signatureKey(item: StartupItem): string {
  switch (item.signature.status) {
    case 'apple':
      return 'security.signature.apple';
    case 'app_store':
      return 'security.signature.appStore';
    case 'developer_id':
      return item.signature.notarized ? 'security.signature.notarized' : 'security.signature.notNotarized';
    case 'adhoc':
      return 'security.signature.adhoc';
    case 'unsigned':
      return 'security.signature.unsigned';
    case 'invalid':
      return 'security.signature.invalid';
    // Files changed after signing, e.g. an app writing into its own bundle: worth a look, not an alarm.
    case 'modified':
      return 'security.signature.modified';
    case 'unknown':
      return 'security.signature.unknown';
    // Configuration files and scripts: code signing does not apply.
    case 'not_code':
      return 'security.signature.notCode';
  }
}

/** Colour of the signature line: red for a broken signature, amber for one worth checking. */
export function signatureTone(item: StartupItem): 'danger' | 'warning' | null {
  const status = item.signature.status;
  if (status === 'invalid') return 'danger';
  return status === 'modified' || status === 'unknown' ? 'warning' : null;
}

/** Paths with ~ are shown as Rust sends them; this only shortens long ones for one-line rows. */
export function shortPath(path: string, max = 64): string {
  return path.length <= max ? path : `…${path.slice(path.length - max + 1)}`;
}
