// Simulated task queue (06 §9): one task at a time; manual / deeplink / retry precede schedule / bundle.
// Push brew-formatted task:log and staged task:updated events; on completion update installed packages and emit library:changed / updates:changed.
// Firefox installs/updates always fail SHA-256 validation to demonstrate failure states; hooks.failure injects other scenario failures.
import { events } from '../bindings';
import { HOMEBREW_ROW } from './updater';
import type { AppError, InstalledItem, OutdatedItem, Task, TaskOp, TaskPhase } from '../bindings';
import { packageKey } from '../client';
import { downloadSizeOf, findItem, nextTaskId } from './fixtures';

export interface MockState {
  installed: InstalledItem[];
  active: Task[];
  history: Task[];
  logs: Map<string, string[]>;
  ignored: Map<string, { version: string | null; until: number | null }>;
  cacheBytes: number;
}

const FAILING = new Set(['firefox']);
const PRIORITY: Record<Task['trigger'], number> = {
  manual: 0,
  deeplink: 0,
  retry: 0,
  schedule: 1,
  bundle: 1
};

export interface Step {
  phase: TaskPhase;
  from: number;
  to: number;
  ticks: number;
  lines: string[];
  /** Cleanup: bytes freed at the start and end of the step, out of `total` (Rust reports them as bytesDone/bytesTotal). */
  freed?: { from: number; to: number; total: number };
  /** Called as the step starts, for what Rust reports at that point (e.g. the scan before installing). */
  enter?: () => void;
  /** No measurable progress, e.g. clamd scanning one whole installer: percent and bytes stay null. */
  indeterminate?: boolean;
}

/** Injected failure: emit `lines` at the first tick of zero-based `step`, then finish with `error`. */
export interface MockFailure {
  step: number;
  error: AppError;
  lines: string[];
}

export interface RunnerHooks {
  beforeRun?: (task: Task) => AppError | undefined;
  homebrewInstalled?: () => void;
  /** Homebrew installer script URL, selected from the configured source. */
  installerUrl?: () => string;
  failure?: (task: Task) => MockFailure | undefined;
  /** Called after task completion (success, failure, or cancellation). */
  finished?: (task: Task) => void;
  /** Custom steps for a task, e.g. plan-based uninstalls of local apps; default brew-shaped steps otherwise. */
  steps?: (task: Task) => Step[] | undefined;
  /** Called on success before the task is recorded and emitted, so results (task.outcome) ship with it. */
  completed?: (task: Task) => void;
  /** Scan before installing (security S2): a step right after a Homebrew download, when the scan runs. */
  scanStep?: (task: Task) => Step | undefined;
}

/** Rust scans what Homebrew fetched before installing it, so the scan follows the download. */
function withScan(plan: Step[], scan: Step | undefined): Step[] {
  const index = plan.findIndex(step => step.phase === 'downloading');
  return scan && index >= 0 ? [...plan.slice(0, index + 1), scan, ...plan.slice(index + 1)] : plan;
}

