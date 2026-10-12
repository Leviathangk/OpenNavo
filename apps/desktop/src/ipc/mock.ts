// Simulated IPC for browser mode (pnpm dev:web) and component tests (06 §16): implement all 06 §5.2 commands with fixtures and push staged task events.
// ?mock=no-brew simulates a Mac without Homebrew; ?mock=first-run starts with fresh settings (onboarding). See installMockIpc for other switches.
import type { invoke } from '@tauri-apps/api/core';
import { mockIPC, mockWindows } from '@tauri-apps/api/mocks';
import { LOCALES, matchLocale } from '@opennavo/shared';
import type { Locale } from '@opennavo/shared';
import type {
  AppInfo,
  CatalogStatus,
  EnvInfo,
  HistoryQuery,
  Kind,
  ListQuery,
  Permissions,
  PrivacyPane,
  SearchQuery,
  Task,
  TaskTarget,
  UpdatesStatus
} from './bindings';
import { events, packageKey } from './client';
import type { AppSettings } from './client';
import { getCatalogItem, listCatalog, listCategories, searchCatalog } from './mock-data/catalog';
import {
  catalogCursor,
  catalogItems,
  envFixture,
  findItem,
  historyFixture,
  installedFixture,
  storageFixture
} from './mock-data/fixtures';
import { createRunner } from './mock-data/runner';
import { createCleanupMock } from './mock-data/cleanup';
import { createMonitorMock } from './mock-data/monitor';
import { createUninstallMock } from './mock-data/uninstall';
import type { MockFailure, MockState } from './mock-data/runner';

const appInfo: AppInfo = { version: '0.0.0', os: 'macos', arch: 'aarch64' };

/** Homebrew installer script URLs (match Rust installer.rs: official uses GitHub; mirrors initially use USTC's copy). */
const OFFICIAL_INSTALLER = 'https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh';
const MIRROR_INSTALLER = 'https://mirrors.ustc.edu.cn/misc/brew-install.sh';

function defaultSettings(): AppSettings {
  return {
    onboardingCompleted: false,
    locale: 'en-US',
    localeMode: 'system',
    launchAtLogin: false,
    keepInMenuBarOnClose: true,
    trayShowCount: true,
    autoCheck: true,
    autoCheckAppUpdates: true,
    checkTime: '09:00',
    runBrewUpdateOnCheck: true,
    includeGreedy: true,
    autoUpgradeFormulae: true,
    autoUpgradeCasks: false,
    notifyUpdates: true,
    customMirrors: [],
    // Match Rust defaults: official source, four empty URLs (06 §11).
    mirror: {
      key: 'official',
      apiDomain: null,
      bottleDomain: null,
      brewGitRemote: null,
      coreGitRemote: null
    },
    brewPath: null,
    homebrewAnalytics: null,
    crashReports: false,
    // Match Rust defaults for new installs: clean related files, move them to the Trash.
    zapByDefault: true,
    uninstallDeleteMode: 'trash',
    // Match Rust defaults (cleanup decisions 3 and 6): review items go to the Trash, reminders off.
    cleanupDeleteMode: 'trash',
    cleanupReminder: 'off',
    cleanupReminderBytes: 10_000_000_000,
    cleanupProjectRoots: ['~/Projects', '~/Developer', '~/code', '~/GitHub', '~/dev'],
    monitorMenuBar: {
      cpu: { enabled: true, style: 'text' },
      gpu: { enabled: false, style: 'text' },
      memory: { enabled: true, style: 'text' },
      disk: { enabled: false, style: 'text' },
      network: { enabled: false, style: 'text' },
      battery: { enabled: false, style: 'text' },
      sensors: { enabled: false, style: 'value' }
    },
    monitorIntervalMs: 2000,
    monitorAlerts: false,
    monitorMenuBarIntro: false,
    fanLastManual: null
  };
}

type Args = Record<string, unknown>;

