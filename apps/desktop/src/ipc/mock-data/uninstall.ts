// Local apps and uninstall plans for browser mode (pnpm dev:web) and component tests (uninstall proposal §06–§07).
// Homebrew casks come from the installed fixture; the rest cover App Store, .pkg and drag-installed apps,
// shared files, admin-only items, an in-use skip and a protected self entry.
// Paths use the placeholder home /Users/you; Full Disk Access gates container scanning as in Rust.
import { events } from '../bindings';
import type {
  AppError,
  AppPlan,
  AppSource,
  DeleteMode,
  Footprint,
  ItemCategory,
  LocalApp,
  MatchReason,
  Permissions,
  PlanItem,
  PlanItemKind,
  SkippedItem,
  SkippedLocation,
  Task,
  TaskTarget,
  UninstallChoice,
  UninstallPlan
} from '../bindings';
import { findItem } from './fixtures';
import type { MockState, Step } from './runner';

const KB = 1e3;
const MB = 1e6;
const GB = 1e9;
const DAY = 86_400_000;
export const MOCK_HOME = '/Users/you';
const PLAN_TTL = 10 * 60 * 1000;

const LOCATIONS = [
  '/Applications',
  '~/Applications',
  '~/Library/Application Support',
  '~/Library/Containers',
  '~/Library/Group Containers',
  '~/Library/Caches',
  '~/Library/Preferences',
  '~/Library/Saved Application State',
  '~/Library/Logs',
  '~/Library/LaunchAgents',
  '/Library/Application Support',
  '/Library/LaunchDaemons',
  '/Library/PrivilegedHelperTools',
  '/usr/local/bin',
  '/private/var/db/receipts',
  'Homebrew zap'
].map(path => path.replace('~', MOCK_HOME));

interface ItemOptions {
  required?: boolean;
  admin?: boolean;
  shared?: string[];
  kind?: PlanItemKind;
  count?: number;
  note?: string;
  /** Simulated failure at execution time. */
  skip?: SkippedItem['reason'];
}
type ItemSpec = [ItemCategory, string, number, MatchReason, ItemOptions?];
/** Plan item plus the simulated execution failure, never sent over IPC. */
type MockItem = PlanItem & { skip?: SkippedItem['reason'] };

interface AppSpec {
  personalData?: string;
  items: ItemSpec[];
}

