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
/** Live activity stops growing the list here; the oldest rows drop off and can be loaded again. */
export const ACTIVITY_LIMIT = 1000;
/** How long the on/off switch waits for the status showing that macOS applied the change. */
export const SWITCH_TIMEOUT_MS = 10_000;

/** What the page shows from the extension; panels on screen follow it (see `follow`). */
export type FirewallView = 'settings' | 'rules' | 'activity' | 'blocklists';
const VIEWS: readonly FirewallView[] = ['settings', 'rules', 'activity', 'blocklists'];

/** An on/off change the switch waits for. */
interface SwitchWait {
  on: boolean;
  timer?: ReturnType<typeof setTimeout>;
}

const newestFirst = (a: FirewallEvent, b: FirewallEvent) => b.at - a.at || (a.id < b.id ? 1 : a.id > b.id ? -1 : 0);
const failureKey = (status: FirewallStatus | null) =>
  status?.error ? `${status.error.code}\n${status.error.detail ?? ''}` : null;

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
  /** Enable, disable or uninstall in flight, or an on/off change macOS has not applied yet. */
  const switching = ref(false);
  /** The on/off state asked for while it is being applied; the switch shows it instead of the old state. */
  const switchingTo = ref<boolean | null>(null);

  const available = computed(() => status.value?.availability === 'available');
  /** The extension is installed: rules and settings can be managed, whether filtering is on or paused. */
  const installed = computed(() => status.value?.extension === 'installed' && status.value.filter !== 'needs_consent');
  const on = computed(() => installed.value && status.value?.filter === 'on');

  let statusRevision = 0;
  let rulesRevision = 0;
  let alertsRevision = 0;
  let blocklistsRevision = 0;
  // Each full read retains successful mutations made while its snapshot is in flight.
  const pendingRules = new Set<Map<string, FirewallRule | null>>();
  const pendingBlocklists = new Set<Map<string, FirewallBlocklist | null>>();
  // ...and live activity rows that arrive meanwhile.
  const pendingActivity = new Set<Map<string, FirewallEvent>>();

  // Panels on screen, counted. What they show follows change events and is read again when the extension answers
  // after not responding, so a tab that failed to load does not stay in error.
  const followers = new Map<FirewallView, number>();
  const followed = (view: FirewallView) => (followers.get(view) ?? 0) > 0;
  /** A panel follows what it shows while it is on screen; call the returned function when it goes away. */
  function follow(...views: FirewallView[]) {
    for (const view of views) followers.set(view, (followers.get(view) ?? 0) + 1);
    let active = true;
    return () => {
      if (!active) return;
      active = false;
      for (const view of views) followers.set(view, Math.max(0, (followers.get(view) ?? 0) - 1));
    };
  }

  function mergeChanges<T extends { id: string }>(rows: T[], changes: Map<string, T | null>): T[] {
    const merged = new Map(rows.map(row => [row.id, row]));
    for (const [id, row] of changes) {
      if (row === null) merged.delete(id);
      else merged.set(id, row);
    }
    return [...merged.values()];
  }

  /** Live rows go on top (a merged row moves up with its new count); past `limit` the oldest rows drop off. */
  function mergeActivity(rows: readonly FirewallEvent[], live: readonly FirewallEvent[], limit: number) {
    const fresh = new Set(live.map(event => event.id));
    const merged = [...[...live].sort(newestFirst), ...rows.filter(event => !fresh.has(event.id))];
    return { rows: merged.slice(0, limit), trimmed: merged.length > limit };
  }

  async function loadStatus() {
    const revision = ++statusRevision;
    const next = await unwrap(commands.firewallStatus());
    if (revision === statusRevision) status.value = next;
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
    const revision = ++rulesRevision;
    const changes = new Map<string, FirewallRule | null>();
    pendingRules.add(changes);
    try {
      const next = await unwrap(commands.firewallRules());
      if (revision !== rulesRevision) return;
      rules.value = mergeChanges(next, changes);
      rulesLoaded.value = true;
    } finally {
      pendingRules.delete(changes);
    }
  }

  // A newer request (filter switch, clear, reload after an event) replaces the page being read.
  let activityRevision = 0;
  async function loadActivity(filter = blockedOnly.value) {
    const revision = ++activityRevision;
    const live = new Map<string, FirewallEvent>();
    pendingActivity.add(live);
    try {
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
      const merged = mergeActivity(
        page,
        [...live.values()].filter(event => !filter || event.action === 'block'),
        ACTIVITY_LIMIT
      );
      activity.value = merged.rows;
      activityMore.value = page.length === ACTIVITY_PAGE || merged.trimmed;
      activityLoaded.value = true;
    } finally {
      pendingActivity.delete(live);
    }
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
    const revision = ++blocklistsRevision;
    const changes = new Map<string, FirewallBlocklist | null>();
    pendingBlocklists.add(changes);
    try {
      const next = await unwrap(commands.firewallBlocklists());
      if (revision !== blocklistsRevision) return;
      blocklists.value = mergeChanges(next, changes);
      blocklistsLoaded.value = true;
    } finally {
      pendingBlocklists.delete(changes);
    }
  }

  async function loadAlerts() {
    const revision = ++alertsRevision;
    const next = await unwrap(commands.firewallAlerts());
    if (revision === alertsRevision) alerts.value = next;
  }

  const LOADERS: Record<FirewallView, () => Promise<void>> = {
    settings: loadSettings,
    rules: loadRules,
    activity: () => loadActivity(),
    blocklists: loadBlocklists
  };
  const read = (views: Iterable<FirewallView>) => Promise.allSettled([...views].map(view => LOADERS[view]()));

  /** What the page needs once the extension is installed; panels opened later read their own data. */
  async function loadInstalled() {
    await Promise.allSettled([read(new Set<FirewallView>(['settings', ...VIEWS.filter(followed)])), loadAlerts()]);
  }

  // Enable and disable return before macOS has applied the change: the switch keeps showing what was asked for until
  // a status shows it, a new failure arrives, or SWITCH_TIMEOUT_MS pass.
  let lifecycleRuns = 0;
  let awaited: SwitchWait | null = null;
  function syncSwitching() {
    switching.value = lifecycleRuns > 0 || awaited !== null;
    switchingTo.value = awaited?.on ?? null;
  }
  function stopWaiting(job: SwitchWait | null) {
    if (!job) return;
    clearTimeout(job.timer);
    if (awaited === job) awaited = null;
    syncSwitching();
  }
  function settles(target: boolean, previous: FirewallStatus | null, next: FirewallStatus) {
    const reached = target ? next.extension === 'installed' && next.filter === 'on' : next.filter !== 'on';
    return reached || (next.error !== null && failureKey(next) !== failureKey(previous));
  }

  function setStatus(next: FirewallStatus) {
    const previous = status.value;
    const wasInstalled = installed.value;
    status.value = next;
    if (awaited && settles(awaited.on, previous, next)) stopWaiting(awaited);
    if (!wasInstalled && installed.value) void loadInstalled();
    // Answering again after not responding: what is on screen is read again instead of keeping its error.
    else if (installed.value && next.connected && previous?.connected === false) void read(VIEWS.filter(followed));
  }

  async function lifecycle(run: () => Promise<FirewallStatus>, target: boolean | null = null) {
    const job: SwitchWait | null = target === null ? null : { on: target };
    if (job) {
      stopWaiting(awaited);
      awaited = job;
    }
    lifecycleRuns += 1;
    syncSwitching();
    const revision = ++statusRevision;
    try {
      const next = await run();
      // The reply describes the moment the command returned; a status pushed meanwhile is newer.
      if (revision === statusRevision) setStatus(next);
      if (job && awaited === job) job.timer = setTimeout(() => stopWaiting(job), SWITCH_TIMEOUT_MS);
    } catch (error) {
      stopWaiting(job);
      throw error;
    } finally {
      lifecycleRuns -= 1;
      syncSwitching();
    }
  }

  /** Installs the system extension when needed, then turns filtering on; macOS asks the user twice on the way. */
  const enable = (options: { allowApple: boolean; allowInstalled: boolean } | null) =>
    lifecycle(() => unwrap(commands.firewallEnable(options)), true);
  const disable = () => lifecycle(() => unwrap(commands.firewallDisable()), false);
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
    for (const changes of pendingRules) changes.set(rule.id, rule);
    const index = rules.value.findIndex(item => item.id === rule.id);
    rules.value = index < 0 ? [...rules.value, rule] : rules.value.map(item => (item.id === rule.id ? rule : item));
    return rule;
  }

  async function deleteRules(ids: string[]) {
    await withBusy(ids, () => unwrap(commands.firewallRuleDelete(ids)));
    for (const changes of pendingRules) {
      for (const id of ids) changes.set(id, null);
    }
    const gone = new Set(ids);
    rules.value = rules.value.filter(rule => !gone.has(rule.id));
  }

  /** Answer one waiting connection; Rust sends the shorter list (firewall:alerts) and the new rule (firewall:changed). */
  async function reply(answer: FirewallReply) {
    await withBusy([answer.alertId], () => unwrap(commands.firewallAlertReply(answer)));
    ++alertsRevision;
    alerts.value = alerts.value.filter(alert => alert.id !== answer.alertId);
  }

  async function clearActivity() {
    await unwrap(commands.firewallEventsClear());
    activityRevision += 1;
    activity.value = [];
    activityMore.value = false;
  }

  function upsertBlocklist(list: FirewallBlocklist) {
    for (const changes of pendingBlocklists) changes.set(list.id, list);
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
    for (const changes of pendingBlocklists) changes.set(id, null);
    blocklists.value = blocklists.value.filter(item => item.id !== id);
  }

  const identifyApp = (path: string) => unwrap(commands.firewallAppIdentify(path));

  // Events from Rust (firewall plan §4.8).
  function applyStatus(next: FirewallStatus) {
    ++statusRevision;
    setStatus(next);
  }

  function applyAlerts(next: FirewallAlert[]) {
    ++alertsRevision;
    alerts.value = next;
  }

  function applyChanged(what: 'rules' | 'settings' | 'blocklists') {
    // What is on screen or was read before is read again; the rest loads when it is opened.
    if (what === 'rules' && (rulesLoaded.value || followed('rules'))) void loadRules().catch(() => undefined);
    if (what === 'settings') void loadSettings().catch(() => undefined);
    if (what === 'blocklists' && (blocklistsLoaded.value || followed('blocklists')))
      void loadBlocklists().catch(() => undefined);
  }

  /** New or merged rows, only while the activity panel is on screen; it reads the log again when it opens. */
  function applyActivity(events: FirewallEvent[]) {
    if (!followed('activity')) return;
    for (const live of pendingActivity) for (const event of events) live.set(event.id, event);
    if (!activityLoaded.value) return;
    const shown = events.filter(event => !blockedOnly.value || event.action === 'block');
    if (!shown.length) return;
    // Rows loaded by hand stay; live rows push the oldest out once the list is full.
    const merged = mergeActivity(activity.value, shown, Math.max(ACTIVITY_LIMIT, activity.value.length));
    activity.value = merged.rows;
    if (merged.trimmed) activityMore.value = true;
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
    switchingTo,
    available,
    installed,
    on,
    follow,
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