/**
 * Failures injected by ?mock= switches (first-launch-onboarding F6): codes, messages, and log format match Rust.
 * 06 §6.6, §12.5: mirrors try USTC first, then the official script; retain the last error if all fail.
 */
function mockFailure(
  task: Task,
  flags: ReadonlySet<string>,
  mirrorKey: string,
  permissions: Permissions
): MockFailure | undefined {
  if (task.op === 'install_homebrew' && flags.has('brew-install-fail-download'))
    return {
      step: 0,
      error: {
        code: 'E_DOWNLOAD',
        message: 'installer_timeout',
        detail: 'timeout'
      },
      lines:
        mirrorKey === 'official'
          ? ['==> Installer download failed (timeout)']
          : [
              '==> Installer download failed (timeout)',
              `==> Falling back to ${OFFICIAL_INSTALLER} (timeout)`,
              `==> Downloading the installer from ${OFFICIAL_INSTALLER}`,
              '==> Installer download failed (timeout)'
            ]
    };
  if (task.op === 'install_homebrew' && flags.has('brew-install-fail-sudo'))
    return {
      step: 1,
      error: { code: 'E_SUDO', message: 'sudo', detail: null },
      lines: ['sudo: no password was provided', 'sudo: a password is required']
    };
  if (
    task.op === 'install' &&
    ((flags.has('adopt-fails') && task.options.adopt) || (flags.has('conflict-after-check') && !task.options.adopt))
  ) {
    return {
      step: 1,
      error: { code: 'E_APP_EXISTS', message: 'app_exists', detail: null },
      lines: ["Error: It seems there is already an App at '/Applications/Example.app'."]
    };
  }
  const target = task.target;
  const app =
    target && target.kind !== 'app' ? (findItem(target.kind, target.token)?.apps[0] ?? `${target.token}.app`) : '';
  // zap hits Full Disk Access-protected directories (06 §12.6): the app is already moved when Homebrew exits during data removal.
  if (task.op === 'uninstall' && target?.kind === 'cask' && task.options.zap && permissions.fullDiskAccess === 'denied')
    return {
      step: 1,
      error: {
        code: 'E_FULL_DISK_ACCESS',
        message: 'full_disk_access',
        detail: null
      },
      lines: [
        `==> Backing up App '${app}' to '/opt/homebrew/Caskroom/${target.token}/1.0/${app}'`,
        `==> Removing App '/Applications/${app}'`,
        '==> Dispatching zap stanza',
        'Error: Unable to remove some files. Please enable Full Disk Access for your terminal under System Settings → Privacy & Security → Full Disk Access.'
      ]
    };
  if (
    flags.has('no-app-management') &&
    permissions.appManagement !== 'granted' &&
    target?.kind === 'cask' &&
    (task.op === 'upgrade' || task.op === 'uninstall')
  ) {
    return {
      // The system blocks updates during installation and uninstalls at the first /Applications modification.
      step: task.op === 'upgrade' ? 2 : 0,
      error: { code: 'E_PERMISSION', message: 'app_management', detail: null },
      lines: [`Error: /Applications/${app}: Operation not permitted`]
    };
  }
  return undefined;
}

/** Next automatic check: use the configured time today or tomorrow if elapsed (Rust's scheduler supplies real values). */
function nextCheckAt(settings: AppSettings, now = Date.now()): number | null {
  if (!settings.autoCheck) return null;
  const [hours, minutes] = settings.checkTime.split(':').map(Number);
  const next = new Date(now);
  next.setHours(hours ?? 9, minutes ?? 0, 0, 0);
  if (next.getTime() <= now) next.setDate(next.getDate() + 1);
  return next.getTime();
}