/** Curated plans keyed by bundle ID; other apps get a generic plan. */
const SPECS: Record<string, AppSpec> = {
  'com.tencent.xinWeChat': {
    personalData: 'chat_history',
    items: [
      ['app_data', '~/Library/Containers/com.tencent.xinWeChat', 8.12 * GB, 'zap_rule'],
      ['app_data', '~/Library/Group Containers/5A4RE8SF68.com.tencent.xinWeChat', 410 * MB, 'app_group'],
      ['app_data', '~/Library/Containers/com.tencent.xinWeChat.MiniProgram', 236 * MB, 'bundle_id', { skip: 'in_use' }],
      ['app_data', '~/Library/Application Scripts/5A4RE8SF68.com.tencent.xinWeChat', 12 * KB, 'zap_rule'],
      ['caches', '~/Library/Caches/com.tencent.xinWeChat', 296 * MB, 'bundle_id'],
      ['preferences', '~/Library/Preferences/com.tencent.xinWeChat.plist', 48 * KB, 'bundle_id', { kind: 'file' }],
      ['preferences', '~/Library/Saved Application State/com.tencent.xinWeChat.savedState', 1.1 * MB, 'bundle_id'],
      ['logs', '~/Library/Logs/DiagnosticReports/WeChat-*.ips', 840 * KB, 'name_exact', { kind: 'glob', count: 3 }],
      ['caches', '~/Library/Caches/com.tencent.bugly', 22 * MB, 'name_similar']
    ]
  },
  'com.docker.docker': {
    personalData: 'vm_disk',
    items: [
      [
        'background',
        '/Library/PrivilegedHelperTools/com.docker.vmnetd',
        2.4 * MB,
        'brew_uninstall',
        { required: true, admin: true }
      ],
      [
        'background',
        '/Library/LaunchDaemons/com.docker.vmnetd.plist',
        4 * KB,
        'brew_uninstall',
        { required: true, admin: true, kind: 'launchd', note: 'com.docker.vmnetd' }
      ],
      [
        'cli',
        '/usr/local/bin/docker',
        0,
        'symlink',
        {
          admin: true,
          kind: 'symlink',
          note: '→ /Applications/Docker.app/Contents/Resources/bin/docker'
        }
      ],
      ['app_data', '~/Library/Containers/com.docker.docker', 24.3 * GB, 'bundle_id'],
      ['app_data', '~/Library/Group Containers/group.com.docker', 1.2 * GB, 'app_group'],
      ['app_data', '~/Library/Application Support/Docker Desktop', 86 * MB, 'zap_rule'],
      ['app_data', '~/.docker', 140 * MB, 'zap_rule', { shared: ['OrbStack'] }],
      ['caches', '~/Library/Caches/com.docker.docker', 312 * MB, 'bundle_id'],
      ['preferences', '~/Library/Preferences/com.docker.docker.plist', 4 * KB, 'bundle_id', { kind: 'file' }],
      ['logs', '~/Library/Logs/Docker Desktop', 46 * MB, 'zap_rule']
    ]
  },
  'com.microsoft.Word': {
    items: [
      ['app_data', '~/Library/Containers/com.microsoft.Word', 610 * MB, 'bundle_id'],
      [
        'app_data',
        '~/Library/Group Containers/UBF8T346G9.Office',
        1.4 * GB,
        'app_group',
        { shared: ['Microsoft Excel'] }
      ],
      [
        'background',
        '/Library/LaunchDaemons/com.microsoft.office.licensingV2.helper.plist',
        4 * KB,
        'launchd',
        {
          admin: true,
          shared: ['Microsoft Excel'],
          kind: 'launchd',
          note: 'com.microsoft.office.licensingV2.helper'
        }
      ],
      [
        'plugins',
        '/Library/Application Support/Microsoft/MAU2.0/Microsoft AutoUpdate.app',
        96 * MB,
        'team_id',
        { admin: true, shared: ['Microsoft Excel'] }
      ],
      [
        'receipts',
        '/private/var/db/receipts/com.microsoft.package.Microsoft_Word.app.bom',
        1.8 * MB,
        'pkg_receipt',
        { admin: true, kind: 'file', note: 'com.microsoft.package.Microsoft_Word.app' }
      ]
    ]
  },
  'us.zoom.xos': {
    items: [
      ['app_data', '~/Library/Application Support/zoom.us', 210 * MB, 'zap_rule'],
      ['caches', '~/Library/Caches/us.zoom.xos', 64 * MB, 'bundle_id'],
      ['preferences', '~/Library/Preferences/us.zoom.xos.plist', 12 * KB, 'bundle_id', { kind: 'file' }],
      ['logs', '~/Library/Logs/zoom.us', 18 * MB, 'name_exact'],
      [
        'background',
        '/Library/LaunchDaemons/us.zoom.ZoomDaemon.plist',
        4 * KB,
        'launchd',
        { admin: true, kind: 'launchd', note: 'us.zoom.ZoomDaemon' }
      ],
      ['plugins', '/Library/Audio/Plug-Ins/HAL/ZoomAudioDevice.driver', 1.1 * MB, 'pkg_receipt', { admin: true }],
      [
        'receipts',
        '/private/var/db/receipts/us.zoom.pkg.videomeeting.bom',
        640 * KB,
        'pkg_receipt',
        { admin: true, kind: 'file', note: 'us.zoom.pkg.videomeeting' }
      ]
    ]
  },
  'com.apple.FinalCut': {
    personalData: 'media',
    items: [
      ['app_data', '~/Library/Containers/com.apple.FinalCut', 860 * MB, 'bundle_id'],
      ['app_data', '~/Library/Application Support/ProApps', 96 * MB, 'name_similar'],
      ['caches', '~/Library/Caches/com.apple.FinalCut', 140 * MB, 'bundle_id']
    ]
  },
  'com.logi.optionsplus': {
    items: [
      ['app_data', '~/Library/Application Support/LogiOptionsPlus', 120 * MB, 'zap_rule'],
      ['caches', '~/Library/Caches/com.logi.optionsplus', 38 * MB, 'bundle_id'],
      [
        'background',
        '/Library/LaunchAgents/com.logi.optionsplus.plist',
        4 * KB,
        'launchd',
        { admin: true, kind: 'launchd', note: 'com.logi.optionsplus' }
      ],
      [
        'receipts',
        '/private/var/db/receipts/com.logi.optionsplus.installer.bom',
        380 * KB,
        'pkg_receipt',
        { admin: true, kind: 'file' }
      ]
    ]
  },
  'com.adobe.Photoshop': {
    personalData: 'documents',
    items: [
      ['app_data', '~/Library/Application Support/Adobe/Adobe Photoshop 2026', 1.6 * GB, 'name_exact'],
      ['caches', '~/Library/Caches/Adobe/Adobe Photoshop 2026', 420 * MB, 'name_exact'],
      ['preferences', '~/Library/Preferences/Adobe Photoshop 2026 Settings', 18 * MB, 'name_exact'],
      ['logs', '~/Library/Logs/Adobe/Photoshop', 12 * MB, 'name_similar']
    ]
  }
};

