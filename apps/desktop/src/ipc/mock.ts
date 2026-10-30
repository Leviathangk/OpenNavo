// Simulated IPC for browser mode (pnpm dev:web) and component tests (06 §16): implement all 06 §5.2 commands with fixtures and push staged task events.
// ?mock=no-brew simulates a Mac without Homebrew; ?mock=first-run starts with fresh settings (onboarding). See installMockIpc for other switches.
import type { invoke } from '@tauri-apps/api/core';
import { mockIPC, mockWindows } from '@tauri-apps/api/mocks';
import { DESKTOP_LOCALES, matchLocale } from '@opennavo/shared';
import type { DesktopLocale as Locale } from '@opennavo/shared';
import type {
  AppInfo,
  CatalogStatus,
  EnvInfo,
  HistoryCleared,
  HistoryQuery,
  Kind,
  LinkOpenTarget,
  ListQuery,
  Permissions,
  PrivacyPane,
  SearchQuery,
  Task,
  TaskTarget,
  UpdateSource,
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
import { createAppStoreMock } from './mock-data/appstore';
import { createUpdaterMock } from './mock-data/updater';
import { createSecurityMock } from './mock-data/security';
import { createFirewallMock } from './mock-data/firewall';
import { createCleanupMock } from './mock-data/cleanup';
import { createMonitorMock } from './mock-data/monitor';
import { createUninstallMock } from './mock-data/uninstall';
import { createMockScheduler } from './mock-data/scheduler';
import type { MockFailure, MockState } from './mock-data/runner';

const appInfo: AppInfo = { version: '0.0.0', os: 'macos', arch: 'aarch64' };

/** Homebrew installer script URLs (match Rust installer.rs: official uses GitHub; mirrors initially use USTC's copy). */
const OFFICIAL_INSTALLER = 'https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh';
const MIRROR_INSTALLER = 'https://mirrors.ustc.edu.cn/misc/brew-install.sh';

function ipcTaskTrigger(trigger: Task['trigger']): Task['trigger'] {
  if (trigger === 'deeplink' || trigger === 'mcp')
    throw { code: 'E_INVALID_ARG', message: 'external_trigger_requires_rust', detail: null };
  return trigger;
}

function defaultSettings(): AppSettings {
  return {
    onboardingCompleted: false,
    locale: 'en-US',
    localeMode: 'system',
    appearance: 'dark',
    launchAtLogin: false,
    keepInMenuBarOnClose: true,
    trayShowCount: true,
    autoCheck: true,
    autoCheckAppUpdates: true,
    appStoreRegion: 'auto',
    appStoreSearch: true,
    appStoreUpdates: true,
    updateSources: {},
    avConsent: false,
    avDbCheckHours: 4,
    avKeepResident: false,
    watchDownloads: true,
    startupAlerts: true,
    avExclusions: [],
    avSchedule: 'off',
    avScheduleTime: '03:00',
    avScheduleWeekday: 1,
    avScanBeforeInstall: true,
    pasteProtection: false,
    virusTotalEnabled: false,
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
    target && target.kind !== 'app' && target.kind !== 'mas'
      ? (findItem(target.kind, target.token)?.apps[0] ?? `${target.token}.app`)
      : '';
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
    (permissions.appManagement !== 'granted' || permissions.appManagementPending) &&
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

let stopPreviousMock: (() => void) | undefined;
let currentFirewall: ReturnType<typeof createFirewallMock> | undefined;
/** The running firewall simulation, so component tests can make a new connection ask. */
export const firewallMock = () => currentFirewall;

/** Stop this mock session before replacing it or tearing down its browser environment. */
export function disposeMockIpc() {
  stopPreviousMock?.();
  stopPreviousMock = undefined;
  currentFirewall = undefined;
}
import.meta.hot?.dispose(disposeMockIpc);

export function installMockIpc(
  options: {
    tickMs?: number;
    brew?: boolean;
    systemLocale?: Locale;
    persistSettings?: boolean;
  } = {}
) {
  disposeMockIpc();
  const scheduler = createMockScheduler();
  const later = (run: () => void, ms: number) => scheduler.later(run, ms);
  const params = new URLSearchParams(typeof location === 'undefined' ? '' : location.search);
  const systemLocale =
    options.systemLocale ??
    DESKTOP_LOCALES.find(code => code === params.get('mock-system-locale')) ??
    matchLocale(typeof navigator === 'undefined' ? [] : navigator.languages, DESKTOP_LOCALES);
  const persistSettings = options.persistSettings ?? import.meta.env.MODE !== 'test';
  const storageKey = 'ONV_DESKTOP_mock_settings';
  // fresh means new settings; as in Rust, old settings missing onboardingCompleted count as onboarded (06 §11).
  // load mirrors Rust settings::load: only a Simplified Chinese Mac keeps Welcome pending; later writes keep their value.
  const normalizeSettings = (input: Partial<AppSettings>, fresh = false, load = false): AppSettings => {
    const localeMode =
      input.localeMode ?? (input.locale === undefined || input.locale === systemLocale ? 'system' : 'manual');
    return {
      ...defaultSettings(),
      ...input,
      onboardingCompleted: (load && systemLocale !== 'zh-CN') || (input.onboardingCompleted ?? !fresh),
      localeMode,
      locale: localeMode === 'system' ? systemLocale : (input.locale ?? 'en-US')
    };
  };
  // Comma-separated ?mock= switches: no-brew, first-run (fresh settings), slow-official / official-down (slow/failed official probe), slow-mirrors (mirrors slower than official),
  // brew-install-fail-download / brew-install-fail-sudo (Homebrew install failures), no-app-management (permission denied for app update/uninstall),
  // full_disk_access / no-permission-check / permission-relaunch / permission-relaunch-ineffective (privacy, below), restore, update.
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
  // Privacy permissions (06 §12.3, §12.6, matching Rust preflight): both are on by default, so the permission gate is open.
  // no-brew / first-run / no-app-management start without App Management; no-brew / first-run / no-full-disk-access
  // without Full Disk Access (full_disk_access forces it on); app-management-switch-off keeps working Full Disk Access with
  // the App Management switch off.
  // no-permission-check simulates unavailable preflight (unknown state); permission-relaunch follows real macOS, where a
  // permission turned on while OpenNavo runs applies only after it reopens (always so for Full Disk Access).
  let permissions: Permissions = {
    appManagementSwitch:
      noBrew || flags.has('first-run') || flags.has('no-app-management') || flags.has('app-management-switch-off')
        ? 'denied'
        : 'granted',
    appManagement: noBrew || flags.has('first-run') || flags.has('no-app-management') ? 'denied' : 'granted',
    fullDiskAccess:
      flags.has('full_disk_access') || (!noBrew && !flags.has('first-run') && !flags.has('no-full-disk-access'))
        ? 'granted'
        : 'denied',
    relaunchRequired: false,
    relaunchIneffective: false,
    appManagementPending: false,
    fullDiskAccessPending: false
  };
  if (permissions.fullDiskAccess === 'granted') permissions.appManagement = 'granted';
  if (flags.has('app-management-switch-off')) {
    permissions.fullDiskAccess = 'granted';
    permissions.appManagement = 'granted';
  }
  if (flags.has('no-permission-check'))
    permissions = {
      appManagement: 'unknown',
      appManagementSwitch: 'unknown',
      fullDiskAccess: 'unknown',
      relaunchRequired: false,
      relaunchIneffective: false,
      appManagementPending: false,
      fullDiskAccessPending: false
    };
  if (flags.has('permission-relaunch-ineffective'))
    permissions = {
      ...permissions,
      fullDiskAccess: 'denied',
      relaunchRequired: true,
      relaunchIneffective: true,
      fullDiskAccessPending: true
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
      // Full Disk Access stays denied until the reopen; App Management turns on but stays blocked.
      setPermissions(
        pane === 'full_disk_access'
          ? { ...permissions, relaunchRequired: true, fullDiskAccessPending: true }
          : {
              ...permissions,
              appManagementSwitch: 'granted',
              appManagement: 'granted',
              relaunchRequired: true,
              relaunchIneffective: false,
              appManagementPending: true
            }
      );
      return;
    }
    if (pane === 'full_disk_access')
      setPermissions({
        ...permissions,
        fullDiskAccess: 'granted',
        appManagement: 'granted'
      });
    else if (pane === 'app_management')
      setPermissions({ ...permissions, appManagementSwitch: 'granted', appManagement: 'granted' });
  };
  const runningApps = new Set(
    flags.has('running-apps') || flags.has('quit-fails') ? ['ghostty', 'zed', 'google-chrome'] : []
  );
  const reopenApps = new Map<string, string>();
  // Created after the runner because it queues tasks; runner hooks only call it once tasks run.
  let uninstall: ReturnType<typeof createUninstallMock> | undefined;
  let cleanup: ReturnType<typeof createCleanupMock> | undefined;
  let updater: ReturnType<typeof createUpdaterMock> | undefined;
  let security: ReturnType<typeof createSecurityMock> | undefined;
  const runner = createRunner(state, options.tickMs, {
    steps: task => uninstall?.hooks.steps(task) ?? cleanup?.hooks.steps(task) ?? updater?.hooks.steps(task),
    completed: task => {
      uninstall?.hooks.completed(task);
      cleanup?.hooks.completed(task);
      updater?.hooks.completed(task);
    },
    scanStep: task => security?.scanStep(task),
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
    failure: task => security?.installFailure(task) ?? mockFailure(task, flags, settings.mirror.key, permissions)
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
  let settings = normalizeSettings({}, true, true);
  if (persist) {
    try {
      const saved: Partial<AppSettings> | null = JSON.parse(localStorage.getItem(storageKey) ?? 'null');
      if (saved) settings = normalizeSettings(saved, false, true);
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
  const monitor = createMonitorMock({
    tickMs: options.tickMs ?? 250,
    flags,
    settings: () => settings,
    // Rust remembers the last manual fan speeds itself; mirror that into the simulated settings.
    updateSettings: patch => {
      settings = normalizeSettings({ ...settings, ...patch });
    }
  });
  const appStore = createAppStoreMock({ flags });
  updater = createUpdaterMock({
    enabled: flags.has('update-sources'),
    masUpdates: appStore.updates,
    apps: () => uninstall?.apps() ?? [],
    masInstalled: appStore.markInstalled,
    sources: () => settings.updateSources,
    history: () => state.history,
    rollback: update => {
      const task = runner.enqueue(
        'rollback',
        update.target,
        { source: update.options.source, rollbackOf: update.id, quitRunning: true, reopen: true },
        'manual',
        update.targetName
      );
      // Rust reports the versions with its first step: from the updated one back to the kept one.
      const live = state.active.find(item => item.id === task.id);
      if (live) Object.assign(live, { fromVersion: update.toVersion, toVersion: update.fromVersion, stepCount: 2 });
      return task.id;
    }
  });
  const updates = updater;
  /** Download paths the simulated Downloads watch announced; download_act accepts only these. */
  const downloads = new Set<string>();
  const securityMock = createSecurityMock({
    flags,
    tickMs: options.tickMs ?? 250,
    settings: () => settings
  });
  security = securityMock;
  const firewall = createFirewallMock({ flags, tickMs: options.tickMs ?? 250 });
  currentFirewall = firewall;
  // Reinstalling the simulator must not let the previous session emit into the new stores.
  stopPreviousMock = () => {
    runner.stop();
    uninstall?.stop();
    cleanup?.stop();
    scheduler.stop();
    window.removeEventListener('storage', onStorage);
    monitor.stop();
    securityMock.stop();
    firewall.stop();
  };
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
    linksError: null,
    syncedAt: Date.now() - 5 * 60 * 1000,
    syncing: false
  };

  const handlers: Record<string, (args: Args) => unknown> = {
    ...uninstall.handlers,
    ...cleanupMock.handlers,
    ...monitor.handlers,
    ...appStore.handlers,
    ...securityMock.handlers,
    ...firewall.handlers,
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
    // Like Rust, only addresses registered on the link app open; anything else is E_LINK_URL.
    link_open: args => {
      const link = getCatalogItem('link', args.token as string)?.link;
      const target = args.target as LinkOpenTarget;
      const url = !link
        ? null
        : typeof target === 'object'
          ? link.github?.latest?.assets.find(asset => asset.name === target.asset)?.url
          : target === 'download'
            ? link.downloadUrl
            : target === 'github'
              ? link.github && (link.github.latest?.url ?? `https://github.com/${link.github.repo}/releases/latest`)
              : link.homepage;
      if (!url) throw { code: 'E_LINK_URL', message: 'unregistered_url', detail: null };
      // Rust watches Downloads after a Download (plan §11.2): the file lands, then its verdict follows.
      if (link && (typeof target === 'object' || target === 'download')) {
        const token = args.token as string;
        const file = typeof target === 'object' ? target.asset : `${token}.dmg`;
        const path = `/Users/mock/Downloads/${file}`;
        const tick = options.tickMs ?? 250;
        const mismatch = flags.has('download-mismatch');
        later(() => {
          downloads.add(path);
          void events.downloadDetected.emit({ token, path });
        }, tick * 2);
        later(
          () =>
            void events.downloadVerified.emit({
              path,
              sha256: 'a'.repeat(64),
              size: 48 * 1024 * 1024,
              format: file.endsWith('.zip') ? 'zip' : file.endsWith('.pkg') ? 'pkg' : 'dmg',
              signature: {
                status: 'developer_id',
                teamId: mismatch ? 'ZZZZZZZZZZ' : (link.teamId ?? 'ABCDE12345'),
                signer: mismatch ? 'Unknown Developer' : (getCatalogItem('link', token)?.name ?? token),
                notarized: !mismatch
              },
              teamMatches: link.teamId ? !mismatch : null,
              av: { clean: true, signature: null, scannedAt: Date.now() },
              quarantined: false
            }),
          tick * 6
        );
      }
      return null;
    },
    download_act: args => {
      if (!downloads.has(String(args.path))) throw { code: 'E_INVALID_ARG', message: 'unknown_download', detail: null };
      return null;
    },
    catalog_categories: () => listCategories(settings.locale),
    library_list: () => state.installed,
    library_refresh: () => state.installed,
    library_storage: () => ({
      ...storageFixture(state.installed),
      cacheBytes: state.cacheBytes
    }),
    ...updates.handlers,
    updates_list: () => updates.merge(runner.outdated()),
    // ?mock=updates-partial: GitHub and developer-site rules could not be read in the last check.
    updates_status: (): UpdatesStatus => ({
      checkedAt,
      nextCheckAt: nextCheckAt(settings),
      checking: false,
      failedSources: flags.has('updates-partial') ? ['github', 'vendor'] : [],
      failures: flags.has('updates-app-failures')
        ? [
            { source: 'sparkle', appId: 'keka', appName: 'Keka', reason: 'rateLimited' },
            { source: 'electron', appId: 'open-design', appName: 'Open Design', reason: 'invalidUrl' }
          ]
        : flags.has('updates-rules-failed')
          ? [{ source: null, appId: null, appName: null, reason: 'rulesUnavailable' }]
          : []
    }),
    // null checks every enabled source; [] only rereads the list (plan §11.4).
    updates_check: args => {
      if ((args.sources as UpdateSource[] | null)?.length !== 0) checkedAt = Date.now();
      return updates.merge(runner.outdated());
    },
    updates_ignore: args => {
      const target = args.target as TaskTarget;
      // Homebrew semantics only; other sources use update_ignore with a preference key.
      if (target.kind === 'app' || target.kind === 'mas')
        throw { code: 'E_INVALID_ARG', message: 'homebrew_only', detail: null };
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
        status: managed ? 'managed' : flags.has('install-blocked') ? 'blocked' : conflict ? 'conflict' : 'ready',
        // The real preflight reads the tap from the local catalog (third-party casks only).
        tap: item?.tap ?? null
      };
    },
    // Like Rust's running_apps::identity, which looks running copies up from installed casks only.
    apps_running: args => {
      const targets = args.targets as TaskTarget[];
      if (targets.some(target => target.kind !== 'cask'))
        throw { code: 'E_INVALID_ARG', message: 'cask_only', detail: null };
      return targets
        .filter(target => runningApps.has(target.token))
        .map(target => ({
          target,
          name: state.installed.find(item => item.token === target.token)?.name ?? target.token
        }));
    },
    task_enqueue: args => {
      // Match Rust IPC: preserve app-internal origins and reject external origins before any queue write.
      const trigger = ipcTaskTrigger(args.trigger as Task['trigger']);
      if ((args.target as TaskTarget | null)?.kind === 'link')
        throw { code: 'E_NOT_INSTALLABLE', message: 'link_app', detail: null };
      return runner.enqueue(
        args.op as Task['op'],
        (args.target as TaskTarget | null) ?? null,
        args.options as Task['options'],
        trigger
      );
    },
    task_enqueue_many: args => {
      const trigger = ipcTaskTrigger(args.trigger as Task['trigger']);
      return (args.targets as TaskTarget[]).map(target =>
        runner.enqueue(args.op as Task['op'], target, args.options as Task['options'], trigger)
      );
    },
    // The approve switch is intentionally compile-time dev-only: Vite removes this branch from release bundles.
    deeplink_install_confirm: () => {
      if (import.meta.env.DEV && flags.has('deeplink-approve')) return { tasks: [] };
      throw { code: 'E_CONFIRMATION_DENIED', message: 'confirmation_denied', detail: null };
    },
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
    // Like Rust: what can still be undone or rolled back stays.
    history_clear: (): HistoryCleared => {
      const keep = (task: Task) =>
        ((task.op === 'uninstall' || task.op === 'clean') && task.outcome?.restorable === true) ||
        updates.rollbackable(task);
      const kept = state.history.filter(keep);
      const cleared = state.history.length - kept.length;
      state.history = kept;
      return { cleared, kept: kept.length };
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
    app_cancel_quit: () => null,
    permissions_get: () => permissions,
    permission_guide_open: args => {
      const pane = args.pane as PrivacyPane;
      later(() => grant(pane), guideDelayMs);
      return null;
    },
    permission_settings_open: () => null,
    permission_guide_close: () => null,
    permission_guide_fit: () => null,
    permission_guide_info: () => ({ draggable: true, ownerName: null, ownerIcon: null }),
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
