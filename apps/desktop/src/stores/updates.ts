import { computed, ref } from 'vue';
import { defineStore } from 'pinia';
import type {
  Kind,
  OutdatedItem,
  Task,
  TaskOp,
  TaskOptions,
  TaskTarget,
  TaskTrigger,
  UpdateChannel,
  UpdatesStatus
} from '@/ipc/bindings';
import { commands, packageKey, unwrap } from '@/ipc/client';
import { useMajorConfirmStore } from './majorConfirm';
import { useTasksStore } from './tasks';

// Cask-only catalog (ADR-018): neither show nor count command-line tools from brew outdated.
const shown = (list: OutdatedItem[]) => list.filter(item => item.source !== 'homebrew' || item.kind === 'cask');
// Operations after which a target no longer has the update brew outdated listed before them.
const SETTLING: readonly TaskOp[] = ['upgrade', 'reinstall', 'uninstall'];

/**
 * Row identity (store/security plan §11.4): Homebrew rows by package; every other source by source and local app,
 * because their kind/token only point at a catalog page (token is '' outside the catalog, the adamId for App Store).
 */
export const rowKey = (item: OutdatedItem) =>
  item.source === 'homebrew' ? packageKey(item.kind, item.token) : `${item.source}:${item.appId ?? ''}`;

/** Key Rust stores skipped versions and channels under; it survives a change of update source. */
export const appKey = (item: OutdatedItem) =>
  item.source === 'homebrew' ? `homebrew:${item.kind}:${item.token}` : `app:${item.appId ?? ''}`;

/** Catalog page of a row, if it has one: App Store rows open their store page, unmatched apps have none. */
export function rowHref(item: OutdatedItem): string | undefined {
  if (item.source === 'mas') return `/appstore/${item.token}`;
  return item.token ? `/package/${item.kind}/${item.token}` : undefined;
}

/**
 * The task an update row queues: Homebrew upgrades the package (explicit casks always use --greedy, 06 §6.3),
 * App Store rows go by adamId, and every other source updates the local app with options.source.
 */
export function updateRequest(item: OutdatedItem): {
  target: TaskTarget;
  options: TaskOptions;
} {
  if (item.source === 'homebrew')
    return {
      target: { kind: item.kind, token: item.token },
      options: item.kind === 'cask' ? { greedy: true } : {}
    };
  if (item.source === 'mas')
    return {
      target: { kind: 'mas', token: item.token },
      options: { source: 'mas' }
    };
  return {
    target: { kind: 'app', token: item.appId ?? '' },
    options: { source: item.source }
  };
}

/** Whether a finished task was for this row (Homebrew package, App Store adamId or local app). */
function matches(item: OutdatedItem, target: TaskTarget) {
  if (target.kind === 'mas') return item.source === 'mas' && item.token === target.token;
  if (target.kind === 'app') return item.source !== 'homebrew' && item.appId === target.token;
  return item.source === 'homebrew' && item.kind === target.kind && item.token === target.token;
}

/**
 * Available updates from every source (06 §6.8, store/security plan §11.4): updates_list reads cache; updates_check
 * reruns the checks of the enabled sources; reread after updates:changed. Last/next check times come from
 * updates_status (Rust schedules checks; the UI does not calculate them).
 */