interface ExtraApp {
  id: string;
  name: string;
  file: string;
  bundleId: string;
  source: AppSource;
  catalogToken?: string;
  vendor: string;
  teamId: string | null;
  version: string;
  size: number;
  lastUsedDays: number | null;
  installedDays: number;
  running?: boolean;
  uninstaller?: string;
  self?: boolean;
}

/** Apps outside Homebrew; tokens refer to the mock catalog where a cask exists (adoptable). */
const EXTRA_APPS: ExtraApp[] = [
  {
    id: 'local-word',
    name: 'Microsoft Word',
    file: 'Microsoft Word.app',
    bundleId: 'com.microsoft.Word',
    source: 'pkg',
    vendor: 'Microsoft Corporation',
    teamId: 'UBF8T346G9',
    version: '16.102',
    size: 2.3 * GB,
    lastUsedDays: 47,
    installedDays: 540
  },
  {
    id: 'local-excel',
    name: 'Microsoft Excel',
    file: 'Microsoft Excel.app',
    bundleId: 'com.microsoft.Excel',
    source: 'pkg',
    vendor: 'Microsoft Corporation',
    teamId: 'UBF8T346G9',
    version: '16.102',
    size: 2.1 * GB,
    lastUsedDays: 20,
    installedDays: 540
  },
  {
    id: 'local-xcode',
    name: 'Xcode',
    file: 'Xcode.app',
    bundleId: 'com.apple.dt.Xcode',
    source: 'app_store',
    vendor: 'Apple',
    teamId: null,
    version: '26.1',
    size: 11.8 * GB,
    lastUsedDays: 2,
    installedDays: 380
  },
  {
    id: 'local-final-cut',
    name: 'Final Cut Pro',
    file: 'Final Cut Pro.app',
    bundleId: 'com.apple.FinalCut',
    source: 'app_store',
    vendor: 'Apple',
    teamId: null,
    version: '11.2',
    size: 6.2 * GB,
    lastUsedDays: 124,
    installedDays: 600
  },
  {
    id: 'local-keynote',
    name: 'Keynote',
    file: 'Keynote.app',
    bundleId: 'com.apple.iWork.Keynote',
    source: 'app_store',
    vendor: 'Apple',
    teamId: null,
    version: '14.4',
    size: 610 * MB,
    lastUsedDays: 301,
    installedDays: 900
  },
  {
    id: 'local-notion',
    name: 'Notion',
    file: 'Notion.app',
    bundleId: 'notion.id',
    source: 'direct',
    catalogToken: 'notion',
    vendor: 'Notion Labs, Inc.',
    teamId: 'LBQJ96FQ8D',
    version: '4.21.0',
    size: 420 * MB,
    lastUsedDays: 3,
    installedDays: 480
  },
  {
    id: 'local-zoom',
    name: 'zoom.us',
    file: 'zoom.us.app',
    bundleId: 'us.zoom.xos',
    source: 'pkg',
    catalogToken: 'zoom',
    vendor: 'Zoom Communications, Inc.',
    teamId: 'BJ4HAAB9B3',
    version: '6.6.0',
    size: 402 * MB,
    lastUsedDays: 12,
    installedDays: 375
  },
  {
    id: 'local-spotify',
    name: 'Spotify',
    file: 'Spotify.app',
    bundleId: 'com.spotify.client',
    source: 'direct',
    catalogToken: 'spotify',
    vendor: 'Spotify AB',
    teamId: '2FNC3A47ZF',
    version: '1.2.74',
    size: 380 * MB,
    lastUsedDays: 96,
    installedDays: 860
  },
  {
    id: 'local-logi',
    name: 'Logi Options+',
    file: 'logioptionsplus.app',
    bundleId: 'com.logi.optionsplus',
    source: 'pkg',
    catalogToken: 'logi-options-plus',
    vendor: 'Logitech Inc.',
    teamId: 'QED4VVPZWA',
    version: '1.92',
    size: 640 * MB,
    lastUsedDays: 403,
    installedDays: 760
  },
  {
    id: 'local-photoshop',
    name: 'Adobe Photoshop 2026',
    file: 'Adobe Photoshop 2026/Adobe Photoshop 2026.app',
    bundleId: 'com.adobe.Photoshop',
    source: 'direct',
    vendor: 'Adobe Inc.',
    teamId: 'JQ525L2MZD',
    version: '27.1',
    size: 4.8 * GB,
    lastUsedDays: 212,
    installedDays: 350,
    uninstaller: '/Applications/Utilities/Adobe Creative Cloud/Uninstall Adobe Creative Cloud.app'
  },
  {
    id: 'local-opennavo',
    name: 'OpenNavo',
    file: 'OpenNavo.app',
    bundleId: 'com.opennavo.desktop',
    source: 'direct',
    vendor: 'OpenNavo',
    teamId: null,
    version: '0.0.0',
    size: 38 * MB,
    lastUsedDays: 0,
    installedDays: 9,
    running: true,
    self: true
  }
];

