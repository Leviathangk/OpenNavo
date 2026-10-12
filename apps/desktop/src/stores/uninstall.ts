import { computed, ref, watch } from 'vue';
import { defineStore } from 'pinia';
import type { AppPlan, DeleteMode, LocalApp, PlanItem, ScanProgress, Task, UninstallPlan } from '@/ipc/bindings';
import { IpcError, commands, unwrap } from '@/ipc/client';
import { useLocalAppsStore } from './localApps';
import { usePermissionsStore } from './permissions';
import { useSettingsStore } from './settings';
import { useTasksStore } from './tasks';

/**
 * Uninstall dialog (proposal §04): permission → scanning → review → submitting → running → done.
 * The backend only deletes items it planned; the UI sends planId + itemIds, never paths (§06).
 */
export type UninstallStage =
  | 'idle'
  | 'permission'
  | 'scanning'
  | 'review'
  | 'submitting'
  | 'running'
  | 'done'
  | 'failed';

/** Display groups: categories in a fixed order, low-confidence items always last under "maybe". */
export const GROUP_ORDER = [
  'app',
  'app_data',
  'caches',
  'preferences',
  'logs',
  'background',
  'cli',
  'plugins',
  'receipts',
  'maybe'
] as const;
export type GroupKey = (typeof GROUP_ORDER)[number];

export const groupOf = (item: PlanItem): GroupKey => (item.confidence === 'low' ? 'maybe' : item.category);

export interface PlanGroup {
  key: GroupKey;
  items: PlanItem[];
}

export function planGroups(app: AppPlan): PlanGroup[] {
  return GROUP_ORDER.flatMap(key => {
    const items = app.items.filter(item => groupOf(item) === key);
    return items.length ? [{ key, items }] : [];
  });
}

const sizeOf = (items: readonly PlanItem[]) => items.reduce((sum, item) => sum + (item.sizeBytes ?? 0), 0);

export interface UninstallResult {
  tasks: Task[];
  freedBytes: number;
  moved: number;
  deleted: number;
  skipped: { path: string; reason: string }[];
  failed: Task[];
  restorable: boolean;
}

export function summarize(tasks: readonly Task[]): UninstallResult {
  // Failed and canceled tasks can still have moved files and a restore manifest.
  const done = tasks.filter(task => task.state === 'succeeded' || task.state === 'failed' || task.state === 'canceled');
  return {
    tasks: [...tasks],
    freedBytes: done.reduce((sum, task) => sum + (task.outcome?.freedBytes ?? 0), 0),
    moved: done.reduce((sum, task) => sum + (task.outcome?.trashed ?? 0), 0),
    deleted: done.reduce((sum, task) => sum + (task.outcome?.deleted ?? 0), 0),
    skipped: done.flatMap(task => task.outcome?.skipped ?? []),
    failed: tasks.filter(task => task.state !== 'succeeded'),
    restorable: done.some(task => task.outcome?.restorable)
  };
}

const isFinished = (task: Task) => task.state === 'succeeded' || task.state === 'failed' || task.state === 'canceled';

/** A run the user sent to the background; it keeps its own tasks while new uninstalls start. */
interface Detached {
  key: number;
  apps: LocalApp[];
  taskIds: string[];
  tasks: Record<string, Task>;
}
/** A background run that finished, for the shell to announce with Undo. */
export interface Announcement {
  key: number;
  apps: LocalApp[];
  result: UninstallResult;
}

