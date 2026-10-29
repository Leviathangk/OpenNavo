// Simulated multi-source updates (store/security plan §7, §11.4) for browser mode and component tests. Homebrew rows
// come from the runner; this adds App Store rows (from the App Store mock's installed copies) and Sparkle fixtures that
// cover every state the Updates page shows: critical, beta channel, administrator password, running app, relaunch
// pending and informational. Tasks follow Rust's routing: App Store rows queue {kind:'mas', token: adamId}, the others
// {kind:'app', token: appId} with options.source. ?mock=update-sources turns those rows on; without it the list holds
// only Homebrew's rows, as before M2, so unrelated tests keep their counts.
import type { AppError, LocalApp, MasUpdate, OutdatedItem, Task, UpdateChannel, UpdateSource } from '../bindings';
import { finishedTask } from './fixtures';
import type { Step } from './runner';

const MB = 1024 * 1024;
const appError = (code: string, message: string, detail: string | null = null): AppError => ({ code, message, detail });

/** Fields a Homebrew row carries besides brew's own: no app ID, channel or notes of its own. */
export const HOMEBREW_ROW = {
  source: 'homebrew',
  appId: null,
  bundleId: null,
  channel: null,
  major: false,
  notes: null,
  integrity: null,
  requiresAdmin: false,
  running: false,
  relaunchPending: false,
  skippedVersion: null,
  criticalUpdate: false,
  informationalUrl: null,
  downloadHost: null,
  expectedTeamMatch: null
} as const satisfies Partial<OutdatedItem>;

const BASE = {
  autoUpdates: true,
  pinned: false,
  ignored: false,
  dependents: 0,
  skippedVersion: null
} as const;

/** Apps that update themselves through Sparkle appcasts; two match catalog casks, two are outside the catalog. */
const sparkleRows = (): OutdatedItem[] => [
  {
    ...BASE,
    source: 'sparkle',
    appId: 'local-iterm2',
    downloadHost: 'iterm2.com',
    expectedTeamMatch: true,
    bundleId: 'com.googlecode.iterm2',
    channel: 'stable',
    major: false,
    notes: {
      format: 'html',
      body: '<h1>iTerm2 3.6.0</h1><ul><li>Faster <b>tmux</b> integration</li><li>Fixes a crash when closing tabs</li></ul><img src="https://iterm2.example/beacon.gif">'
    },
    integrity: 'eddsa',
    requiresAdmin: false,
    running: true,
    relaunchPending: false,
    criticalUpdate: false,
    informationalUrl: null,
    kind: 'cask',
    token: 'iterm2',
    name: 'iTerm2',
    installedVersion: '3.5.14',
    currentVersion: '3.6.0',
    downloadSize: 28 * MB
  },
  {
    ...BASE,
    source: 'sparkle',
    appId: 'local-sequel-ace',
    downloadHost: 'sequel-ace.com',
    expectedTeamMatch: null,
    bundleId: 'com.sequel-ace.sequel-ace',
    channel: 'beta',
    major: true,
    notes: {
      format: 'markdown',
      body: '## 5.0 beta 2\n\n- **New:** search in query history\n- Fixes SSH tunnels'
    },
    integrity: 'eddsa',
    requiresAdmin: true,
    running: false,
    relaunchPending: false,
    criticalUpdate: true,
    informationalUrl: null,
    kind: 'cask',
    token: '',
    name: 'Sequel Ace',
    installedVersion: '4.2.1',
    currentVersion: '5.0b2',
    downloadSize: 41 * MB
  },
  {
    ...BASE,
    source: 'sparkle',
    appId: 'local-rectangle',
    downloadHost: 'github.com',
    expectedTeamMatch: true,
    bundleId: 'com.knollsoft.Rectangle',
    channel: 'stable',
    major: false,
    notes: null,
    integrity: 'eddsa',
    requiresAdmin: false,
    running: true,
    relaunchPending: true,
    criticalUpdate: false,
    informationalUrl: null,
    kind: 'cask',
    token: 'rectangle',
    name: 'Rectangle',
    installedVersion: '0.89',
    currentVersion: '0.90',
    downloadSize: null
  },
  {
    ...BASE,
    source: 'sparkle',
    appId: 'local-hand-mirror',
    downloadHost: null,
    expectedTeamMatch: null,
    bundleId: 'net.rafaelconde.Hand-Mirror',
    channel: 'stable',
    major: true,
    notes: { url: 'https://hand-mirror.example/whats-new' },
    integrity: null,
    requiresAdmin: false,
    running: false,
    relaunchPending: false,
    criticalUpdate: false,
    informationalUrl: 'https://hand-mirror.example/whats-new',
    kind: 'cask',
    token: '',
    name: 'Hand Mirror',
    installedVersion: '2.4',
    currentVersion: '3.0',
    downloadSize: null
  }
];