/** Bundle IDs for installed fixture casks (only used to pick curated plans). */
const CASK_BUNDLES: Record<string, string> = {
  'docker-desktop': 'com.docker.docker',
  'google-chrome': 'com.google.Chrome',
  'visual-studio-code': 'com.microsoft.VSCode',
  orbstack: 'dev.kdrag0n.MacVirt',
  raycast: 'com.raycast.macos',
  ghostty: 'com.mitchellh.ghostty',
  wechat: 'com.tencent.xinWeChat',
  obsidian: 'md.obsidian'
};
const RUNNING_CASKS = new Set(['docker-desktop', 'wechat', 'raycast']);
const LAST_USED_CASKS: Record<string, number> = {
  'docker-desktop': 1,
  wechat: 0,
  raycast: 0,
  ghostty: 0,
  'visual-studio-code': 0,
  obsidian: 5,
  orbstack: 9,
  'google-chrome': 0
};

const expand = (path: string) => path.replace(/^~(?=\/)/, MOCK_HOME);
const confidence = (reason: MatchReason): PlanItem['confidence'] =>
  reason === 'name_similar' ? 'low' : reason === 'team_id' || reason === 'name_exact' ? 'medium' : 'high';
const delay = (ms: number) => new Promise(resolve => setTimeout(resolve, ms));
const appError = (code: string, message: string): AppError => ({ code, message, detail: null });

export function localAppsFixture(state: MockState, now = Date.now()): LocalApp[] {
  const casks = state.installed
    .filter(item => item.kind === 'cask')
    .map((item): LocalApp => {
      const app = item.appPaths[0];
      return {
        id: `cask-${item.token}`,
        path: app ?? `/opt/homebrew/Caskroom/${item.token}`,
        name: item.name,
        bundleId: CASK_BUNDLES[item.token] ?? null,
        version: item.actualVersion ?? item.installedVersion,
        source: 'homebrew',
        updater: 'homebrew',
        homebrewCask: null,
        cask: item.fullToken ?? item.token,
        catalogToken: item.token,
        teamId: null,
        vendor: null,
        arch: 'universal',
        sizeBytes: item.sizeBytes,
        installedAt: item.installedAt,
        lastUsedAt: now - (LAST_USED_CASKS[item.token] ?? 30) * DAY,
        iconPath: null,
        running: RUNNING_CASKS.has(item.token),
        removable: true,
        protectedReason: null,
        uninstaller: null
      };
    });
  const extra = EXTRA_APPS.map((app): LocalApp => {
    const catalogToken = app.catalogToken && findItem('cask', app.catalogToken) ? app.catalogToken : null;
    return {
      id: app.id,
      path: `/Applications/${app.file}`,
      name: app.name,
      bundleId: app.bundleId,
      version: app.version,
      source: app.source,
      // Same rule as Rust: App Store copies update there; a cask match outside Homebrew can be handed to it.
      updater: app.self ? null : app.source === 'app_store' ? 'mas' : null,
      homebrewCask: app.self ? null : catalogToken,
      cask: null,
      catalogToken,
      teamId: app.teamId,
      vendor: app.vendor,
      arch: 'universal',
      sizeBytes: app.size,
      installedAt: now - app.installedDays * DAY,
      lastUsedAt: app.lastUsedDays === null ? null : now - app.lastUsedDays * DAY,
      iconPath: null,
      running: Boolean(app.running),
      removable: !app.self,
      protectedReason: app.self ? 'self' : null,
      uninstaller: app.uninstaller ?? null
    };
  });
  return [...casks, ...extra];
}

