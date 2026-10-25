import { computed, ref } from 'vue';
import { defineStore } from 'pinia';
import type {
  HistoryCleared,
  RunningApp,
  Task,
  TaskKind,
  TaskLogEvent,
  TaskOp,
  TaskOptions,
  TaskTarget,
  TaskTrigger
} from '@/ipc/bindings';
import { commands, packageKey, unwrap } from '@/ipc/client';
import { useInstallConfirmStore } from './installConfirm';
import { useTapConfirmStore } from './tapConfirm';
import { useLibraryStore } from './library';
import { useLocalAppsStore } from './localApps';
import { useUpdatesStore } from './updates';
import { useRunningAppsStore } from './runningApps';

/** Log lines retained per task in the UI (full logs remain in task log files, 06 §6.4). */
const LOG_LIMIT = 200;
const LOG_CHAR_LIMIT = 64 * 1024;
const FINISHED_LIMIT = 20;

/** Task queue (06 §9): task:updated pushes state, task:log pushes logs; completed tasks leave the active list. */
export const useTasksStore = defineStore('tasks', () => {
  const active = ref<Task[]>([]);
  const logs = ref<Record<string, string[]>>({});
  /** Recently completed tasks in this session, for completion notices and the Updates page's just-now section. */
  const finished = ref<Task[]>([]);
  /** The first task list arrived; startup reads it in the background (stores/index.ts). */
  const loaded = ref(false);
  /** Counts clears of the stored history: lists and totals built from it reread when it changes. */
  const historyEpoch = ref(0);

  const running = computed(() => active.value.find(task => task.state === 'running'));
  const queued = computed(() => active.value.filter(task => task.state === 'queued'));
  const forPackage = (kind: TaskKind, token: string) =>
    active.value.find(task => task.target?.kind === kind && task.target.token === token);

  // A step without measurable progress (the scan before installing) reports null mid-task; small progress rings hold
  // the last known value through it instead of dropping back to 0.
  const lastPercent = new Map<string, number>();
  /** The task's percent, else its last known one; null before any is known. */
  const knownPercent = (task: Task) => task.percent ?? lastPercent.get(task.id) ?? null;
  /** 0–100 for progress rings, which never turn into spinners. */
  const shownPercent = (task: Task) => knownPercent(task) ?? 0;

  let listRead = 0;
  const pendingReads = new Set<Map<string, Task>>();
  async function load() {
    const read = ++listRead;
    const changes = new Map<string, Task>();
    pendingReads.add(changes);
    try {
      const list = await unwrap(commands.taskListActive());
      if (read !== listRead) return;
      const current = new Map(list.map(task => [task.id, task]));
      for (const task of changes.values()) {
        if (task.state === 'queued' || task.state === 'running') current.set(task.id, task);
        else current.delete(task.id);
      }
      active.value = [...current.values()];
      loaded.value = true;
      pruneLogs();
    } finally {
      pendingReads.delete(changes);
    }
  }

  /**
   * Deletes the finished records, except those that can still be undone or rolled back. Active tasks stay, and so do
   * the tasks held in memory for the screens that are showing them.
   */
  async function clearHistory(): Promise<HistoryCleared> {
    const result = await unwrap(commands.historyClear());
    historyEpoch.value += 1;
    return result;
  }

  function pruneLogs() {
    const retained = new Set([...active.value, ...finished.value].map(task => task.id));
    for (const id of Object.keys(logs.value)) {
      if (!retained.has(id)) delete logs.value[id];
    }
  }

  function applyUpdate(task: Task | null) {
    if (!task) return;
    for (const changes of pendingReads) changes.set(task.id, task);
    const index = active.value.findIndex(item => item.id === task.id);
    if (task.percent !== null) lastPercent.set(task.id, task.percent);
    if (task.state === 'queued' || task.state === 'running') {
      if (index >= 0) active.value.splice(index, 1, task);
      else active.value.push(task);
      return;
    }
    if (index >= 0) active.value.splice(index, 1);
    lastPercent.delete(task.id);
    finished.value = [task, ...finished.value.filter(item => item.id !== task.id)].slice(0, FINISHED_LIMIT);
    // In the same update as the finished task, so no frame shows its target's state from before it ran.
    useUpdatesStore().settle(task);
    useLibraryStore().settle(task);
    useLocalAppsStore().settle(task);
    pruneLogs();
  }

  function appendLog(event: TaskLogEvent) {
    // Ignore late events for evicted tasks; full logs remain available on disk.
    if (![...active.value, ...finished.value].some(task => task.id === event.id)) return;
    const lines = [...(logs.value[event.id] ?? []), ...event.lines.slice(-LOG_LIMIT)].slice(-LOG_LIMIT);
    const retained: string[] = [];
    let remaining = LOG_CHAR_LIMIT;
    for (let index = lines.length - 1; index >= 0 && remaining > 0; index -= 1) {
      const line = lines[index]!.slice(-remaining);
      retained.unshift(line);
      remaining -= line.length;
    }
    logs.value[event.id] = retained;
  }

  let upgradeTail: Promise<unknown> = Promise.resolve();

  function upgrade(
    targets: TaskTarget[],
    trigger: TaskTrigger,
    options: TaskOptions,
    batch: boolean,
    known: readonly RunningApp[] = []
  ): Promise<Task[]> {
    // Process repeated clicks in order; never replace an unanswered confirmation.
    const result = upgradeTail.then(async () => {
      const unique = [...new Map(targets.map(target => [packageKey(target.kind, target.token), target])).values()];
      const existing = unique.flatMap(target => {
        const task = forPackage(target.kind, target.token);
        return task ? [task] : [];
      });
      const fresh = unique.filter(target => !forPackage(target.kind, target.token));
      if (!fresh.length) return existing;
      const unattended = trigger === 'schedule' || trigger === 'bundle';
      // Rust finds running Homebrew apps (casks) from their installed bundles and accepts only casks. App Store and
      // directly updated apps come with their update row's running flag (known); command-line formulae have no app to
      // quit. Asking Rust about those targets would fail the whole update with E_INVALID_ARG.
      const casks = fresh.filter(target => target.kind === 'cask');
      const runningApps = unattended
        ? []
        : [
            ...(casks.length ? await unwrap(commands.appsRunning(casks)) : []),
            ...known.filter(app =>
              fresh.some(target => target.kind === app.target.kind && target.token === app.target.token)
            )
          ];
      const choice = runningApps.length
        ? await useRunningAppsStore().ask(runningApps, batch)
        : { action: 'skip' as const, reopen: false };
      if (choice.action === 'cancel') return [];
      const runningKeys = new Set(runningApps.map(app => packageKey(app.target.kind, app.target.token)));
      const created = [...existing];
      for (const target of fresh) {
        const wasRunning = runningKeys.has(packageKey(target.kind, target.token));
        if (wasRunning && choice.action === 'skip') continue;
        const quitRunning = wasRunning && choice.action === 'quit' && !unattended;
        created.push(
          await unwrap(
            commands.taskEnqueue(
              'upgrade',
              target,
              {
                ...options,
                quitRunning,
                reopen: quitRunning && choice.reopen
              },
              trigger
            )
          )
        );
      }
      return created;
    });
    upgradeTail = result.catch(() => undefined);
    return result;
  }

  let installTail: Promise<unknown> = Promise.resolve();
  const installing = new Map<string, Promise<Task | null>>();
  function install(target: TaskTarget, trigger: TaskTrigger, options: TaskOptions): Promise<Task | null> {
    const key = packageKey(target.kind, target.token);
    const pending = installing.get(key);
    if (pending) return pending;
    const result = installTail.then(async () => {
      const existing = forPackage(target.kind, target.token);
      if (existing) return existing;
      // App Store installs (store/security plan §11.3) have no Homebrew preflight: Apple's own checks run in the task.
      if (target.kind === 'mas') {
        const task = await unwrap(commands.taskEnqueue('install', target, { ...options, source: 'mas' }, trigger));
        applyUpdate(task);
        return task;
      }
      const check = await unwrap(commands.installPreflight(target));
      if (check.status === 'managed') {
        await useLibraryStore().refresh();
        await useUpdatesStore().load();
        return null;
      }
      // Homebrew has not reviewed third-party taps; installing from one is the user's call every time.
      if (check.tap) {
        if (trigger === 'schedule') return null;
        if (!(await useTapConfirmStore().ask({ name: check.name, tap: check.tap }))) return null;
      }
      let adopt = false;
      if (check.status === 'conflict' || check.status === 'blocked') {
        if (trigger === 'schedule') return null;
        adopt = await useInstallConfirmStore().ask(check);
        if (!adopt) return null;
      }
      // Old task arguments or caller-supplied adopt cannot replace consent for this action.
      const task = await unwrap(commands.taskEnqueue('install', target, { ...options, adopt }, trigger));
      applyUpdate(task);
      return task;
    });
    installing.set(key, result);
    installTail = result.catch(() => undefined);
    void result.finally(() => installing.delete(key)).catch(() => undefined);
    return result;
  }

  async function enqueue(
    op: TaskOp,
    target: TaskTarget | null,
    trigger: TaskTrigger = 'manual',
    options: TaskOptions = {}
  ) {
    if (op === 'install' && target) return install(target, trigger, options);
    if (op === 'upgrade' && target) return (await upgrade([target], trigger, options, false))[0] ?? null;
    return unwrap(commands.taskEnqueue(op, target, options, trigger));
  }

  function enqueueMany(
    op: TaskOp,
    targets: TaskTarget[],
    trigger: TaskTrigger = 'manual',
    options: TaskOptions = {},
    known: readonly RunningApp[] = []
  ) {
    if (op === 'install') {
      return Promise.all(targets.map(target => install(target, trigger, options))).then(tasks =>
        tasks.filter((task): task is Task => task !== null)
      );
    }
    if (op === 'upgrade') return upgrade(targets, trigger, options, true, known);
    return unwrap(commands.taskEnqueueMany(op, targets, options, trigger));
  }

  function cancel(id: string) {
    return unwrap(commands.taskCancel(id));
  }

  return {
    active,
    logs,
    finished,
    loaded,
    historyEpoch,
    running,
    queued,
    forPackage,
    knownPercent,
    shownPercent,
    load,
    applyUpdate,
    appendLog,
    enqueue,
    enqueueMany,
    cancel,
    clearHistory
  };
});