/** Rows from M4's server rules: a developer website (WeChat) and a GitHub project whose Team ID does not match. */
const ruleRows = (): OutdatedItem[] => [
  {
    ...BASE,
    autoUpdates: false,
    source: 'vendor',
    appId: 'local-wechat',
    bundleId: 'com.tencent.xinWeChat',
    channel: null,
    major: false,
    notes: null,
    integrity: 'team_id',
    requiresAdmin: false,
    running: false,
    relaunchPending: false,
    criticalUpdate: false,
    informationalUrl: null,
    downloadHost: 'dldir1.qq.com',
    expectedTeamMatch: true,
    kind: 'cask',
    token: '',
    name: 'WeChat',
    installedVersion: '4.1.2',
    currentVersion: '4.1.3',
    downloadSize: 230 * MB
  },
  {
    ...BASE,
    source: 'github',
    appId: 'local-keka',
    bundleId: 'com.aone.keka',
    channel: null,
    major: false,
    notes: { format: 'markdown', body: '- Faster RAR extraction' },
    integrity: 'digest',
    requiresAdmin: false,
    running: false,
    relaunchPending: false,
    criticalUpdate: false,
    informationalUrl: null,
    downloadHost: 'github.com',
    expectedTeamMatch: false,
    kind: 'cask',
    token: '',
    name: 'Keka',
    installedVersion: '1.4.7',
    currentVersion: '1.4.8',
    downloadSize: 12 * MB
  }
];

/** Same rule as Rust: a major update raises the first numeric part of the marketing version. */
const isMajor = (from: string, to: string) => {
  const head = (version: string) => Number.parseInt(version, 10);
  return Number.isFinite(head(from)) && Number.isFinite(head(to)) && head(to) > head(from);
};

export interface UpdaterMockOptions {
  /** ?mock=update-sources: add the App Store and Sparkle rows. */
  enabled: boolean;
  /** App Store updates of the installed copies (the App Store mock's mas_updates). */
  masUpdates: () => MasUpdate[];
  /** Local apps, to give App Store rows the app ID Rust would (LocalApp.id by bundle ID). */
  apps: () => readonly LocalApp[];
  /** The App Store mock records a finished install or update. */
  masInstalled: (adamId: number) => void;
  /** updateSources from settings: missing keys are enabled, except msupdate. */
  sources: () => Partial<Record<UpdateSource, boolean>>;
  /** Finished tasks, newest first (the runner's history). */
  history: () => Task[];
  /** Queue the rollback of a finished update; returns the task ID. */
  rollback: (update: Task) => string;
}

const NATIVE: readonly (UpdateSource | null | undefined)[] = ['sparkle', 'electron', 'github', 'vendor'];