function planItems(app: LocalApp, fullDiskAccess: boolean): MockItem[] {
  const spec = SPECS[app.bundleId ?? ''];
  const admin = app.source === 'app_store' || app.source === 'pkg';
  const name = app.name.replace(/\s+\d{4}$/, '');
  const generic: ItemSpec[] = [
    [
      'app_data',
      `~/Library/Application Support/${name}`,
      180 * MB,
      app.cask || app.catalogToken ? 'zap_rule' : 'name_exact'
    ],
    ['caches', `~/Library/Caches/${app.bundleId ?? name}`, 64 * MB, 'bundle_id'],
    ['preferences', `~/Library/Preferences/${app.bundleId ?? name}.plist`, 16 * KB, 'bundle_id', { kind: 'file' }],
    ['preferences', `~/Library/Saved Application State/${app.bundleId ?? name}.savedState`, 610 * KB, 'bundle_id']
  ];
  const specs: ItemSpec[] = [
    ['app', app.path, app.sizeBytes ?? 0, 'app_bundle', { required: true, admin }],
    ...(spec?.items ?? generic)
  ];
  return specs
    .map(([category, path, size, reason, options = {}], index): MockItem => {
      const shared = options.shared ?? [];
      const level = confidence(reason);
      return {
        id: `${app.id}:${index}`,
        path: expand(path),
        category,
        kind: options.kind ?? 'dir',
        count: options.count ?? 1,
        sizeBytes: options.kind === 'symlink' ? 0 : size,
        reason,
        confidence: level,
        selected: Boolean(options.required) || (level !== 'low' && !shared.length),
        required: Boolean(options.required),
        needsAdmin: Boolean(options.admin),
        sharedWith: shared,
        note: options.note ?? null,
        skip: options.skip
      };
    })
    .filter(item => fullDiskAccess || !/\/Library\/(Containers|Group Containers)\//.test(item.path));
}

interface Execution {
  app: LocalApp;
  items: MockItem[];
  mode: DeleteMode;
  purged?: boolean;
  restored?: boolean;
}

export interface UninstallMockOptions {
  state: MockState;
  tickMs: number;
  permissions: () => Permissions;
  enqueue: (op: Task['op'], target: TaskTarget | null, options: Task['options'], targetName: string) => Task;
}

/** Commands for the uninstall feature plus runner hooks (steps, completion results). */
export function createUninstallMock({ state, tickMs, permissions, enqueue }: UninstallMockOptions) {
  let apps = localAppsFixture(state);
  let lastFootprintScan = 0;
  const plans = new Map<string, { plan: UninstallPlan; items: Map<string, MockItem> }>();
  const canceled = new Set<string>();
  const executions = new Map<string, Execution>();

  const changed = (reason: 'refresh' | 'task' | 'running' | 'footprints') =>
    void events.localAppsChanged.emit({ reason });
  const findApp = (id: string) => apps.find(app => app.id === id);

  async function scan(requestId: string) {
    for (let checked = 0; checked < LOCATIONS.length; checked += 4) {
      if (canceled.has(requestId)) throw appError('E_SCAN_CANCELED', 'scan_canceled');
      void events.uninstallScanProgress.emit({
        requestId,
        checked,
        total: LOCATIONS.length,
        location: LOCATIONS[checked] ?? ''
      });
      await delay(tickMs);
    }
    if (canceled.has(requestId)) throw appError('E_SCAN_CANCELED', 'scan_canceled');
    void events.uninstallScanProgress.emit({
      requestId,
      checked: LOCATIONS.length,
      total: LOCATIONS.length,
      location: ''
    });
  }

  function incomplete(): SkippedLocation[] {
    if (permissions().fullDiskAccess === 'granted') return [];
    return ['~/Library/Containers', '~/Library/Group Containers'].map(path => ({
      path: expand(path),
      reason: 'full_disk_access' as const
    }));
  }

  /** Steps for plan-based uninstalls: quit → app → related files → record, with one log line per path. */
  function steps(task: Task): Step[] | undefined {
    const execution = executions.get(task.id);
    if (!execution) return undefined;
    const { app, items, mode } = execution;
    const verb = mode === 'trash' ? 'Moving to Trash' : 'Deleting';
    const rest = items.filter(item => item.category !== 'app' && item.reason !== 'brew_uninstall');
    const lines = rest.map(item => (item.skip ? `Skipped ${item.path} (${item.skip})` : `${verb} ${item.path}`));
    return [
      ...(app.running && task.options.quitRunning
        ? [
            {
              phase: 'preparing' as const,
              from: 0,
              to: 10,
              ticks: 2,
              lines: [`==> Quitting ${app.bundleId ?? app.name}`]
            }
          ]
        : []),
      {
        phase: 'uninstalling',
        from: 10,
        to: 45,
        ticks: 4,
        lines: app.cask
          ? [`==> brew uninstall --cask ${app.cask}`, `==> Uninstalling Cask ${app.cask}`]
          : [`==> ${verb} ${app.path}`]
      },
      {
        phase: 'cleaning',
        from: 45,
        to: 95,
        ticks: Math.max(3, Math.ceil(rest.length / 2)),
        lines: ['==> Removing related files', ...lines]
      },
      { phase: 'finishing', from: 95, to: 100, ticks: 1, lines: ['==> Recorded'] }
    ];
  }

  /** Attach UninstallOutcome and drop the app from the local list. */
  function completed(task: Task) {
    const execution = executions.get(task.id);
    if (!execution) return;
    const done = execution.items.filter(item => !item.skip);
    const skipped: SkippedItem[] = execution.items.flatMap(item =>
      item.skip ? [{ itemId: item.id, path: item.path, reason: item.skip }] : []
    );
    const freed = done.reduce((sum, item) => sum + (item.sizeBytes ?? 0), 0);
    task.outcome = {
      freedBytes: freed,
      trashedBytes: execution.mode === 'trash' ? freed : 0,
      trashed: execution.mode === 'trash' ? done.length : 0,
      deleted: execution.mode === 'delete' ? done.length : 0,
      skipped,
      restorable: execution.mode === 'trash'
    };
    apps = apps.filter(app => app.id !== execution.app.id);
    changed('task');
  }

  const handlers: Record<string, (args: Record<string, unknown>) => unknown> = {
    local_app_footprints: () => ({
      progress: lastFootprintScan
        ? { total: apps.length, completed: apps.length, failed: 0, retrying: 0, running: false }
        : null,
      values: Object.fromEntries(apps.map(app => [app.id, handlers.local_app_footprint!({ id: app.id })])),
      errors: {}
    }),
    local_app_retry_footprint: args => {
      if (!findApp(args.id as string)) throw appError('E_NOT_FOUND', 'app_not_found');
      changed('footprints');
      return null;
    },
    local_apps_scan_footprints: args => {
      if (!args.force && lastFootprintScan && Date.now() - lastFootprintScan < 3_600_000) return false;
      lastFootprintScan = Date.now();
      return true;
    },
    local_apps_list: () => apps,
    local_apps_refresh: () => {
      changed('refresh');
      return apps;
    },
    local_app_open: () => null,
    local_app_reveal: () => null,
    local_app_footprint: (args): Footprint => {
      const app = findApp(args.id as string);
      if (!app) throw appError('E_NOT_FOUND', 'app_not_found');
      const items = planItems(app, permissions().fullDiskAccess === 'granted');
      const sum = (category: ItemCategory) =>
        items.filter(item => item.category === category).reduce((total, item) => total + (item.sizeBytes ?? 0), 0);
      return {
        appBytes: app.sizeBytes ?? 0,
        dataBytes: sum('app_data') + sum('preferences') + sum('logs'),
        cacheBytes: sum('caches'),
        complete: permissions().fullDiskAccess === 'granted'
      };
    },
    uninstall_plan: async args => {
      const requestId = args.requestId as string;
      const ids = args.appIds as string[];
      const targets = ids.map(id => findApp(id));
      if (targets.some(app => !app)) throw appError('E_NOT_FOUND', 'app_not_found');
      if (targets.some(app => app && !app.removable)) throw appError('E_PROTECTED', 'protected');
      await scan(requestId);
      const fullDiskAccess = permissions().fullDiskAccess === 'granted';
      const items = new Map<string, MockItem>();
      const now = Date.now();
      const plan: UninstallPlan = {
        planId: requestId,
        apps: (targets as LocalApp[]).map((app): AppPlan => {
          const planned = planItems(app, fullDiskAccess);
          planned.forEach(item => items.set(item.id, item));
          return {
            appId: app.id,
            name: app.name,
            source: app.source,
            running: app.running,
            removal: app.cask ? 'brew' : 'trash',
            brewCommand: app.cask ? `brew uninstall --cask ${app.cask}` : null,
            blockedReason: !app.cask && permissions().appManagement === 'denied' ? 'app_management' : null,
            uninstaller: app.uninstaller,
            personalData: SPECS[app.bundleId ?? '']?.personalData ?? null,
            // The IPC contract has no skip flag; strip the simulation marker.
            items: planned.map(({ skip: _skip, ...item }) => item)
          };
        }),
        incomplete: incomplete(),
        createdAt: now,
        expiresAt: now + PLAN_TTL
      };
      plans.set(requestId, { plan, items });
      return plan;
    },
    uninstall_plan_cancel: args => {
      canceled.add(args.requestId as string);
      return null;
    },
    uninstall_reveal: () => null,
    uninstall_execute: args => {
      const entry = plans.get(args.planId as string);
      if (!entry || entry.plan.expiresAt < Date.now()) throw appError('E_PLAN_EXPIRED', 'plan_expired');
      const mode = args.mode as DeleteMode;
      return (args.choices as UninstallChoice[]).map(choice => {
        const app = findApp(choice.appId);
        if (!app) throw appError('E_NOT_FOUND', 'app_not_found');
        if (!app.cask && permissions().appManagement === 'denied') throw appError('E_APP_MANAGEMENT', 'app_management');
        const chosen = new Set(choice.itemIds);
        const items = [...entry.items.values()].filter(
          item => item.id.startsWith(`${app.id}:`) && (item.required || chosen.has(item.id))
        );
        const target: TaskTarget = app.cask
          ? { kind: 'cask', token: app.catalogToken ?? app.cask }
          : { kind: 'app', token: app.id };
        const task = enqueue(
          'uninstall',
          target,
          {
            planId: entry.plan.planId,
            itemIds: choice.itemIds,
            deleteMode: mode,
            quitRunning: choice.quitRunning
          },
          app.name
        );
        executions.set(task.id, { app, items, mode });
        return task;
      });
    },
    uninstall_restore: args => {
      const execution = executions.get(args.taskId as string);
      if (!execution || execution.mode !== 'trash' || execution.purged || execution.restored)
        throw appError('E_RESTORE_UNAVAILABLE', 'restore_unavailable');
      execution.restored = true;
      // Homebrew's record is not restored: the app comes back as a drag-installed, adoptable app.
      const back = execution.app.cask
        ? {
            ...execution.app,
            source: 'direct' as const,
            cask: null
          }
        : execution.app;
      apps = [...apps, back];
      changed('task');
      return { restored: execution.items.filter(item => !item.skip).length, missing: [] };
    },
    uninstall_purge: args => {
      const execution = executions.get(args.taskId as string);
      if (!execution || execution.mode !== 'trash' || execution.purged || execution.restored)
        throw appError('E_RESTORE_UNAVAILABLE', 'restore_unavailable');
      execution.purged = true;
      return execution.items.filter(item => !item.skip).reduce((sum, item) => sum + (item.sizeBytes ?? 0), 0);
    }
  };

  return { handlers, hooks: { steps, completed }, apps: () => apps };
}