export const useUninstallStore = defineStore('uninstall', () => {
  const tasks = useTasksStore();
  const settings = useSettingsStore();
  const permissions = usePermissionsStore();

  const stage = ref<UninstallStage>('idle');
  const appIds = ref<string[]>([]);
  /** The apps as they were when this session started: after uninstalling they leave local_apps_list. */
  const targets = ref<LocalApp[]>([]);
  const requestId = ref<string | null>(null);
  const progress = ref<ScanProgress | null>(null);
  const plan = ref<UninstallPlan | null>(null);
  /** itemId → checked; required items are always included regardless. */
  const selected = ref<Record<string, boolean>>({});
  /** Batch: apps the user removed from this run. */
  const excluded = ref<string[]>([]);
  const mode = ref<DeleteMode>('trash');
  const taskIds = ref<string[]>([]);
  const error = ref<IpcError | null>(null);
  /** Closed while the execute request was still in flight; the run detaches once its tasks are known. */
  const background = ref(false);
  /** Shown once after a stale plan was replaced. */
  const rescanned = ref(false);
  const detached = ref<Detached[]>([]);
  const announcements = ref<Announcement[]>([]);
  let detachKey = 0;

  const open = computed(() => stage.value !== 'idle' && !background.value);
  const apps = computed(() => plan.value?.apps ?? []);
  const included = computed(() => apps.value.filter(app => !excluded.value.includes(app.appId)));
  const isChecked = (item: PlanItem) => item.required || Boolean(selected.value[item.id]);
  const chosen = computed(() => included.value.flatMap(app => app.items.filter(isChecked)));
  const chosenBytes = computed(() => sizeOf(chosen.value));
  const totalItems = computed(() => included.value.reduce((sum, app) => sum + app.items.length, 0));
  const adminCount = computed(() => chosen.value.filter(item => item.needsAdmin).length);
  const sharedKept = computed(
    () => included.value.flatMap(app => app.items).filter(item => item.sharedWith.length && !isChecked(item)).length
  );
  const onlyRequired = computed(() => chosen.value.every(item => item.required));
  /** Non-Homebrew apps need App Management to remove their bundles. */
  const blocked = computed(() => included.value.some(app => app.blockedReason !== null));

  /**
   * Item ids are stable across scans of the same files, so a rescan (expired plan, permission granted) keeps what
   * the user ticked; new items start from the defaults.
   */
  function defaults(next: UninstallPlan, previous: Record<string, boolean> = {}) {
    // "Clean related files by default" off → start from required items only; suggestions stay one click away.
    const clean = settings.value?.zapByDefault ?? true;
    selected.value = Object.fromEntries(
      next.apps.flatMap(app =>
        app.items.map(item => [
          item.id,
          item.required || (item.id in previous ? Boolean(previous[item.id]) : clean && item.selected)
        ])
      )
    );
  }

  const retainedTasks = ref<Record<string, Task>>({});

  let session = 0;
  const completedOperations = new Set<string>();

  function reset() {
    session += 1;
    stage.value = 'idle';
    appIds.value = [];
    targets.value = [];
    requestId.value = null;
    progress.value = null;
    plan.value = null;
    selected.value = {};
    excluded.value = [];
    taskIds.value = [];
    retainedTasks.value = {};
    error.value = null;
    background.value = false;
    rescanned.value = false;
  }

  async function scan() {
    if (!permissions.fullDiskReady) {
      stage.value = 'permission';
      return;
    }
    const id = crypto.randomUUID();
    const previous = plan.value ? { ...selected.value } : {};
    requestId.value = id;
    progress.value = null;
    error.value = null;
    stage.value = 'scanning';
    try {
      const next = await unwrap(commands.uninstallPlan(id, appIds.value));
      if (requestId.value !== id) return;
      plan.value = next;
      excluded.value = excluded.value.filter(appId => next.apps.some(app => app.appId === appId));
      defaults(next, previous);
      stage.value = 'review';
    } catch (cause) {
      if (requestId.value !== id) return;
      if (cause instanceof IpcError && cause.code === 'E_SCAN_CANCELED') return;
      error.value =
        cause instanceof IpcError ? cause : new IpcError({ code: 'E_UNKNOWN', message: String(cause), detail: null });
      stage.value = 'failed';
    }
  }

  /**
   * Start a session; runs in the background keep reporting on their own. Without Full Disk Access the dialog first
   * guides the permission and scans once it is on.
   * One Homebrew cask can install several apps and brew removes them together, so keep one app per cask
   * (Rust rejects duplicates); the review lists the siblings that go with it.
   */
  async function start(ids: string[]) {
    if (!ids.length) return;
    reset();
    const localApps = useLocalAppsStore();
    const casks = new Set<string>();
    appIds.value = [...new Set(ids)].filter(id => {
      const cask = localApps.find(id)?.cask;
      if (!cask) return true;
      if (casks.has(cask)) return false;
      casks.add(cask);
      return true;
    });
    targets.value = appIds.value.flatMap(id => {
      const app = localApps.find(id);
      return app ? [app] : [];
    });
    mode.value = settings.value?.uninstallDeleteMode ?? 'trash';
    if (!permissions.status) await permissions.refresh().catch(() => undefined);
    await scan();
  }

  function applyProgress(event: ScanProgress) {
    if (event.requestId === requestId.value && stage.value === 'scanning') progress.value = event;
  }

  /** Hand the running session to the background so a new one can start; its result is announced when done. */
  function detach() {
    detached.value = [
      ...detached.value,
      { key: ++detachKey, apps: targets.value, taskIds: [...taskIds.value], tasks: { ...retainedTasks.value } }
    ];
    reset();
    settleDetached();
  }

  /** Close: cancel a scan, discard a review, or keep running tasks in the background. */
  async function close() {
    if (stage.value === 'running') {
      detach();
      return;
    }
    if (stage.value === 'submitting') {
      background.value = true;
      return;
    }
    const scanning = stage.value === 'scanning' ? requestId.value : null;
    reset();
    if (scanning) await unwrap(commands.uninstallPlanCancel(scanning)).catch(() => undefined);
  }

  const setItem = (id: string, value: boolean) => {
    selected.value = { ...selected.value, [id]: value };
  };
  function setItems(items: readonly PlanItem[], value: boolean) {
    const next = { ...selected.value };
    for (const item of items) if (!item.required) next[item.id] = value;
    selected.value = next;
  }
  const selectAll = () =>
    setItems(
      apps.value.flatMap(app => app.items),
      true
    );
  const selectRequired = () =>
    setItems(
      apps.value.flatMap(app => app.items),
      false
    );
  function recommended() {
    if (!plan.value) return;
    selected.value = Object.fromEntries(
      plan.value.apps.flatMap(app => app.items.map(item => [item.id, item.required || item.selected]))
    );
  }
  function toggleApp(appId: string) {
    excluded.value = excluded.value.includes(appId)
      ? excluded.value.filter(id => id !== appId)
      : [...excluded.value, appId];
  }

  async function confirm() {
    const current = plan.value;
    if (!current || stage.value !== 'review' || !included.value.length || blocked.value) return;
    // Rust also rejects stale plans; rescanning first avoids a round trip and a confusing error.
    if (current.expiresAt <= Date.now()) {
      rescanned.value = true;
      await scan();
      return;
    }
    const epoch = session;
    stage.value = 'submitting';
    try {
      const choices = included.value.map(app => ({
        appId: app.appId,
        itemIds: app.items.filter(isChecked).map(item => item.id),
        quitRunning: true
      }));
      const created = await unwrap(commands.uninstallExecute(current.planId, choices, mode.value));
      if (epoch !== session) return;
      taskIds.value = created.map(task => task.id);
      for (const task of created) {
        const latest =
          retainedTasks.value[task.id] ??
          [...tasks.active, ...tasks.finished].find(item => item.id === task.id) ??
          task;
        retain(latest);
        if (!tasks.active.some(item => item.id === task.id) && !tasks.finished.some(item => item.id === task.id))
          tasks.applyUpdate(latest);
      }
      stage.value = 'running';
      settle();
      // Closed while the request was in flight: continue in the background now that the tasks are known.
      // `settle()` may have moved the stage on; read it again rather than trusting the narrowing above.
      const settled = stage.value as UninstallStage;
      if (background.value && settled === 'running') detach();
      else if (background.value && settled === 'done') announceCurrent();
    } catch (cause) {
      if (epoch !== session) return;
      if (cause instanceof IpcError && cause.code === 'E_PLAN_EXPIRED') {
        rescanned.value = true;
        await scan();
        return;
      }
      if (background.value) {
        reset();
        return;
      }
      stage.value = 'review';
      throw cause;
    }
  }

  function retain(task: Task) {
    const previous = retainedTasks.value[task.id];
    if (previous && (isFinished(previous) || (previous.state === 'running' && task.state === 'queued'))) return;
    retainedTasks.value[task.id] = task;
  }

  // Retain session results independently of the bounded notification cache.
  watch(
    () => [...tasks.active, ...tasks.finished],
    current => {
      for (const task of current) {
        if (
          taskIds.value.includes(task.id) ||
          (stage.value === 'submitting' &&
            task.options?.planId === plan.value?.planId &&
            (task.id in retainedTasks.value || Object.keys(retainedTasks.value).length < 100))
        )
          retain(task);
        for (const run of detached.value) {
          const previous = run.tasks[task.id];
          if (run.taskIds.includes(task.id) && !(previous && isFinished(previous))) run.tasks[task.id] = task;
        }
      }
      settleDetached();
    },
    { flush: 'sync' }
  );
  const sessionTasks = computed(() =>
    taskIds.value.flatMap(id => (retainedTasks.value[id] ? [retainedTasks.value[id]!] : []))
  );
  const result = computed(() => summarize(sessionTasks.value.filter(task => isFinished(task))));

  /** Move to done once every task of this session finished. */
  function settle() {
    if (stage.value !== 'running') return;
    const current = sessionTasks.value;
    if (current.length === taskIds.value.length && current.every(isFinished)) stage.value = 'done';
  }
  watch(sessionTasks, settle, { deep: true });

  function announceCurrent() {
    announcements.value = [
      ...announcements.value,
      { key: ++detachKey, apps: targets.value, result: summarize(sessionTasks.value) }
    ];
    reset();
  }

  /** Background runs whose tasks all finished become announcements. */
  function settleDetached() {
    const done = detached.value.filter(run => {
      const list = run.taskIds.map(id => run.tasks[id]);
      return list.every(task => task && isFinished(task));
    });
    if (!done.length) return;
    detached.value = detached.value.filter(run => !done.includes(run));
    announcements.value = [
      ...announcements.value,
      ...done.map(run => ({
        key: run.key,
        apps: run.apps,
        result: summarize(run.taskIds.flatMap(id => (run.tasks[id] ? [run.tasks[id]!] : [])))
      }))
    ];
  }
  function announced(key: number) {
    announcements.value = announcements.value.filter(item => item.key !== key);
  }

  // The permission arrived while the dialog waited for it: scan now.
  watch(
    () => permissions.fullDiskReady,
    ready => {
      if (ready && stage.value === 'permission') void scan();
    }
  );
  // Granting Full Disk Access during review rescans so containers show up (§07).
  watch(
    () => permissions.fullDiskAccess,
    (next, previous) => {
      if (next === 'granted' && previous !== 'granted' && stage.value === 'review' && plan.value?.incomplete.length)
        void scan();
    }
  );

  function candidates(ids: readonly string[]) {
    return [...new Set(ids)].filter(id => {
      const task = retainedTasks.value[id] ?? [...tasks.active, ...tasks.finished].find(t => t.id === id);
      return !completedOperations.has(id) && (!task || task.outcome?.restorable === true);
    });
  }

  function completed(id: string) {
    completedOperations.add(id);
    if (completedOperations.size > 1000) completedOperations.delete(completedOperations.values().next().value!);
    const task = retainedTasks.value[id];
    if (task?.outcome) task.outcome = { ...task.outcome, restorable: false };
  }

  function batchFailure(failures: unknown[], detail: object): IpcError {
    const first = failures[0];
    return new IpcError({
      code: first instanceof IpcError ? first.code : 'E_UNKNOWN',
      message: first instanceof Error ? first.message : String(first),
      detail: JSON.stringify({
        ...detail,
        errors: failures.map(cause =>
          cause instanceof IpcError
            ? { code: cause.code, message: cause.message, detail: cause.detail }
            : { message: String(cause) }
        )
      })
    });
  }

  async function restore(ids: readonly string[] = taskIds.value) {
    const epoch = session;
    const own = ids === taskIds.value;
    let restored = 0;
    const missing: string[] = [];
    const failures: unknown[] = [];
    for (const id of candidates(ids)) {
      try {
        const outcome = await unwrap(commands.uninstallRestore(id));
        restored += outcome.restored;
        missing.push(...outcome.missing);
        if (!outcome.missing.length) completed(id);
      } catch (cause) {
        failures.push(cause);
        let paths: string[] = [];
        if (cause instanceof IpcError && cause.detail) {
          try {
            const detail = JSON.parse(cause.detail);
            if (Array.isArray(detail.missing))
              paths = detail.missing.filter((path: unknown) => typeof path === 'string');
          } catch {
            /* Non-JSON backend errors remain visible below. */
          }
        }
        missing.push(...(paths.length ? paths : [`${id}: ${String(cause)}`]));
      }
    }
    if (!restored && failures.length) throw batchFailure(failures, { restored, missing });
    if (!restored && !missing.length)
      throw new IpcError({ code: 'E_RESTORE_UNAVAILABLE', message: 'uninstall_restore_unavailable', detail: null });
    if (own && epoch === session && !missing.length) reset();
    return { restored, missing };
  }

  async function purge(ids: readonly string[] = taskIds.value) {
    const epoch = session;
    const own = ids === taskIds.value;
    let freed = 0;
    const failures: unknown[] = [];
    for (const id of candidates(ids)) {
      try {
        freed += await unwrap(commands.uninstallPurge(id));
        completed(id);
      } catch (cause) {
        failures.push(cause);
      }
    }
    if (failures.length) throw batchFailure(failures, { freedBytes: freed });
    if (own && epoch === session) reset();
    return freed;
  }

  return {
    stage,
    appIds,
    targets,
    progress,
    plan,
    selected,
    excluded,
    mode,
    taskIds,
    error,
    background,
    rescanned,
    detached,
    announcements,
    open,
    apps,
    included,
    chosen,
    chosenBytes,
    totalItems,
    adminCount,
    sharedKept,
    onlyRequired,
    blocked,
    sessionTasks,
    result,
    isChecked,
    start,
    scan,
    applyProgress,
    close,
    reset,
    setItem,
    setItems,
    selectAll,
    selectRequired,
    recommended,
    toggleApp,
    confirm,
    announced,
    restore,
    purge
  };
});