export function createUpdaterMock({
  enabled: on,
  masUpdates,
  apps,
  masInstalled,
  sources,
  history,
  rollback
}: UpdaterMockOptions) {
  let sparkle = sparkleRows();
  const skipped = new Map<string, string>();
  const channels = new Map<string, UpdateChannel>();
  const ignored = new Set<string>();
  // Updates from these sources kept the version they replaced (plan §7, P2): Notion's Electron update three hours ago
  // can roll back. A rollback spends its update's kept copy.
  const spent = new Set<string>();
  if (on) {
    const finishedAt = Date.now() - 3 * 60 * 60 * 1000;
    const update = finishedTask({
      op: 'upgrade',
      state: 'succeeded',
      target: { kind: 'app', token: 'local-notion' },
      targetName: 'Notion',
      options: { source: 'electron' },
      fromVersion: '4.3.0',
      toVersion: '4.4.1',
      createdAt: finishedAt - 40_000,
      startedAt: finishedAt - 38_000
    });
    const list = history();
    const index = list.findIndex(task => (task.finishedAt ?? 0) < (update.finishedAt ?? 0));
    list.splice(index < 0 ? list.length : index, 0, update);
  }
  /** Like Rust: a succeeded update from those sources, still kept, and the newest change of its app. */
  function rollbackable(task: Task) {
    const app = task.target?.kind === 'app' ? task.target.token : null;
    return (
      app !== null &&
      task.op === 'upgrade' &&
      task.state === 'succeeded' &&
      NATIVE.includes(task.options.source) &&
      !spent.has(task.id) &&
      !history().some(
        other =>
          other.target?.kind === 'app' &&
          other.target.token === app &&
          other.state === 'succeeded' &&
          (other.op === 'upgrade' || other.op === 'rollback') &&
          (other.finishedAt ?? 0) > (task.finishedAt ?? 0)
      )
    );
  }

  const enabled = (source: UpdateSource) => sources()[source] ?? source !== 'msupdate';
  const appKey = (row: OutdatedItem) =>
    row.source === 'homebrew' ? `homebrew:${row.kind}:${row.token}` : `app:${row.appId ?? ''}`;

  function masRows(): OutdatedItem[] {
    return masUpdates().map(update => ({
      ...BASE,
      autoUpdates: false,
      source: 'mas',
      appId: apps().find(app => app.bundleId === update.bundleId)?.id ?? `mas-${update.adamId}`,
      bundleId: update.bundleId,
      channel: null,
      major: isMajor(update.installedVersion, update.storeVersion),
      notes: update.releaseNotes ? { format: 'text', body: update.releaseNotes } : null,
      integrity: null,
      requiresAdmin: true,
      running: false,
      relaunchPending: false,
      criticalUpdate: false,
      informationalUrl: null,
      downloadHost: null,
      expectedTeamMatch: null,
      kind: 'cask',
      token: String(update.adamId),
      name: update.name,
      installedVersion: update.installedVersion,
      currentVersion: update.storeVersion,
      downloadSize: update.sizeBytes
    }));
  }

  const sourceRows = () =>
    on
      ? [
          ...(enabled('mas') ? masRows() : []),
          ...(enabled('sparkle') ? sparkle : []),
          ...ruleRows().filter(row => enabled(row.source))
        ]
      : [];

  /** Preferences applied to any row: skipped versions left out, ignore state, skip and channel shown. */
  function withPrefs(list: OutdatedItem[]): OutdatedItem[] {
    return list.flatMap(row => {
      const key = appKey(row);
      const skip = skipped.get(key) ?? null;
      if (skip === row.currentVersion) return [];
      return [
        {
          ...row,
          ignored: ignored.has(key),
          skippedVersion: skip,
          channel: row.channel ? (channels.get(key) ?? row.channel) : null
        }
      ];
    });
  }

  /**
   * Homebrew rows for apps another source updates are dropped, as Rust picks one source per app; skipping or ignoring
   * that source's update never hands the app back to Homebrew.
   */
  function merge(homebrew: OutdatedItem[]): OutdatedItem[] {
    const claimed = new Set(sourceRows().flatMap(row => (row.token ? [row.token] : [])));
    const brew = homebrew.filter(row => !claimed.has(row.token)).map(row => ({ ...row, ...HOMEBREW_ROW }));
    return withPrefs([...brew, ...sourceRows()]);
  }

  const handlers: Record<string, (args: Record<string, unknown>) => unknown> = {
    update_prepare_reinstall: args => {
      const row = sourceRows().find(
        item => item.appId === args.appId && ['sparkle', 'electron', 'github', 'vendor'].includes(item.source)
      );
      if (!row || row.informationalUrl) throw appError('E_NOT_INSTALLABLE', 'native_reinstall_source');
      skipped.delete(appKey(row));
      row.relaunchPending = false;
      return row;
    },
    update_skip: args => {
      const key = String(args.appKey);
      const version = (args.version as string | null) ?? null;
      if (version) skipped.set(key, version);
      else skipped.delete(key);
      return null;
    },
    update_set_channel: args => {
      channels.set(String(args.appKey), args.channel as UpdateChannel);
      return null;
    },
    update_ignore: args => {
      const key = String(args.appKey);
      if (args.ignored) ignored.add(key);
      else ignored.delete(key);
      return null;
    },
    app_relaunch: args => {
      sparkle = sparkle.filter(row => !(row.appId === args.appId && row.relaunchPending));
      return null;
    },
    update_rollback_targets: args =>
      (args.historyIds as string[]).filter(id => {
        const task = history().find(item => item.id === id);
        return task !== undefined && rollbackable(task);
      }),
    update_rollback: args => {
      const task = history().find(item => item.id === args.historyId);
      if (!task || !rollbackable(task)) throw appError('E_ROLLBACK_UNAVAILABLE', 'no_backup');
      return rollback(task);
    }
  };

  /** App Store tasks report Rust's four steps; Sparkle updates download, then verify and swap the bundle while installing. */
  function steps(task: Task): Step[] | undefined {
    const target = task.target;
    if (target?.kind === 'mas')
      return [
        {
          phase: 'preparing',
          from: 0,
          to: 0,
          ticks: 2,
          lines: ['Requesting the purchase from the App Store']
        },
        {
          phase: 'downloading',
          from: 0,
          to: 100,
          ticks: 6,
          lines: ['Downloading from the App Store']
        },
        {
          phase: 'installing',
          from: 0,
          to: 100,
          ticks: 3,
          lines: ['Installing']
        },
        {
          phase: 'finishing',
          from: 100,
          to: 100,
          ticks: 1,
          lines: ['Registering the app']
        }
      ];
    if (target?.kind === 'app' && task.op === 'rollback')
      return [
        {
          phase: 'preparing',
          from: 0,
          to: 0,
          ticks: 2,
          lines: ['Asking the app to quit normally'],
          indeterminate: true
        },
        {
          phase: 'installing',
          from: 0,
          to: 0,
          ticks: 3,
          lines: ['Checking the kept version: signature, developer, notarization', 'Swapping the kept version back in'],
          indeterminate: true
        }
      ];
    if (target?.kind === 'app' && task.op === 'upgrade' && task.options.source)
      return [
        {
          phase: 'downloading',
          from: 0,
          to: 70,
          ticks: 5,
          lines: ['Downloading the update']
        },
        {
          phase: 'installing',
          from: 70,
          to: 100,
          ticks: 4,
          lines: ['Checking the EdDSA signature and notarization', 'Replacing the app (backup kept)']
        }
      ];
    return undefined;
  }

  function completed(task: Task) {
    const target = task.target;
    if (target?.kind === 'mas') {
      masInstalled(Number(target.token));
      return;
    }
    if (target?.kind === 'app' && task.op === 'rollback' && task.options.rollbackOf) {
      // Rust spends the update's kept copy and skips the version rolled back from.
      spent.add(task.options.rollbackOf);
      if (task.fromVersion) skipped.set(`app:${target.token}`, task.fromVersion);
      return;
    }
    if (target?.kind !== 'app' || task.op !== 'upgrade' || !task.options.source) return;
    // A running app keeps the old version until it is reopened; the row stays as relaunch pending.
    sparkle = sparkle.flatMap(row => {
      if (row.appId !== target.token) return [row];
      return row.running
        ? [
            {
              ...row,
              installedVersion: row.currentVersion,
              relaunchPending: true
            }
          ]
        : [];
    });
  }

  return { handlers, merge, hooks: { steps, completed }, rollbackable };
}
