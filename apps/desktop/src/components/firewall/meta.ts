import { convertFileSrc, isTauri } from '@tauri-apps/api/core';
import type { OnChipTone } from '@opennavo/ui';
import type { FirewallApp, FirewallExtensionState, FirewallRule, FirewallSigner, FirewallStatus } from '@/ipc/bindings';

export type FirewallTab = 'rules' | 'activity' | 'blocklists' | 'settings';
export const FIREWALL_TABS: readonly FirewallTab[] = ['rules', 'activity', 'blocklists', 'settings'];

/**
 * What the page shows. Management needs the extension installed and the filter configured, even while paused; before
 * that the page explains, then follows macOS's two confirmations. A removed extension that macOS deletes only at the
 * next restart no longer runs, so it can be turned on again like one that was never installed.
 */
export type FirewallPhase = 'loading' | 'unavailable' | 'setup' | 'progress' | 'ready';
const SETUP_STATES: readonly FirewallExtensionState[] = ['not_installed', 'removal_pending', 'failed'];
export function firewallPhase(status: FirewallStatus | null): FirewallPhase {
  if (!status) return 'loading';
  if (status.extension === 'installed' && status.filter !== 'needs_consent') return 'ready';
  if (status.availability !== 'available') return 'unavailable';
  if (SETUP_STATES.includes(status.extension)) return 'setup';
  return 'progress';
}

export type SetupStep = 'install' | 'approve' | 'consent';
export const SETUP_STEPS: readonly SetupStep[] = ['install', 'approve', 'consent'];
/** Where each of macOS's steps stands: the extension installs, the user allows it, then allows network filtering. */
export function stepState(status: FirewallStatus, step: SetupStep): 'done' | 'active' | 'todo' {
  const { extension, filter } = status;
  const approved = extension === 'installed' || extension === 'needs_reboot';
  if (step === 'install')
    return extension === 'activating' ? 'active' : approved || extension === 'needs_approval' ? 'done' : 'todo';
  if (step === 'approve') return extension === 'needs_approval' ? 'active' : approved ? 'done' : 'todo';
  if (extension !== 'installed') return 'todo';
  return filter === 'needs_consent' ? 'active' : filter === 'on' ? 'done' : 'todo';
}

/**
 * The question window ignores its buttons this long after another question appears or the window shows up: the window
 * can pop up under the cursor, and the second click of a double click would answer the next app's question.
 */
export const ANSWER_DELAY_MS = 600;

/** Icon Rust exported into the asset-protocol cache; browser mode (dev:web) falls back to letter tiles. */
export const appIcon = (app: FirewallApp | null) => (app?.iconPath && isTauri() ? convertFileSrc(app.iconPath) : null);

/** Command-line tools and other programs outside an app get a terminal tile instead of an app icon. */
export const isBundle = (app: FirewallApp) => app.bundlePath !== null;

export const SIGNER_TONE: Record<FirewallSigner, OnChipTone> = {
  apple: 'neutral',
  app_store: 'neutral',
  developer_id: 'success',
  adhoc: 'warning',
  unsigned: 'warning',
  invalid: 'danger'
};

/** Who signed the program, as one line: the developer's name when there is one. */
export function signerText(app: FirewallApp, t: (key: string, values?: Record<string, unknown>) => string): string {
  if (app.signer === 'developer_id')
    return app.teamName || app.teamId ? t('firewall.signer.developer', { team: app.teamName ?? app.teamId }) : '';
  return t(`firewall.signer.${app.signer}`);
}

/** Developer line under an app's name; Developer ID apps show the team, the rest the kind of signature. */
export function developerText(app: FirewallApp, t: (key: string, values?: Record<string, unknown>) => string) {
  if (app.signer === 'developer_id') return app.teamName ?? app.teamId ?? t('firewall.signer.developer_id');
  return t(`firewall.signer.${app.signer}`);
}

/** "example.com:443", with an IPv6 address in brackets. */
export const endpoint = (remote: string, port: number) =>
  remote.includes(':') ? `[${remote}]:${port}` : `${remote}:${port}`;

/** "api.example.com · port 443", "Any address", "Any address · port 22". */
export function ruleDestination(
  rule: Pick<FirewallRule, 'remote' | 'port'>,
  t: (key: string, values?: Record<string, unknown>) => string
) {
  const remote = rule.remote ?? t('firewall.rules.anyRemote');
  return rule.port === null ? remote : `${remote} · ${t('firewall.rules.port', { port: rule.port })}`;
}

/** Rules grouped by program: rules for every program first, then apps by name. */
export interface FirewallRuleGroup {
  key: string;
  app: FirewallApp | null;
  rules: FirewallRule[];
}

export function groupRules(rules: readonly FirewallRule[], collator: Intl.Collator): FirewallRuleGroup[] {
  const groups = new Map<string, FirewallRuleGroup>();
  for (const rule of rules) {
    const key = rule.app?.key ?? '*';
    const group = groups.get(key);
    if (group) group.rules.push(rule);
    else groups.set(key, { key, app: rule.app, rules: [rule] });
  }
  // Within an app: the rule for any destination first, then named destinations; blocks before allows.
  const rank = (rule: FirewallRule) => (rule.remote === null && rule.port === null ? 0 : 1);
  for (const group of groups.values())
    group.rules.sort(
      (a, b) =>
        rank(a) - rank(b) ||
        Number(a.action === 'allow') - Number(b.action === 'allow') ||
        collator.compare(a.remote ?? '', b.remote ?? '') ||
        (a.port ?? 0) - (b.port ?? 0)
    );
  return [...groups.values()].sort((a, b) =>
    !a.app ? -1 : !b.app ? 1 : collator.compare(a.app.name, b.app.name) || a.key.localeCompare(b.key)
  );
}

/** Paths are shown as Rust sends them; this only shortens long ones for one-line rows. */
export function shortPath(path: string, max = 64): string {
  return path.length <= max ? path : `…${path.slice(path.length - max + 1)}`;
}

/** Host names, *.wildcards, IPv4/IPv6 and CIDR ranges, checked again by Rust (E_FW_RULE_INVALID). */
const HOST = /^(\*\.)?([a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)(\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*\.?$/i;
const IPV4 = /^(25[0-5]|2[0-4]\d|1?\d?\d)(\.(25[0-5]|2[0-4]\d|1?\d?\d)){3}$/;
export function validRemote(value: string): boolean {
  const text = value.trim();
  if (!text || text.length > 253) return false;
  const [address, prefix, extra] = text.split('/');
  if (extra !== undefined) return false;
  if (prefix !== undefined) {
    if (!/^\d{1,3}$/.test(prefix)) return false;
    const bits = Number(prefix);
    if (IPV4.test(address!)) return bits <= 32;
    return address!.includes(':') && bits <= 128;
  }
  if (IPV4.test(text)) return true;
  if (text.includes(':')) return /^[0-9a-f:.]+$/i.test(text) && text.split(':').length <= 9;
  return HOST.test(text);
}

export const validPort = (value: number) => Number.isInteger(value) && value >= 1 && value <= 65535;
