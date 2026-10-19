import { computed, ref, shallowRef } from 'vue';
import { defineStore } from 'pinia';
import { commands, unwrap } from '@/ipc/client';
import type {
  FirewallAlert,
  FirewallBlocklist,
  FirewallEvent,
  FirewallReply,
  FirewallRule,
  FirewallRuleInput,
  FirewallSettings,
  FirewallStatus
} from '@/ipc/bindings';

/** Rust's defaults (firewall plan §2), shown until the extension answers. */
export const FIREWALL_DEFAULTS: FirewallSettings = {
  mode: 'ask',
  allowApple: true,
  allowInstalled: true
};
/** Activity rows per page; older ones load on demand. */
export const ACTIVITY_PAGE = 100;

/**
 * App firewall (firewall plan, M7 / S4): the network system extension owns rules, settings, block lists and the
 * activity log, and decides even when OpenNavo is not running. This store mirrors them from commands and the
 * firewall:* events; every change goes through Rust.
 */
export const useFirewallStore = defineStore('firewall', () => {
  const status = shallowRef<FirewallStatus | null>(null);
  const settings = shallowRef<FirewallSettings | null>(null);
  const rules = shallowRef<FirewallRule[]>([]);
  const rulesLoaded = ref(false);
  const activity = shallowRef<FirewallEvent[]>([]);
  const activityLoaded = ref(false);
  /** More activity may exist before the oldest row on screen. */
  const activityMore = ref(false);
  const blockedOnly = ref(false);
  const blocklists = shallowRef<FirewallBlocklist[]>([]);
  const blocklistsLoaded = ref(false);
  /** Connections waiting for an answer, oldest first (firewall:alerts sends the whole list). */
  const alerts = shallowRef<FirewallAlert[]>([]);
  /** IDs of rules, block lists or alerts with a request in flight, so their controls stay disabled. */
  const busy = ref(new Set<string>());
  /** Enable, disable or uninstall in flight. */
  const switching = ref(false);

  const available = computed(() => status.value?.availability === 'available');
  /** The extension is installed: rules and settings can be managed, whether filtering is on or paused. */
  const installed = computed(() => status.value?.extension === 'installed' && status.value.filter !== 'needs_consent');
  const on = computed(() => installed.value && status.value?.filter === 'on');

  async function loadStatus() {
    status.value = await unwrap(commands.firewallStatus());
  }

  let settingsRevision = 0;
  let settingsPending = 0;
  let confirmedSettings: FirewallSettings | null = null;
  let settingsQueue: Promise<void> = Promise.resolve();

  async function loadSettings() {
    if (settingsPending) return;
    const revision = ++settingsRevision;
    const loaded = await unwrap(commands.firewallSettings());
    if (revision !== settingsRevision || settingsPending) return;
    confirmedSettings = loaded;
    settings.value = loaded;
  }

  async function loadRules() {
    rules.value = await unwrap(commands.firewallRules());
    rulesLoaded.value = true;
  }

  // A newer request (filter switch, clear, reload after an event) replaces the page being read.
  let activityRevision = 0;
  async function loadActivity(filter = blockedOnly.value) {
    const revision = ++activityRevision;
    const page = await unwrap(
      commands.firewallEvents({
        before: null,
        beforeId: null,
        limit: ACTIVITY_PAGE,
        blockedOnly: filter
      })
    );
    if (revision !== activityRevision) return;
    blockedOnly.value = filter;
    activity.value = page;
    activityMore.value = page.length === ACTIVITY_PAGE;
    activityLoaded.value = true;
  }

  async function loadMoreActivity() {
    const oldest = activity.value.at(-1);
    if (!oldest || !activityMore.value) return;
    const revision = activityRevision;
    const page = await unwrap(
      commands.firewallEvents({
        before: oldest.at,
        beforeId: oldest.id,
        limit: ACTIVITY_PAGE,
        blockedOnly: blockedOnly.value
      })
    );
    if (revision !== activityRevision) return;
    const seen = new Set(activity.value.map(event => event.id));
    activity.value = [...activity.value, ...page.filter(event => !seen.has(event.id))];
    activityMore.value = page.length === ACTIVITY_PAGE;
  }

  async function loadBlocklists() {
    blocklists.value = await unwrap(commands.firewallBlocklists());
    blocklistsLoaded.value = true;
  }

  async function loadAlerts() {
    alerts.value = await unwrap(commands.firewallAlerts());
  }

  /** What the page needs once the extension is installed. */
  async function loadInstalled() {
    await Promise.allSettled([loadSettings(), loadRules(), loadActivity(), loadBlocklists(), loadAlerts()]);
  }

  async function lifecycle(run: () => Promise<FirewallStatus>) {
    switching.value = true;
    try {
      status.value = await run();
    } finally {
      switching.value = false;
    }
  }

  /** Installs the system extension when needed, then turns filtering on; macOS asks the user twice on the way. */
  const enable = (options: { allowApple: boolean; allowInstalled: boolean } | null) =>
    lifecycle(() => unwrap(commands.firewallEnable(options)));
  const disable = () => lifecycle(() => unwrap(commands.firewallDisable()));
  const uninstall = () => lifecycle(() => unwrap(commands.firewallUninstall()));
  const openApproval = () => unwrap(commands.firewallOpenApproval());
  const showAlerts = () => unwrap(commands.firewallAlertShow());

  function saveSettings(next: FirewallSettings) {
    if (!settingsPending) confirmedSettings = settings.value;
    const revision = ++settingsRevision;
    const desired = { ...next };
    settingsPending += 1;
    settings.value = desired;
    const save = settingsQueue.then(async () => {
      try {
        confirmedSettings = await unwrap(commands.firewallSettingsSet(desired));
        if (revision === settingsRevision) settings.value = confirmedSettings;
      } catch (error) {
        if (revision === settingsRevision) settings.value = confirmedSettings;
        throw error;
      } finally {
        settingsPending -= 1;
        if (revision === settingsRevision && status.value && settings.value)
          status.value = { ...status.value, mode: settings.value.mode };
      }
    });
    // A failure must not cancel later user changes.
    settingsQueue = save.catch(() => undefined);
    return save;
  }

  async function withBusy<T>(ids: readonly string[], run: () => Promise<T>): Promise<T> {
    busy.value = new Set([...busy.value, ...ids]);
    try {
      return await run();
    } finally {
      const next = new Set(busy.value);
      ids.forEach(id => next.delete(id));
      busy.value = next;
    }
  }

  async function saveRule(input: FirewallRuleInput) {
    const rule = await withBusy(input.id ? [input.id] : [], () => unwrap(commands.firewallRuleSave(input)));
    const index = rules.value.findIndex(item => item.id === rule.id);
    rules.value = index < 0 ? [...rules.value, rule] : rules.value.map(item => (item.id === rule.id ? rule : item));
    return rule;
  }

  async function deleteRules(ids: string[]) {
    await withBusy(ids, () => unwrap(commands.firewallRuleDelete(ids)));
    const gone = new Set(ids);
    rules.value = rules.value.filter(rule => !gone.has(rule.id));
  }

  /** Answer one waiting connection; Rust sends the shorter list (firewall:alerts) and the new rule (firewall:changed). */
  async function reply(answer: FirewallReply) {
    await withBusy([answer.alertId], () => unwrap(commands.firewallAlertReply(answer)));
    alerts.value = alerts.value.filter(alert => alert.id !== answer.alertId);
  }

  async function clearActivity() {
    await unwrap(commands.firewallEventsClear());
    activityRevision += 1;
    activity.value = [];
    activityMore.value = false;
  }

  function upsertBlocklist(list: FirewallBlocklist) {
    const index = blocklists.value.findIndex(item => item.id === list.id);
    blocklists.value =
      index < 0 ? [...blocklists.value, list] : blocklists.value.map(item => (item.id === list.id ? list : item));
    return list;
  }

  const addBlocklist = async (source: string) => upsertBlocklist(await unwrap(commands.firewallBlocklistAdd(source)));
  const setBlocklist = (id: string, enabled: boolean) =>
    withBusy([id], async () => upsertBlocklist(await unwrap(commands.firewallBlocklistSet(id, enabled))));
  const refreshBlocklist = (id: string) =>
    withBusy([id], async () => upsertBlocklist(await unwrap(commands.firewallBlocklistRefresh(id))));
  async function removeBlocklist(id: string) {
    await withBusy([id], () => unwrap(commands.firewallBlocklistRemove(id)));
    blocklists.value = blocklists.value.filter(item => item.id !== id);
  }

  const identifyApp = (path: string) => unwrap(commands.firewallAppIdentify(path));

  // Events from Rust (firewall plan §4.8).
  function applyStatus(next: FirewallStatus) {
    const wasInstalled = installed.value;
    status.value = next;
    if (!wasInstalled && installed.value) void loadInstalled();
  }

  function applyAlerts(next: FirewallAlert[]) {
    alerts.value = next;
  }

  function applyChanged(what: 'rules' | 'settings' | 'blocklists') {
    // Only what the page already read is reloaded; the rest loads when it is opened.
    if (what === 'rules' && rulesLoaded.value) void loadRules().catch(() => undefined);
    if (what === 'settings') void loadSettings().catch(() => undefined);
    if (what === 'blocklists' && blocklistsLoaded.value) void loadBlocklists().catch(() => undefined);
  }

  /** New or merged rows: a merged row moves to the top with its new count. */
  function applyActivity(events: FirewallEvent[]) {
    if (!activityLoaded.value) return;
    const shown = events.filter(event => !blockedOnly.value || event.action === 'block');
    if (!shown.length) return;
    const fresh = new Set(shown.map(event => event.id));
    activity.value = [
      ...[...shown].sort((a, b) => b.at - a.at || (a.id < b.id ? 1 : a.id > b.id ? -1 : 0)),
      ...activity.value.filter(event => !fresh.has(event.id))
    ];
  }

  return {
    status,
    settings,
    rules,
    rulesLoaded,
    activity,
    activityLoaded,
    activityMore,
    blockedOnly,
    blocklists,
    blocklistsLoaded,
    alerts,
    busy,
    switching,
    available,
    installed,
    on,
    loadStatus,
    loadSettings,
    loadRules,
    loadActivity,
    loadMoreActivity,
    loadBlocklists,
    loadAlerts,
    loadInstalled,
    enable,
    disable,
    uninstall,
    openApproval,
    showAlerts,
    saveSettings,
    saveRule,
    deleteRules,
    reply,
    clearActivity,
    addBlocklist,
    setBlocklist,
    refreshBlocklist,
    removeBlocklist,
    identifyApp,
    applyStatus,
    applyAlerts,
    applyChanged,
    applyActivity
  };
});