export const useUpdatesStore = defineStore('updates', () => {
  const items = ref<OutdatedItem[]>([]);
  const loaded = ref(false);
  // Rows whose update, reinstall or uninstall just succeeded, with the task's finish time. Rust drops them from its
  // list right after the task, but that event trails the task's own, so a list read can still carry them for a
  // moment. They stay out until a read no longer lists them or a check ran after.
  const settled = new Map<string, number>();
  // Pins and unpins that just succeeded: the pinned flag to show until a read or a later check agrees.
  const pins = new Map<string, { pinned: boolean; at: number }>();
  const status = ref<UpdatesStatus | null>(null);
  const requested = ref(false);
  /** Legacy account-preflight failure from the last task, not a live sign-in check. */
  const masSignedOut = ref(false);
  const checking = computed(() => requested.value || Boolean(status.value?.checking));
  const checkedAt = computed(() => status.value?.checkedAt ?? null);
  /** Sources the last check could not read; the others' results are listed as usual (plan §11.4). */
  const failures = computed(() => status.value?.failures ?? []);
  const failedSources = computed(() => status.value?.failedSources ?? []);
  const nextCheckAt = computed(() => status.value?.nextCheckAt ?? null);

  // Catalog packages with an update, whichever source delivers it; App Store rows carry adamIds, not catalog tokens.
  const byKey = computed(
    () =>
      new Map(
        items.value.flatMap(item =>
          item.source !== 'mas' && item.token ? [[packageKey(item.kind, item.token), item] as const] : []
        )
      )
  );
  /**
   * Rows an update can be queued for: not pinned or ignored, not informational, not waiting for a relaunch, and not
   * from a source whose expected developer differs from the installed app's (Rust would refuse it).
   */
  const actionable = computed(() =>
    items.value.filter(
      item =>
        !item.pinned &&
        !item.ignored &&
        !item.informationalUrl &&
        !item.relaunchPending &&
        item.expectedTeamMatch !== false
    )
  );
  const find = (kind: Kind, token: string) => byKey.value.get(packageKey(kind, token));
  const findMas = (adamId: number) =>
    items.value.find(item => item.source === 'mas' && item.token === String(adamId)) ?? undefined;
  /** The row a task target updates (the tray sends targets, not rows). */
  const rowFor = (target: TaskTarget) => items.value.find(item => matches(item, target));

  async function loadStatus() {
    status.value = await unwrap(commands.updatesStatus());
  }

  function settle(task: Task) {
    const target = task.target;
    if (target?.kind === 'mas' && (task.state === 'succeeded' || task.state === 'failed'))
      masSignedOut.value = task.state === 'failed' && task.error?.code === 'E_MAS_NOT_SIGNED_IN';
    if (task.state !== 'succeeded' || !target) return;
    const at = task.finishedAt ?? Date.now();
    if (task.op === 'pin' || task.op === 'unpin') {
      const key = packageKey(target.kind, target.token);
      const pinned = task.op === 'pin';
      pins.set(key, { pinned, at });
      items.value = items.value.map(item => (matches(item, target) ? { ...item, pinned } : item));
      return;
    }
    if (!SETTLING.includes(task.op)) return;
    const done = items.value.filter(item => matches(item, target));
    if (target.kind === 'app' && task.op === 'upgrade' && !task.options.quitRunning) {
      // A running app keeps its old version until it is reopened: Rust keeps the row as relaunch pending.
      items.value = items.value.flatMap(item => {
        if (!matches(item, target)) return [item];
        return item.running
          ? [
              {
                ...item,
                installedVersion: item.currentVersion,
                relaunchPending: true
              }
            ]
          : [];
      });
      for (const item of done) if (!item.running) settled.set(rowKey(item), at);
      return;
    }
    if (target.kind !== 'app' && target.kind !== 'mas') pins.delete(packageKey(target.kind, target.token));
    for (const item of done) settled.set(rowKey(item), at);
    items.value = items.value.filter(item => !matches(item, target));
  }

  /** checkTime: when the check behind the list happened; it accounts for tasks finished before. */
  function accept(list: OutdatedItem[], checkTime: number | null) {
    const listed = new Map(list.map(item => [rowKey(item), item]));
    const checkedSince = (at: number) => checkTime !== null && checkTime >= at;
    for (const [key, finishedAt] of settled) {
      const item = listed.get(key);
      // A relaunch-pending row is the finished update itself, not the stale row from before it.
      if (!item || item.relaunchPending || checkedSince(finishedAt)) settled.delete(key);
    }
    for (const [key, pin] of pins) {
      const item = listed.get(key);
      if (!item || item.pinned === pin.pinned || checkedSince(pin.at)) pins.delete(key);
    }
    items.value = shown(list)
      .filter(item => !settled.has(rowKey(item)))
      .map(item => {
        const pin = item.source === 'homebrew' ? pins.get(packageKey(item.kind, item.token)) : undefined;
        return pin ? { ...item, pinned: pin.pinned } : item;
      });
    loaded.value = true;
  }

  async function load() {
    // Status first: a list read after it is at least as new as the check the status reports.
    await loadStatus().catch(() => undefined);
    accept(await unwrap(commands.updatesList()), status.value?.checkedAt ?? null);
  }

  /** Check every source enabled in Settings; whether Homebrew runs brew update first is a setting Rust reads. */
  async function check() {
    const startedAt = Date.now();
    requested.value = true;
    try {
      accept(await unwrap(commands.updatesCheck(null)), startedAt);
    } finally {
      requested.value = false;
      await loadStatus().catch(() => undefined);
    }
  }

  /**
   * Queue updates for rows from any mix of sources (store/security plan §7): major updates are confirmed once first
   * (declining skips only them), then the smallest downloads go first. Consecutive rows of one source share an
   * enqueue, so each keeps its source's options and the tasks store still asks once per batch about running apps.
   */
  async function upgrade(rows: readonly OutdatedItem[], trigger: TaskTrigger = 'manual'): Promise<Task[]> {
    let chosen = [...rows];
    const majors = chosen.filter(row => row.major);
    if (majors.length && trigger === 'manual') {
      const others = chosen.length > majors.length;
      if (!(await useMajorConfirmStore().ask({ majors, others }))) chosen = chosen.filter(row => !row.major);
    }
    const size = (row: OutdatedItem) => row.downloadSize ?? Number.MAX_SAFE_INTEGER;
    chosen.sort((a, b) => size(a) - size(b));
    const runs: { options: TaskOptions; targets: TaskTarget[] }[] = [];
    for (const row of chosen) {
      const { target, options } = updateRequest(row);
      const last = runs.at(-1);
      if (last && JSON.stringify(last.options) === JSON.stringify(options)) last.targets.push(target);
      else runs.push({ options, targets: [target] });
    }
    // Rust only inspects Homebrew apps for running copies; other sources report it on their rows.
    const running = chosen
      .filter(row => row.source !== 'homebrew' && row.running && !row.relaunchPending)
      .map(row => ({ target: updateRequest(row).target, name: row.name }));
    const tasks = useTasksStore();
    const queued: Task[] = [];
    for (const { options, targets } of runs)
      queued.push(...(await tasks.enqueueMany('upgrade', targets, trigger, options, running)));
    return queued;
  }

  /** Stop checking an app altogether, whatever its source (Rust's update_prefs.ignored). */
  async function ignoreApp(item: OutdatedItem, ignored = true) {
    await unwrap(commands.updateIgnore(appKey(item), ignored));
    await load();
  }

  /** Skip this version (null clears a skip); a newer release brings the app back with its old skip shown. */
  async function skip(item: OutdatedItem, version: string | null) {
    await unwrap(commands.updateSkip(appKey(item), version));
    await load();
  }

  async function setChannel(item: OutdatedItem, channel: UpdateChannel) {
    await unwrap(commands.updateSetChannel(appKey(item), channel));
    await load();
  }

  /** Quit the app normally and open it again, so the replaced version runs; Rust never force-quits. */
  async function relaunch(item: OutdatedItem) {
    if (!item.appId) return;
    await unwrap(commands.appRelaunch(item.appId));
    await load();
  }

  /**
   * History entries whose update can be rolled back now (plan §7, P2): the newest OpenNavo update of an app whose kept
   * copy is still there and that has not changed since. Rust checks again before it queues the rollback.
   */
  const rollbackable = ref(new Set<string>());
  let rollbackRead = 0;
  async function loadRollbacks(ids: readonly string[]) {
    const read = ++rollbackRead;
    const found = ids.length ? await unwrap(commands.updateRollbackTargets([...ids])) : [];
    if (read === rollbackRead) rollbackable.value = new Set(found);
  }

  /** Queue a rollback; Rust asks a running app to quit normally first and skips the version rolled back from. */
  async function rollback(task: Task): Promise<string> {
    const id = await unwrap(commands.updateRollback(task.id));
    rollbackable.value = new Set([...rollbackable.value].filter(other => other !== task.id));
    return id;
  }

  return {
    items,
    loaded,
    status,
    checking,
    checkedAt,
    failedSources,
    failures,
    nextCheckAt,
    masSignedOut,
    byKey,
    actionable,
    find,
    findMas,
    rowFor,
    load,
    loadStatus,
    check,
    upgrade,
    ignoreApp,
    skip,
    setChannel,
    relaunch,
    rollbackable,
    loadRollbacks,
    rollback,
    settle
  };
});