export function installMockIpc(
  options: {
    tickMs?: number;
    brew?: boolean;
    systemLocale?: Locale;
    persistSettings?: boolean;
  } = {}
) {
  const params = new URLSearchParams(typeof location === 'undefined' ? '' : location.search);
  const systemLocale =
    options.systemLocale ??
    LOCALES.find(code => code === params.get('mock-system-locale')) ??
    matchLocale(typeof navigator === 'undefined' ? [] : navigator.languages);
  const persistSettings = options.persistSettings ?? import.meta.env.MODE !== 'test';
  const storageKey = 'ONV_DESKTOP_mock_settings';
  // fresh means new settings; as in Rust, old settings missing onboardingCompleted count as onboarded (06 §11).
  const normalizeSettings = (input: Partial<AppSettings>, fresh = false): AppSettings => {
    const localeMode =
      input.localeMode ?? (input.locale === undefined || input.locale === systemLocale ? 'system' : 'manual');
    return {
      ...defaultSettings(),
      ...input,
      onboardingCompleted: input.onboardingCompleted ?? !fresh,
      localeMode,
      locale: localeMode === 'system' ? systemLocale : (input.locale ?? 'en-US')
    };
  };
  // Comma-separated ?mock= switches: no-brew, first-run (fresh settings), slow-official / official-down (slow/failed official probe), slow-mirrors (mirrors slower than official),
  // brew-install-fail-download / brew-install-fail-sudo (Homebrew install failures), no-app-management (permission denied for app update/uninstall),
  // full_disk_access / no-permission-check / permission-relaunch (privacy permissions, below), restore, update.
  const flags = new Set(
    typeof location !== 'undefined' ? (new URLSearchParams(location.search).get('mock') ?? '').split(',') : []
  );
  // first-run neither reads nor writes persisted simulated settings, avoiding changes to other tabs at the same URL.
  const persist = persistSettings && !flags.has('first-run');
  const noBrew = options.brew === false || flags.has('no-brew');
  const installed = noBrew ? [] : installedFixture();
  const state: MockState = {
    installed,
    active: [],
    history: noBrew ? [] : historyFixture(),
    logs: new Map(),
    ignored: new Map(),
    cacheBytes: storageFixture(installed).cacheBytes
  };
  // Privacy permissions (06 §12.3, §12.6, matching Rust preflight): App Management allowed by default; Full Disk Access disabled.
  // no-brew / first-run / no-app-management start without App Management; full_disk_access enables Full Disk Access.
  // no-permission-check simulates unavailable preflight (unknown state); permission-relaunch requires reopening after permission is enabled.
  let permissions: Permissions = {
    appManagement: noBrew || flags.has('first-run') || flags.has('no-app-management') ? 'denied' : 'granted',
    fullDiskAccess: flags.has('full_disk_access') ? 'granted' : 'denied',
    relaunchRequired: false
  };
  if (flags.has('full_disk_access')) permissions.appManagement = 'granted';
  if (flags.has('no-permission-check'))
    permissions = {
      appManagement: 'unknown',
      fullDiskAccess: 'unknown',
      relaunchRequired: false
    };
  let env: EnvInfo = {
    ...envFixture(),
    appManagement: permissions.appManagement
  };
  if (noBrew) env = { ...env, brew: null };
  const setEnv = (next: EnvInfo) => {
    env = next;
    void events.envChanged.emit(env);
  };
  const setPermissions = (next: Permissions) => {
    permissions = next;
    void events.permissionsChanged.emit(permissions);
    if (env.appManagement !== next.appManagement) setEnv({ ...env, appManagement: next.appManagement });
  };
  // After opening guidance, simulate the user enabling permission in System Settings after a delay (tests shorten it with tickMs).
  const guideDelayMs = options.tickMs === undefined ? 2200 : options.tickMs * 10;
  const grant = (pane: PrivacyPane) => {
    if (flags.has('permission-relaunch')) {
      setPermissions({ ...permissions, relaunchRequired: true });
      return;
    }
    if (pane === 'full_disk_access')
      setPermissions({
        ...permissions,
        fullDiskAccess: 'granted',
        appManagement: 'granted'
      });
    else if (pane === 'app_management') setPermissions({ ...permissions, appManagement: 'granted' });
  };
  const runningApps = new Set(
    flags.has('running-apps') || flags.has('quit-fails') ? ['ghostty', 'zed', 'google-chrome'] : []
  );
  const reopenApps = new Map<string, string>();
  // Created after the runner because it queues tasks; runner hooks only call it once tasks run.
  let uninstall: ReturnType<typeof createUninstallMock> | undefined;
  let cleanup: ReturnType<typeof createCleanupMock> | undefined;
  const runner = createRunner(state, options.tickMs, {
    steps: task => uninstall?.hooks.steps(task) ?? cleanup?.hooks.steps(task),
    completed: task => {
      uninstall?.hooks.completed(task);
      cleanup?.hooks.completed(task);
    },
    beforeRun: task => {
      const token = task.target?.token;
      if (task.op !== 'upgrade' || !token) return;
      if (flags.has('running-after-check')) runningApps.add(token);
      if (!runningApps.has(token)) return;
      if (!task.options.quitRunning || task.trigger === 'schedule' || task.trigger === 'bundle')
        return { code: 'E_APP_RUNNING', message: 'app_running', detail: null };
      if (flags.has('quit-fails'))
        return {
          code: 'E_APP_QUIT_FAILED',
          message: 'quit_refused',
          detail: null
        };
      runningApps.delete(token);
      if (task.options.reopen) reopenApps.set(task.id, token);
    },
    finished: task => {
      const token = reopenApps.get(task.id);
      if (token && task.state === 'succeeded') runningApps.add(token);
      reopenApps.delete(task.id);
    },
    // Simulate completed Homebrew installation: brew appears in the environment and emits env:changed, as Rust does.
    homebrewInstalled: () => setEnv({ ...envFixture(), appManagement: env.appManagement }),
    installerUrl: () => (settings.mirror.key === 'official' ? OFFICIAL_INSTALLER : MIRROR_INSTALLER),
    failure: task => mockFailure(task, flags, settings.mirror.key, permissions)
  });
  uninstall = createUninstallMock({
    state,
    tickMs: options.tickMs ?? 200,
    permissions: () => permissions,
    enqueue: (op, target, taskOptions, targetName) => runner.enqueue(op, target, taskOptions, 'manual', targetName)
  });
  cleanup = createCleanupMock({
    state,
    tickMs: options.tickMs ?? 200,
    permissions: () => permissions,
    apps: () => uninstall?.apps() ?? [],
    running: () => runningApps,
    deleteMode: () => settings.cleanupDeleteMode,
    flags,
    enqueue: (op, target, taskOptions, targetName) => runner.enqueue(op, target, taskOptions, 'manual', targetName)
  });
  const cleanupMock = cleanup;
  let settings = normalizeSettings({}, true);
  if (persist) {
    try {
      const saved: Partial<AppSettings> | null = JSON.parse(localStorage.getItem(storageKey) ?? 'null');
      if (saved) settings = normalizeSettings(saved);
    } catch {
      /* Simulated settings remain usable when browser storage is unavailable. */
    }
  }
  const onStorage = (event: StorageEvent) => {
    if (event.key !== storageKey || !event.newValue) return;
    try {
      const before = settings.locale;
      settings = normalizeSettings(JSON.parse(event.newValue) as Partial<AppSettings>);
      if (before !== settings.locale) void events.localeChanged.emit({ locale: settings.locale });
    } catch {
      /* Ignore corrupt settings from other tabs. */
    }
  };
  if (persist) window.addEventListener('storage', onStorage);
  import.meta.hot?.dispose(() => window.removeEventListener('storage', onStorage));
  const monitor = createMonitorMock({
    tickMs: options.tickMs ?? 250,
    flags,
    settings: () => settings,
    // Rust remembers the last manual fan speeds itself; mirror that into the simulated settings.
    updateSettings: patch => {
      settings = normalizeSettings({ ...settings, ...patch });
    }
  });
  import.meta.hot?.dispose(monitor.stop);
  let checkedAt: number | null = noBrew ? null : Date.now() - 2 * 60 * 60 * 1000;
  // ?mock=restore: two tasks were queued on last exit (startup recovery prompt).
  if (flags.has('restore')) {
    const before = Date.now() - 60 * 60 * 1000;
    for (const token of ['wget', 'node']) {
      state.active.push({
        id: `restore-${token}`,
        op: 'upgrade',
        target: { kind: 'formula', token },
        targetName: null,
        outcome: null,
        options: {},
        trigger: 'manual',
        state: 'queued',
        phase: null,
        percent: null,
        bytesDone: null,
        bytesTotal: null,
        speedBps: null,
        stepIndex: null,
        stepCount: null,
        fromVersion: null,
        toVersion: null,
        error: null,
        exitCode: null,
        logPath: null,
        createdAt: before,
        startedAt: null,
        finishedAt: null
      });
    }
  }
  let catalogStatus: CatalogStatus = {
    cursor: catalogCursor,
    itemCount: catalogItems.length,
    syncedAt: Date.now() - 5 * 60 * 1000,
    syncing: false
  };

  const handlers: Record<string, (args: Args) => unknown> = {
    ...uninstall.handlers,
    ...cleanupMock.handlers,
    ...monitor.handlers,
    // Clean tasks keep their own Trash manifests; everything else is an uninstall or leftovers task.
    uninstall_restore: args =>
      cleanupMock.restore(args.taskId as string) ?? uninstall?.handlers.uninstall_restore?.(args),
    uninstall_purge: args => cleanupMock.purge(args.taskId as string) ?? uninstall?.handlers.uninstall_purge?.(args),
    app_info: () => appInfo,
    locale_get: () => settings.locale,
    system_locale_get: () => systemLocale,
    env_detect: () => env,
    catalog_status: () => catalogStatus,
    catalog_sync: () => {
      catalogStatus = { ...catalogStatus, syncedAt: Date.now() };
      return { mode: 'none', applied: 0, cursor: catalogCursor };
    },
    catalog_search: args => searchCatalog(args.query as SearchQuery, settings.locale),
    catalog_list: args => listCatalog(args.query as ListQuery, settings.locale),
    catalog_get: args => getCatalogItem(args.kind as Kind, args.token as string),
    catalog_categories: () => listCategories(settings.locale),
    library_list: () => state.installed,
    library_refresh: () => state.installed,
    library_storage: () => ({
      ...storageFixture(state.installed),
      cacheBytes: state.cacheBytes
    }),
    updates_list: () => runner.outdated(),
    updates_status: (): UpdatesStatus => ({
      checkedAt,
      nextCheckAt: nextCheckAt(settings),
      checking: false
    }),
    updates_check: () => {
      checkedAt = Date.now();
      return runner.outdated();
    },
    updates_ignore: args => {
      const target = args.target as TaskTarget;
      state.ignored.set(packageKey(target.kind, target.token), {
        version: (args.version as string | null) ?? null,
        until: (args.until as number | null) ?? null
      });
      return null;
    },
    updates_unignore: args => {
      const target = args.target as TaskTarget;
      state.ignored.delete(packageKey(target.kind, target.token));
      return null;
    },
    install_preflight: args => {
      const target = args.target as TaskTarget;
      if (target.kind !== 'cask') throw { code: 'E_INVALID_ARG', message: 'cask_only', detail: null };
      if (flags.has('install-check-fails')) throw { code: 'E_TIMEOUT', message: 'brew_read_timeout', detail: null };
      const item = getCatalogItem(target.kind, target.token);
      const managed = state.installed.some(localItem => localItem.token === target.token);
      const conflict =
        flags.has('existing-app') ||
        flags.has('install-blocked') ||
        flags.has('adopt-fails') ||
        (flags.has('conflict-after-check') &&
          state.history.some(task => task.target?.token === target.token && task.error?.code === 'E_APP_EXISTS'));
      return {
        name: item?.name ?? target.token,
        paths: conflict ? [`/Applications/${item?.name ?? target.token}.app`] : [],
        status: managed ? 'managed' : flags.has('install-blocked') ? 'blocked' : conflict ? 'conflict' : 'ready'
      };
    },
    apps_running: args =>
      (args.targets as TaskTarget[])
        .filter(target => runningApps.has(target.token))
        .map(target => ({
          target,
          name: state.installed.find(item => item.token === target.token)?.name ?? target.token
        })),
    task_enqueue: args =>
      runner.enqueue(
        args.op as Task['op'],
        (args.target as TaskTarget | null) ?? null,
        args.options as Task['options'],
        args.trigger as Task['trigger']
      ),
    task_enqueue_many: args =>
      (args.targets as TaskTarget[]).map(target =>
        runner.enqueue(args.op as Task['op'], target, args.options as Task['options'], args.trigger as Task['trigger'])
      ),
    task_cancel: args => {
      runner.cancel(args.id as string);
      return null;
    },
    task_list_active: () => state.active.map(task => ({ ...task })),
    task_log: args => {
      const lines = state.logs.get(args.id as string) ?? [];
      const tail = args.tail as number | null;
      return (tail ? lines.slice(-tail) : lines).join('\n');
    },
    history_list: args => {
      const query = args.query as HistoryQuery;
      if (query.limit > 200) throw { code: 'E_INVALID_ARG', message: 'history_query', detail: null };
      const q = query.q?.toLowerCase();
      const items = state.history
        .filter(task => !query.ops.length || query.ops.includes(task.op))
        .filter(task => !query.states.length || query.states.includes(task.state))
        .filter(
          task => !query.target || (task.target?.kind === query.target.kind && task.target.token === query.target.token)
        )
        .filter(task => query.includeScheduledUpdates || !(task.op === 'update' && task.trigger === 'schedule'))
        .filter(task => query.from === null || (task.finishedAt ?? 0) >= query.from)
        .filter(task => query.to === null || (task.finishedAt ?? 0) <= query.to)
        .filter(
          task => !q || (task.target?.token ?? '').includes(q) || (task.targetName ?? '').toLowerCase().includes(q)
        );
      return {
        total: items.length,
        items: items.slice(query.offset, query.offset + query.limit)
      };
    },
    history_clear: () => {
      const count = state.history.length;
      state.history = [];
      return count;
    },
    history_export_csv: () => state.history.length,
    settings_get: () => settings,
    settings_set: args => {
      const before = settings.locale;
      settings = normalizeSettings(args.settings as Partial<AppSettings>);
      if (persist) {
        try {
          localStorage.setItem(storageKey, JSON.stringify(settings));
        } catch {
          /* Affects cross-tab simulation only. */
        }
      }
      if (before !== settings.locale) void events.localeChanged.emit({ locale: settings.locale });
      return settings;
    },
    mirror_probe: args =>
      (args.mirrors as { key: string }[]).map((mirror, index) =>
        mirror.key === 'official' && flags.has('official-down')
          ? {
              key: mirror.key,
              ok: false,
              latencyMs: null,
              status: null,
              error: 'timeout'
            }
          : {
              key: mirror.key,
              ok: true,
              // slow-mirrors makes every mirror slower than official, demonstrating the official default selection.
              latencyMs:
                mirror.key === 'official'
                  ? flags.has('slow-official')
                    ? 2400
                    : 324
                  : (flags.has('slow-mirrors') ? 800 : 23) + index * 4,
              status: 200,
              error: null
            }
      ),
    doctor_run: () => ({
      ok: false,
      warnings: [
        {
          title: 'You have unlinked kegs in your Cellar.',
          body: 'Leaving kegs unlinked can lead to build-trouble.\n  python@3.12'
        },
        {
          title: 'Some installed casks are deprecated or disabled.',
          body: '  keepingyouawake'
        }
      ],
      ranAt: Date.now()
    }),
    brewfile_export: args => (args.targets ? (args.targets as unknown[]).length : state.installed.length),
    // Browser mode has no filesystem: restore previews a fixed Brewfile with installed, installable, unsupported, and missing entries.
    brewfile_preview: () => {
      const lines = [
        'tap "homebrew/cask"',
        'brew "git"',
        'brew "fzf"',
        'brew "neovim"',
        'cask "ghostty"',
        'cask "zed"',
        'mas "Xcode", id: 497799835',
        'cask "no-such-app"'
      ];
      const entries = lines.map((raw, index) => {
        const match = /^(brew|cask) "([^"]+)"/.exec(raw);
        if (!match)
          return {
            line: index + 1,
            raw,
            kind: null,
            token: null,
            status: raw.startsWith('tap "homebrew/') ? 'skipped' : 'unsupported',
            reason: raw.split(' ')[0] ?? null
          };
        const kind = match[1] === 'cask' ? 'cask' : 'formula';
        const token = match[2] ?? '';
        const known = getCatalogItem(kind, token);
        const present = state.installed.some(item => item.kind === kind && item.token === token);
        return {
          line: index + 1,
          raw,
          kind,
          token,
          status: !known ? 'not_found' : present ? 'installed' : 'ready',
          reason: null
        };
      });
      return {
        entries,
        ready: entries.filter(entry => entry.status === 'ready').length,
        installed: entries.filter(entry => entry.status === 'installed').length,
        unsupported: entries.filter(entry => entry.status === 'unsupported').length
      };
    },
    brewfile_install: args =>
      (args.targets as TaskTarget[]).map(target => runner.enqueue('install', target, {}, 'bundle')),
    app_open: () => null,
    app_reveal: () => null,
    system_open_privacy: () => null,
    app_quit: () => null,
    permissions_get: () => permissions,
    permission_guide_open: args => {
      const pane = args.pane as PrivacyPane;
      setTimeout(() => grant(pane), guideDelayMs);
      return null;
    },
    permission_guide_close: () => null,
    permission_guide_info: () => ({ draggable: true }),
    permission_guide_drag: () => null,
    // Self-update (a new version exists with ?mock=update); downloads emit no progress.
    'plugin:updater|check': () =>
      flags.has('update')
        ? {
            rid: 1,
            currentVersion: appInfo.version,
            version: '0.2.0',
            date: '2026-10-05T00:00:00Z',
            body: 'Release notes (browser mock)',
            rawJson: {}
          }
        : null,
    'plugin:updater|download_and_install': () => null,
    // Rust refuses restart with running or queued tasks (active_tasks error).
    'plugin:process|restart': () => {
      if (state.active.length) throw new Error('active_tasks');
      return null;
    },
    // Browser mode has one window; tray show/hide and main-window events need no handling.
    'plugin:window|get_all_windows': () => ['main'],
    'plugin:event|emit_to': () => null
  };

  mockWindows('main');
  mockIPC(
    (command, payload) => {
      const handler = handlers[command] ?? (command.startsWith('plugin:window|') ? () => null : undefined);
      if (!handler) throw new Error(`IPC mock does not implement command: ${command}`);
      // Real IPC returns fresh JSON objects each time; clone simulated mutable state through JSON to avoid shared UI references.
      // Long-running commands (uninstall scans) resolve later; clone their result once it arrives.
      const clone = (value: unknown) => (value === undefined ? value : (JSON.parse(JSON.stringify(value)) as unknown));
      const result = handler((payload ?? {}) as Args);
      return result instanceof Promise ? result.then(clone) : clone(result);
    },
    { shouldMockEvents: true }
  );
  // Tauri mockIPC simulates broadcast emit but not emit_to; single-window tests turn update requests into local events.
  const internals = (window as unknown as { __TAURI_INTERNALS__: { invoke: typeof invoke } })['__TAURI_INTERNALS__'];
  const mockInvoke = internals.invoke;
  internals.invoke = (command, args, invokeOptions) => {
    if (command === 'plugin:event|emit_to' && (args as Args)?.event === 'updates:requested') {
      return mockInvoke('plugin:event|emit', args, invokeOptions);
    }
    return mockInvoke(command, args, invokeOptions);
  };
  return state;
}