function steps(task: Task, appName: string | undefined, url: string, installerUrl: string): Step[] {
  const catalog = task.target?.kind === 'cask' ? findItem('cask', task.target.token) : undefined;
  const upstream = catalog?.brewToken ?? task.target?.token ?? '';
  const token = catalog?.tap ? `${catalog.tap}/${upstream}` : upstream;
  const target = task.target?.kind === 'cask' ? `--cask ${token}` : token;
  const download: Step = {
    phase: 'downloading',
    from: 5,
    to: 70,
    ticks: 14,
    lines: [`==> Downloading ${url}`]
  };
  switch (task.op as TaskOp) {
    case 'install':
      return [
        {
          phase: 'preparing',
          from: 0,
          to: 5,
          ticks: 2,
          lines: [`==> Fetching downloads for: ${token}`]
        },
        download,
        {
          phase: 'installing',
          from: 70,
          to: 95,
          ticks: 5,
          lines: [
            `==> Installing Cask ${token}`,
            ...(appName ? [`==> Moving App '${appName}' to '/Applications/${appName}'`] : [])
          ]
        },
        {
          phase: 'finishing',
          from: 95,
          to: 100,
          ticks: 2,
          lines: [`${token} was successfully installed!`]
        }
      ];
    case 'upgrade':
      return [
        {
          phase: 'preparing',
          from: 0,
          to: 5,
          ticks: 2,
          lines: [`==> Upgrading ${target}`, `${token} ${task.fromVersion ?? ''} -> ${task.toVersion ?? ''}`]
        },
        download,
        { phase: 'installing', from: 70, to: 95, ticks: 5, lines: [`==> Installing ${token}`] },
        {
          phase: 'cleaning',
          from: 95,
          to: 100,
          ticks: 2,
          lines: [`==> Cleaning`, `${token} was successfully upgraded!`]
        }
      ];
    case 'uninstall':
      return [
        {
          phase: 'uninstalling',
          from: 0,
          to: 90,
          ticks: 6,
          lines: [`==> Uninstalling ${task.target?.kind === 'cask' ? 'Cask ' : ''}${token}`]
        },
        {
          phase: 'finishing',
          from: 90,
          to: 100,
          ticks: 2,
          lines: [`==> Purging files for version ${task.fromVersion ?? ''} of ${token}`]
        }
      ];
    case 'update':
      return [
        {
          phase: 'updating',
          from: 0,
          to: 100,
          ticks: 8,
          lines: ['==> Updating Homebrew...', 'Already up-to-date.']
        }
      ];
    case 'install_homebrew':
      // The installer script is small and reports no byte progress (06 §12.5); use preparing without simulated transfer speed.
      return [
        {
          phase: 'preparing',
          from: 0,
          to: 10,
          ticks: 3,
          lines: [`==> Downloading the installer from ${installerUrl}`]
        },
        {
          phase: 'installing',
          from: 10,
          to: 90,
          ticks: 10,
          lines: [
            '==> Checking for `sudo` access (which may request your password)...',
            '==> This script will install:',
            '/opt/homebrew/bin/brew',
            '==> Downloading and installing Homebrew...'
          ]
        },
        {
          phase: 'finishing',
          from: 90,
          to: 100,
          ticks: 2,
          lines: ['==> Installation successful!']
        }
      ];
    case 'cleanup':
      return [
        {
          phase: 'cleaning',
          from: 0,
          to: 100,
          ticks: 6,
          lines: ['==> This operation has freed approximately 2.3GB of disk space.']
        }
      ];
    default:
      return [{ phase: 'finishing', from: 0, to: 100, ticks: 2, lines: [`==> ${task.op} ${target}`] }];
  }
}

export function createRunner(state: MockState, tickMs = 200, hooks: RunnerHooks = {}) {
  let running: Task | undefined;
  let stopped = false;
  const timers = new Set<ReturnType<typeof setTimeout>>();
  const later = (advance: () => void) => {
    if (stopped) return;
    const timer = setTimeout(() => {
      timers.delete(timer);
      advance();
    }, tickMs);
    timers.add(timer);
  };

  const emitTask = (task: Task) => void events.taskUpdated.emit({ ...task });
  const log = (task: Task, lines: string[]) => {
    const buffer = state.logs.get(task.id) ?? [];
    buffer.push(...lines);
    state.logs.set(task.id, buffer);
    void events.taskLog.emit({ id: task.id, lines });
  };

  function outdated(): OutdatedItem[] {
    return state.installed.flatMap(item => {
      if (item.status !== 'outdated' && item.status !== 'pinned') return [];
      const ignored = state.ignored.get(packageKey(item.kind, item.token));
      if (ignored && (ignored.version === null || ignored.version === item.latestVersion)) return [];
      const catalogItem = findItem(item.kind, item.token);
      return [
        {
          ...HOMEBREW_ROW,
          kind: item.kind,
          token: item.token,
          name: item.name,
          installedVersion: item.installedVersion,
          currentVersion: item.latestVersion ?? item.installedVersion,
          autoUpdates: item.autoUpdates,
          pinned: item.pinned,
          ignored: false,
          dependents: item.requiredBy.length,
          downloadSize: catalogItem ? downloadSizeOf(catalogItem) : null
        }
      ];
    });
  }

  function applyResult(task: Task) {
    const target = task.target;
    // Local apps outside Homebrew and App Store apps are not library entries; hooks.completed applies their results.
    if (!target || target.kind === 'app' || target.kind === 'mas') return;
    const index = state.installed.findIndex(
      item => item.kind === target.kind && (item.token === target.token || item.fullToken === target.token)
    );
    const catalogItem = findItem(target.kind, state.installed[index]?.token ?? target.token);
    switch (task.op) {
      case 'install':
        if (index < 0 && catalogItem) {
          state.installed.push({
            kind: target.kind,
            token: target.token,
            fullToken:
              target.kind === 'cask'
                ? `${catalogItem.tap ?? 'homebrew/cask'}/${catalogItem.brewToken ?? target.token}`
                : undefined,
            name: catalogItem.displayName['en-US'] ?? catalogItem.name,
            installedVersion: catalogItem.version,
            actualVersion: target.kind === 'cask' ? catalogItem.version : null,
            latestVersion: catalogItem.version,
            status: 'up_to_date',
            onRequest: true,
            requiredBy: [],
            installedAt: Date.now(),
            sizeBytes: downloadSizeOf(catalogItem) * 2,
            appPaths: catalogItem.apps[0] ? [`/Applications/${catalogItem.apps[0]}`] : [],
            iconPath: null,
            autoUpdates: catalogItem.autoUpdates,
            pinned: false
          });
        }
        break;
      case 'upgrade': {
        const item = state.installed[index];
        if (item) {
          item.installedVersion = item.latestVersion ?? item.installedVersion;
          item.actualVersion = item.kind === 'cask' ? item.installedVersion : null;
          item.status = 'up_to_date';
        }
        break;
      }
      case 'uninstall':
        if (index >= 0) state.installed.splice(index, 1);
        break;
      case 'pin':
      case 'unpin': {
        const item = state.installed[index];
        if (item) {
          item.pinned = task.op === 'pin';
          const behind = item.installedVersion !== item.latestVersion;
          item.status = item.pinned ? 'pinned' : behind ? 'outdated' : 'up_to_date';
        }
        break;
      }
      case 'cleanup':
        state.cacheBytes = 0;
        break;
    }
  }

  function applyGlobal(task: Task) {
    if (task.op === 'install_homebrew') hooks.homebrewInstalled?.();
  }

  function finish(task: Task, outcome: 'succeeded' | 'failed' | 'canceled') {
    task.state = outcome;
    task.finishedAt = Date.now();
    task.exitCode = outcome === 'succeeded' ? 0 : outcome === 'failed' ? 1 : null;
    if (outcome === 'succeeded') {
      task.percent = 100;
      applyResult(task);
      applyGlobal(task);
      hooks.completed?.(task);
    }
    if (outcome === 'canceled' && !task.error)
      task.error = { code: 'E_INTERRUPTED', message: 'canceled', detail: null };
    state.active = state.active.filter(item => item.id !== task.id);
    state.history.unshift({ ...task });
    emitTask(task);
    hooks.finished?.(task);
    if (outcome === 'succeeded') void events.libraryChanged.emit({ reason: 'task' });
    void events.updatesChanged.emit({ count: outdated().length });
    running = undefined;
    schedule();
  }

  function run(task: Task) {
    running = task;
    const blocked = hooks.beforeRun?.(task);
    if (blocked) {
      task.error = blocked;
      finish(task, 'canceled');
      return;
    }
    const target = task.target;
    const catalogItem =
      target && target.kind !== 'app' && target.kind !== 'mas' ? findItem(target.kind, target.token) : undefined;
    const appName = catalogItem?.apps[0];
    const url = `https://example.test/downloads/${target?.token ?? 'brew'}-${task.toVersion ?? 'latest'}.zip`;
    const plan =
      hooks.steps?.(task) ?? withScan(steps(task, appName, url, hooks.installerUrl?.() ?? url), hooks.scanStep?.(task));
    const failure = hooks.failure?.(task);
    const total = catalogItem ? downloadSizeOf(catalogItem) : 48_000_000;
    task.state = 'running';
    task.startedAt = Date.now();
    task.stepCount = plan.length;
    let stepIndex = 0;
    let tick = 0;
    log(task, plan[0]?.lines ?? []);
    plan[0]?.enter?.();

    const advance = () => {
      if (stopped || task.state === 'canceled') return;
      const step = plan[stepIndex];
      if (!step) {
        finish(task, 'succeeded');
        return;
      }
      if (failure && failure.step === stepIndex) {
        task.phase = step.phase;
        task.error = failure.error;
        log(task, failure.lines);
        finish(task, 'failed');
        return;
      }
      tick += 1;
      const ratio = Math.min(1, tick / step.ticks);
      task.phase = step.phase;
      task.stepIndex = stepIndex + 1;
      task.percent = step.indeterminate ? null : Math.round(step.from + (step.to - step.from) * ratio);
      if (step.phase === 'downloading') {
        task.bytesTotal = total;
        task.bytesDone = Math.round(total * ratio);
        task.speedBps = Math.round(total / ((step.ticks * tickMs) / 1000));
        const bar = '#'.repeat(Math.round(ratio * 40));
        log(task, [`${bar.padEnd(40)} ${(ratio * 100).toFixed(1)}%`]);
        if (FAILING.has(task.target?.token ?? '') && ratio >= 0.4) {
          task.error = { code: 'E_CHECKSUM', message: 'SHA256 mismatch', detail: null };
          log(task, ['Error: SHA256 mismatch']);
          finish(task, 'failed');
          return;
        }
      } else if (step.freed) {
        task.bytesTotal = step.freed.total;
        task.bytesDone = Math.round(step.freed.from + (step.freed.to - step.freed.from) * ratio);
        task.speedBps = null;
      } else {
        task.bytesDone = null;
        task.speedBps = null;
      }
      emitTask(task);
      if (tick >= step.ticks) {
        stepIndex += 1;
        tick = 0;
        const next = plan[stepIndex];
        if (next) {
          log(task, next.lines);
          next.enter?.();
        }
      }
      later(advance);
    };
    emitTask(task);
    later(advance);
  }

  function schedule() {
    if (stopped || running) return;
    const next = [...state.active]
      .filter(task => task.state === 'queued')
      .sort((a, b) => PRIORITY[a.trigger] - PRIORITY[b.trigger] || a.createdAt - b.createdAt)[0];
    if (next) run(next);
  }

  function enqueue(
    op: TaskOp,
    target: Task['target'],
    options: Task['options'],
    trigger: Task['trigger'],
    targetName: string | null = null
  ): Task {
    // Return the existing task when the package is already queued/running (06 §9).
    const existing =
      target && state.active.find(task => task.target?.kind === target.kind && task.target.token === target.token);
    // Return a snapshot as IPC does; subsequent changes arrive only as events. Re-enqueuing restored tasks starts scheduling.
    if (existing) {
      queueMicrotask(schedule);
      return { ...existing };
    }
    const installed = target && state.installed.find(item => item.kind === target.kind && item.token === target.token);
    const catalogItem =
      target && target.kind !== 'app' && target.kind !== 'mas' ? findItem(target.kind, target.token) : undefined;
    const now = Date.now();
    const task: Task = {
      id: nextTaskId(now),
      op,
      target,
      targetName,
      outcome: null,
      options,
      trigger,
      state: 'queued',
      phase: null,
      percent: null,
      bytesDone: null,
      bytesTotal: null,
      speedBps: null,
      stepIndex: null,
      stepCount: null,
      fromVersion: installed?.installedVersion ?? null,
      toVersion: op === 'install' || op === 'upgrade' ? (catalogItem?.version ?? null) : null,
      error: null,
      exitCode: null,
      logPath: null,
      createdAt: now,
      startedAt: null,
      finishedAt: null
    };
    state.active.push(task);
    emitTask(task);
    queueMicrotask(schedule);
    return { ...task };
  }

  function cancel(id: string) {
    const task = state.active.find(item => item.id === id);
    if (!task) return;
    if (task === running) log(task, ['^C', 'Error: Interrupted']);
    finish(task, 'canceled');
  }

  /** Retire this session without emitting task results or starting any queued work. */
  function stop() {
    stopped = true;
    for (const timer of timers) clearTimeout(timer);
    timers.clear();
  }

  return { enqueue, cancel, outdated, stop };
}
