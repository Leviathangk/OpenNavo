// Clean Up Mac for browser mode (pnpm dev:web) and component tests (cleanup proposal §05, §07).
// Mirrors the P0 backend: rebuildable caches, logs, browser and developer caches, Homebrew's cleanup and leftovers of
// deleted apps. Running apps keep their caches, Full Disk Access gates sandboxed caches and Safari, protected rules hide
// items, and clean tasks report an outcome that undo / purge can act on.
// C2 adds Xcode device support files (the newest version per device stays unchecked), the clang module cache, npm logs,
// Google's updater downloads and Chromium caches inside Application Support; ?mock=cleanup-go-compiling holds the Go
// cache while `go` is compiling (a long-running `go run` no longer counts).
// C3 adds the temp folder, installers in Downloads and on the Desktop, build output of old projects (recent ones are
// listed, not offered), the Trash, and device backups with Xcode archives (personal, unchecked, moved to the Trash).
// C4 adds Docker's own reclaimable space as unchecked prune commands, with volumes only shown;
// ?mock=cleanup-docker-stopped reports Docker as installed but not running.
import { events } from '../bindings';
import type {
  AppError,
  CategoryReport,
  CleanupCategory,
  CleanupGroup,
  CleanupItem,
  CleanupReason,
  CleanupReport,
  CleanupRisk,
  CleanupSummary,
  CleanupVolume,
  DeleteMode,
  LocalApp,
  Permissions,
  ProtectInput,
  ProtectRule,
  RestoreResult,
  SkippedGroup,
  SkippedGroupReason,
  SkippedItem,
  SkippedLocation,
  Task,
  TaskTarget
} from '../bindings';
import { MOCK_HOME } from './uninstall';
import type { MockState, Step } from './runner';

const KB = 1e3;
const MB = 1e6;
const GB = 1e9;
const DAY = 86_400_000;
const REPORT_TTL = 10 * 60 * 1000;
const VOLUME_TOTAL = 494.4 * GB;

const expand = (path: string) => path.replace(/^~(?=\/|$)/, MOCK_HOME);
const delay = (ms: number) => new Promise(resolve => setTimeout(resolve, ms));
const appError = (code: string, message: string): AppError => ({ code, message, detail: null });

interface ItemSpec {
  path?: string;
  command?: string;
  size: number;
  daysAgo: number;
  reason: CleanupReason;
  /** Review items: preselected only when old enough (§05). */
  selected?: boolean;
  admin?: boolean;
  note?: string;
  /** Open files a tool cache keeps (Go 1.24+ runs `go run` binaries from its build cache). */
  keptOpen?: number;
  /** Hidden without Full Disk Access. */
  fda?: boolean;
  /** A single file (installers, things in the Trash) rather than a folder. */
  file?: boolean;
  /** Fixed by Rust for the category (installers and backups go to the Trash, project output is deleted). */
  deleteMode?: DeleteMode;
}
interface GroupSpec {
  id: string;
  title: string;
  subtitle?: string;
  appId?: string;
  bundleId?: string;
  /** Cask token or app id whose running state skips the group (caches in use). */
  owner?: string;
  /** Tool process that holds the group while it works (Go compiling), as Rust's tool rules do. */
  tool?: string;
  /** Listed with its size but never offered (a project changed within the last month). */
  skip?: SkippedGroupReason;
  items: ItemSpec[];
}
interface CategorySpec {
  key: CleanupCategory;
  risk: CleanupRisk;
  groups: GroupSpec[];
  protectedGroups?: { title: string; bundleId: string }[];
}

const CATEGORIES: CategorySpec[] = [
  {
    key: 'app_caches',
    risk: 'rebuildable',
    groups: [
      {
        id: 'spotify',
        title: 'Spotify',
        appId: 'local-spotify',
        bundleId: 'com.spotify.client',
        owner: 'local-spotify',
        items: [
          {
            path: '~/Library/Caches/com.spotify.client',
            size: 2.1 * GB,
            daysAgo: 0,
            reason: 'cache_dir'
          }
        ]
      },
      {
        id: 'wechat',
        title: 'WeChat',
        appId: 'cask-wechat',
        bundleId: 'com.tencent.xinWeChat',
        owner: 'cask-wechat',
        items: [
          {
            path: '~/Library/Containers/com.tencent.xinWeChat/Data/Library/Caches',
            size: 1.42 * GB,
            daysAgo: 0,
            reason: 'cache_dir',
            fda: true
          }
        ]
      },
      {
        id: 'photoshop',
        title: 'Adobe Photoshop 2026',
        appId: 'local-photoshop',
        bundleId: 'com.adobe.Photoshop',
        owner: 'local-photoshop',
        items: [
          {
            path: '~/Library/Caches/Adobe/Photoshop',
            size: 760 * MB,
            daysAgo: 9,
            reason: 'cache_dir'
          },
          {
            path: '~/Library/Application Support/Adobe/Common/Media Cache Files',
            size: 540 * MB,
            daysAgo: 9,
            reason: 'cache_dir'
          }
        ]
      },
      {
        id: 'notion',
        title: 'Notion',
        appId: 'local-notion',
        bundleId: 'notion.id',
        owner: 'local-notion',
        items: [{ path: '~/Library/Caches/notion.id', size: 420 * MB, daysAgo: 3, reason: 'cache_dir' }]
      },
      {
        id: 'vscode',
        title: 'Visual Studio Code',
        appId: 'cask-visual-studio-code',
        bundleId: 'com.microsoft.VSCode',
        owner: 'cask-visual-studio-code',
        items: [
          {
            path: '~/Library/Caches/com.microsoft.VSCode',
            size: 380 * MB,
            daysAgo: 1,
            reason: 'cache_dir'
          },
          {
            path: '~/Library/Application Support/Code/CachedData',
            size: 420 * MB,
            daysAgo: 2,
            reason: 'cache_dir'
          },
          {
            path: '~/Library/Application Support/Code/CachedExtensionVSIXs',
            size: 310 * MB,
            daysAgo: 16,
            reason: 'cache_dir'
          }
        ]
      },
      {
        id: 'zoom',
        title: 'Zoom',
        appId: 'local-zoom',
        bundleId: 'us.zoom.xos',
        owner: 'local-zoom',
        items: [{ path: '~/Library/Caches/us.zoom.xos', size: 210 * MB, daysAgo: 6, reason: 'cache_dir' }]
      },
      {
        id: 'appstore',
        title: 'App Store',
        bundleId: 'com.apple.appstore',
        items: [
          {
            path: '~/Library/Caches/com.apple.appstore',
            size: 248 * MB,
            daysAgo: 5,
            reason: 'cache_dir'
          }
        ]
      },
      {
        id: 'slack',
        title: 'Slack',
        bundleId: 'com.tinyspeck.slackmacgap',
        owner: 'slack',
        items: [
          {
            path: '~/Library/Containers/com.tinyspeck.slackmacgap/Data/Library/Caches',
            size: 920 * MB,
            daysAgo: 0,
            reason: 'cache_dir',
            fda: true
          }
        ]
      }
    ],
    protectedGroups: [
      { title: '1Password', bundleId: 'com.1password.1password' },
      { title: 'ClashX', bundleId: 'com.west2online.ClashX' }
    ]
  },
  {
    key: 'logs',
    risk: 'rebuildable',
    groups: [
      {
        id: 'jetbrains',
        title: 'JetBrains',
        items: [{ path: '~/Library/Logs/JetBrains', size: 126 * MB, daysAgo: 9, reason: 'log_file' }]
      },
      {
        id: 'npm-logs',
        title: 'npm',
        items: [{ path: '~/.npm/_logs', size: 38 * MB, daysAgo: 1, reason: 'log_file' }]
      },
      {
        id: 'adobe',
        title: 'Adobe',
        items: [{ path: '~/Library/Logs/Adobe', size: 84 * MB, daysAgo: 21, reason: 'log_file' }]
      },
      {
        id: 'crash',
        title: 'DiagnosticReports',
        items: [
          {
            path: '~/Library/Logs/DiagnosticReports',
            size: 144 * MB,
            daysAgo: 8,
            reason: 'log_file'
          }
        ]
      }
    ]
  },
  {
    key: 'browser_caches',
    risk: 'rebuildable',
    groups: [
      {
        id: 'chrome',
        title: 'Google Chrome',
        appId: 'cask-google-chrome',
        bundleId: 'com.google.Chrome',
        owner: 'google-chrome',
        items: [
          {
            path: '~/Library/Caches/Google/Chrome/Default/Cache',
            size: 980 * MB,
            daysAgo: 0,
            reason: 'browser_cache'
          },
          {
            path: '~/Library/Caches/Google/Chrome/Default/Code Cache',
            size: 220 * MB,
            daysAgo: 0,
            reason: 'browser_cache'
          }
        ]
      },
      {
        // Google's updater keeps downloaded updates on its own; Chrome running does not hold it.
        id: 'google-updater',
        title: 'GoogleUpdater',
        items: [
          {
            path: '~/Library/Application Support/Google/GoogleUpdater/crx_cache',
            size: 750 * MB,
            daysAgo: 6,
            reason: 'browser_cache'
          }
        ]
      },
      {
        id: 'safari',
        title: 'Safari',
        bundleId: 'com.apple.Safari',
        items: [
          {
            path: '~/Library/Caches/com.apple.Safari',
            size: 642 * MB,
            daysAgo: 0,
            reason: 'browser_cache',
            fda: true
          }
        ]
      }
    ]
  },
  {
    key: 'dev_caches',
    risk: 'rebuildable',
    groups: [
      {
        id: 'xcode',
        title: 'Xcode',
        appId: 'local-xcode',
        bundleId: 'com.apple.dt.Xcode',
        owner: 'local-xcode',
        items: [
          {
            path: '~/Library/Developer/Xcode/DerivedData',
            size: 5.2 * GB,
            daysAgo: 2,
            reason: 'tool_cache'
          },
          {
            path: '~/Library/Developer/CoreSimulator/Caches',
            size: 640 * MB,
            daysAgo: 12,
            reason: 'tool_cache'
          }
        ]
      },
      {
        id: 'devicesupport-iphone16-2',
        title: 'iPhone16,2',
        subtitle: 'iOS',
        owner: 'local-xcode',
        items: [
          {
            path: '~/Library/Developer/Xcode/iOS DeviceSupport/iPhone16,2 27.0 (24A437)',
            size: 6.9 * GB,
            daysAgo: 3,
            reason: 'device_support_latest',
            selected: false
          },
          {
            path: '~/Library/Developer/Xcode/iOS DeviceSupport/iPhone16,2 27.0 (24A5390f)',
            size: 6.8 * GB,
            daysAgo: 41,
            reason: 'device_support'
          },
          {
            path: '~/Library/Developer/Xcode/iOS DeviceSupport/iPhone16,2 26.5 (23F77)',
            size: 6 * GB,
            daysAgo: 118,
            reason: 'device_support'
          }
        ]
      },
      {
        id: 'devicesupport-ipad13-4',
        title: 'iPad13,4',
        subtitle: 'iOS',
        owner: 'local-xcode',
        items: [
          {
            path: '~/Library/Developer/Xcode/iOS DeviceSupport/iPad13,4 27.0 (24A5418b)',
            size: 7.2 * GB,
            daysAgo: 9,
            reason: 'device_support_latest',
            selected: false
          },
          {
            path: '~/Library/Developer/Xcode/iOS DeviceSupport/iPad13,4 26.5 (23F77)',
            size: 5.9 * GB,
            daysAgo: 150,
            reason: 'device_support'
          }
        ]
      },
      {
        id: 'gradle',
        title: 'Gradle',
        items: [
          {
            path: '~/.gradle/caches',
            size: 4.2 * GB,
            daysAgo: 6,
            reason: 'tool_download_cache',
            selected: false
          }
        ]
      },
      {
        id: 'go-modules',
        title: 'Go modules',
        items: [{ path: '~/go/pkg/mod', size: 2.1 * GB, daysAgo: 3, reason: 'tool_download_cache', selected: false }]
      },
      {
        id: 'playwright',
        title: 'Playwright',
        items: [
          {
            path: '~/Library/Caches/ms-playwright',
            size: 1.6 * GB,
            daysAgo: 20,
            reason: 'tool_download_cache',
            selected: false
          }
        ]
      },
      {
        id: 'clang',
        title: 'Clang',
        items: [
          {
            path: '/private/var/folders/b3/mock/C/clang/ModuleCache',
            size: 1.2 * GB,
            daysAgo: 1,
            reason: 'tool_cache'
          }
        ]
      },
      {
        id: 'npm',
        title: 'npm',
        items: [{ path: '~/.npm/_cacache', size: 640 * MB, daysAgo: 1, reason: 'tool_cache' }]
      },
      {
        id: 'yarn',
        title: 'Yarn',
        items: [{ path: '~/Library/Caches/Yarn', size: 410 * MB, daysAgo: 12, reason: 'tool_cache' }]
      },
      {
        id: 'go',
        title: 'Go',
        tool: 'go',
        items: [{ path: '~/Library/Caches/go-build', size: 3.6 * GB, daysAgo: 4, reason: 'tool_cache', keptOpen: 3 }]
      },
      {
        id: 'pods',
        title: 'CocoaPods',
        items: [{ path: '~/Library/Caches/CocoaPods', size: 290 * MB, daysAgo: 40, reason: 'tool_cache' }]
      },
      {
        id: 'pip',
        title: 'pip',
        items: [{ path: '~/Library/Caches/pip', size: 180 * MB, daysAgo: 25, reason: 'tool_cache' }]
      }
    ]
  },
  {
    key: 'homebrew',
    risk: 'rebuildable',
    groups: [
      {
        id: 'brew',
        title: 'Homebrew',
        items: [{ command: 'brew cleanup', size: 0, daysAgo: 0, reason: 'brew_cleanup' }]
      }
    ]
  },
  {
    key: 'temp',
    risk: 'rebuildable',
    groups: [
      {
        id: 'temp',
        title: 'temp',
        items: [
          {
            path: '/private/var/folders/q7/mock/T/go-build3429764944',
            size: 193 * MB,
            daysAgo: 6,
            reason: 'temp_file'
          },
          {
            path: '/private/var/folders/q7/mock/T/XcodeDistPipeline.~~~P5AkwY',
            size: 125 * MB,
            daysAgo: 12,
            reason: 'temp_file'
          },
          {
            path: '/private/var/folders/q7/mock/T/vscode-typescript501',
            size: 18 * MB,
            daysAgo: 4,
            reason: 'temp_file'
          }
        ]
      }
    ]
  },
  {
    key: 'leftovers',
    risk: 'review',
    groups: [
      {
        id: 'teamviewer',
        title: 'TeamViewer',
        bundleId: 'com.teamviewer.TeamViewer',
        items: [
          {
            path: '~/Library/LaunchAgents/com.teamviewer.teamviewer.plist',
            size: 4 * KB,
            daysAgo: 210,
            reason: 'orphan_service',
            selected: true,
            note: 'com.teamviewer.teamviewer'
          },
          {
            path: '~/Library/Application Support/TeamViewer',
            size: 64 * MB,
            daysAgo: 210,
            reason: 'orphan',
            selected: true
          },
          {
            path: '~/Library/Preferences/com.teamviewer.TeamViewer.plist',
            size: 12 * KB,
            daysAgo: 210,
            reason: 'orphan',
            selected: true
          }
        ]
      },
      {
        id: 'sketch',
        title: 'Sketch',
        bundleId: 'com.bohemiancoding.sketch3',
        items: [
          {
            path: '~/Library/Application Support/com.bohemiancoding.sketch3',
            size: 1.38 * GB,
            daysAgo: 210,
            reason: 'orphan',
            selected: true
          },
          {
            path: '~/Library/Caches/com.bohemiancoding.sketch3',
            size: 22 * MB,
            daysAgo: 210,
            reason: 'orphan',
            selected: true
          }
        ]
      },
      {
        id: 'figma-agent',
        title: 'Figma Agent',
        bundleId: 'com.figma.agent',
        items: [
          {
            path: '~/Library/LaunchAgents/com.figma.agent.plist',
            size: 4 * KB,
            daysAgo: 64,
            reason: 'orphan_service',
            selected: true,
            note: 'com.figma.agent'
          },
          {
            path: '~/Library/Application Support/Figma',
            size: 310 * MB,
            daysAgo: 64,
            reason: 'orphan',
            selected: true
          }
        ]
      },
      {
        id: 'loom',
        title: 'Loom',
        bundleId: 'com.loom.desktop',
        items: [
          {
            path: '~/Library/Preferences/com.loom.desktop.plist',
            size: 16 * KB,
            daysAgo: 12,
            reason: 'orphan',
            selected: false
          }
        ]
      }
    ]
  },
  {
    key: 'installers',
    risk: 'review',
    groups: [
      {
        id: 'installers',
        title: 'installers',
        items: [
          {
            path: '~/Downloads/Docker-arm64-4.30.0.dmg',
            size: 1.73 * GB,
            daysAgo: 40,
            reason: 'installer_installed',
            selected: true,
            fda: true,
            file: true,
            deleteMode: 'trash'
          },
          {
            path: '~/Desktop/Setup-Tool-2.1.pkg',
            size: 86 * MB,
            daysAgo: 120,
            reason: 'installer',
            selected: false,
            fda: true,
            file: true,
            deleteMode: 'trash'
          }
        ]
      }
    ]
  },
  {
    key: 'project_artifacts',
    risk: 'review',
    groups: [
      {
        id: 'rust-cli',
        title: 'rust-cli',
        subtitle: '~/Developer/rust-cli',
        items: [
          {
            path: '~/Developer/rust-cli/target',
            size: 3.4 * GB,
            daysAgo: 64,
            reason: 'build_artifact',
            selected: true,
            deleteMode: 'delete'
          }
        ]
      },
      {
        id: 'old-shop',
        title: 'old-shop',
        subtitle: '~/code/old-shop',
        items: [
          {
            path: '~/code/old-shop/node_modules',
            size: 612 * MB,
            daysAgo: 95,
            reason: 'build_artifact',
            selected: true,
            deleteMode: 'delete'
          },
          {
            path: '~/code/old-shop/.next',
            size: 148 * MB,
            daysAgo: 95,
            reason: 'build_artifact',
            selected: true,
            deleteMode: 'delete'
          }
        ]
      },
      {
        id: 'website',
        title: 'website',
        skip: 'recent',
        items: [{ path: '~/code/website/node_modules', size: 1.58 * GB, daysAgo: 1, reason: 'build_artifact' }]
      }
    ]
  },
  {
    key: 'docker',
    risk: 'review',
    groups: [
      {
        id: 'docker',
        title: 'Docker',
        appId: 'cask-docker-desktop',
        bundleId: 'com.docker.docker',
        items: [
          {
            command: 'docker image prune --all --force',
            size: 87.33 * GB,
            daysAgo: 0,
            reason: 'docker_images',
            selected: false
          },
          {
            command: 'docker builder prune --all --force',
            size: 21.1 * GB,
            daysAgo: 0,
            reason: 'docker_build_cache',
            selected: false
          },
          {
            command: 'docker container prune --force',
            size: 300.1 * MB,
            daysAgo: 0,
            reason: 'docker_containers',
            selected: false
          }
        ]
      },
      {
        // Volumes can hold databases: Rust measures them and lists them as skipped, never as an item.
        id: 'docker-volumes',
        title: 'Docker',
        skip: 'volumes',
        items: [{ size: 24.95 * GB, daysAgo: 0, reason: 'docker_images' }]
      }
    ]
  },
  {
    key: 'trash',
    risk: 'review',
    groups: [
      {
        id: 'trash',
        title: 'trash',
        items: [
          {
            path: '~/.Trash/Screen Recording 2026-09-02.mov',
            size: 820 * MB,
            daysAgo: 30,
            reason: 'trash',
            selected: false,
            fda: true,
            file: true,
            deleteMode: 'delete'
          },
          {
            path: '~/.Trash/old-exports',
            size: 240 * MB,
            daysAgo: 45,
            reason: 'trash',
            selected: false,
            fda: true,
            deleteMode: 'delete'
          }
        ]
      }
    ]
  },
  {
    key: 'device_backups',
    risk: 'personal',
    groups: [
      {
        id: 'backup-iphone',
        title: 'iPhone 15 Pro',
        subtitle: 'iPhone16,1',
        items: [
          {
            path: '~/Library/Application Support/MobileSync/Backup/00008130-001A2B3C4D5E',
            size: 48.2 * GB,
            daysAgo: 210,
            reason: 'device_backup',
            selected: false,
            fda: true,
            deleteMode: 'trash'
          }
        ]
      },
      {
        id: 'archive-acme',
        title: 'Acme',
        subtitle: 'Xcode',
        appId: 'local-xcode',
        bundleId: 'com.apple.dt.Xcode',
        items: [
          {
            path: '~/Library/Developer/Xcode/Archives/2026-07-04/Acme 2026-7-4, 22.41.xcarchive',
            size: 223 * MB,
            daysAgo: 99,
            reason: 'xcode_archive',
            selected: false,
            deleteMode: 'trash'
          },
          {
            path: '~/Library/Developer/Xcode/Archives/2026-06-07/Acme 2026-6-7, 15.40.xcarchive',
            size: 221 * MB,
            daysAgo: 126,
            reason: 'xcode_archive',
            selected: false,
            deleteMode: 'trash'
          }
        ]
      }
    ]
  }
];

/** Display order of categories during the scan, with the location shown as progress. */
const SCAN_STEPS: { category: CleanupCategory; location: string }[] = CATEGORIES.flatMap(category =>
  category.groups.slice(0, 2).map(group => ({
    category: category.key,
    location: expand(group.items[0]?.path ?? group.items[0]?.command ?? '')
  }))
);

const tokenOf = (owner: string) => owner.replace(/^cask-/, '');

export interface CleanupMockOptions {
  state: MockState;
  tickMs: number;
  permissions: () => Permissions;
  /** Local apps (running flags) from the uninstall mock. */
  apps: () => LocalApp[];
  /** Extra running cask tokens from ?mock=running-apps. */
  running: () => ReadonlySet<string>;
  deleteMode: () => DeleteMode;
  /** ?mock= flags: cleanup-in-use skips a cache at execution time, cleanup-expired rejects execution,
   *  cleanup-go-compiling holds the Go build cache while `go` compiles, cleanup-docker-stopped has Docker not running. */
  flags: ReadonlySet<string>;
  enqueue: (op: Task['op'], target: TaskTarget | null, options: Task['options'], targetName: string | null) => Task;
}

/** Cleanup commands plus runner hooks (steps, completion results, undo / purge). */
export function createCleanupMock({
  state,
  tickMs,
  permissions,
  apps,
  running,
  deleteMode,
  flags,
  enqueue
}: CleanupMockOptions) {
  const reports = new Map<string, { report: CleanupReport; items: Map<string, CleanupItem> }>();
  const canceled = new Set<string>();
  const executions = new Map<string, { reportId: string; items: CleanupItem[] }>();
  const trashed = new Map<string, { paths: string[]; bytes: number; done?: boolean }>();
  const removed = new Set<string>();
  let rules: ProtectRule[] = [];
  let summary: CleanupSummary | null = null;
  let freeBytes = 76.2 * GB;
  /** Like Rust, the disk is read live for every summary. */
  const volume = (): CleanupVolume => ({
    name: 'Macintosh HD',
    totalBytes: VOLUME_TOTAL,
    freeBytes
  });
  let ruleSeq = 0;

  const isRunning = (owner?: string) => {
    if (!owner) return false;
    const app = apps().find(item => item.id === owner);
    return Boolean(app?.running) || running().has(tokenOf(owner));
  };
  /** The executable that holds a running app's caches, as Rust names it in SkippedGroup.processes. */
  const processName = (owner?: string) => apps().find(item => item.id === owner)?.name ?? owner ?? '';
  const isProtected = (path: string, bundleId?: string) =>
    rules.some(rule =>
      rule.kind === 'bundle_id' ? rule.value === bundleId : path === rule.value || path.startsWith(`${rule.value}/`)
    );

  function build(): CleanupReport {
    const fda = permissions().fullDiskAccess === 'granted';
    const now = Date.now();
    const reportId = crypto.randomUUID();
    const incomplete: SkippedLocation[] = fda
      ? []
      : [
          '~/Library/Containers',
          '~/Library/Caches/com.apple.Safari',
          '~/Downloads',
          '~/Desktop',
          '~/.Trash',
          '~/Library/Application Support/MobileSync/Backup'
        ].map(path => ({
          path: expand(path),
          reason: 'full_disk_access' as const
        }));
    const categories: CategoryReport[] = [];
    for (const spec of CATEGORIES) {
      const groups: CleanupGroup[] = [];
      const skipped: SkippedGroup[] = [];
      for (const group of spec.groups) {
        const visible = group.items.filter(item => {
          const path = expand(item.path ?? '');
          const key = path || item.command;
          return (fda || !item.fda) && !(key && removed.has(key)) && !isProtected(path, group.bundleId);
        });
        if (!visible.length) continue;
        if (spec.key === 'docker' && flags.has('cleanup-docker-stopped')) {
          if (!skipped.length)
            skipped.push({ title: 'Docker', appId: null, bytes: null, reason: 'tool_stopped', processes: [] });
          continue;
        }
        if (spec.key === 'homebrew' && !state.cacheBytes) continue;
        const bytes = visible.reduce((sum, item) => sum + item.size, 0);
        if (spec.risk === 'rebuildable' && isRunning(group.owner)) {
          skipped.push({
            title: group.title,
            appId: group.appId ?? null,
            bytes,
            reason: 'running',
            processes: [processName(group.owner)]
          });
          continue;
        }
        if (group.tool && flags.has(`cleanup-${group.tool}-compiling`)) {
          skipped.push({ title: group.title, appId: null, bytes, reason: 'busy', processes: [group.tool] });
          continue;
        }
        if (group.skip) {
          skipped.push({ title: group.title, appId: null, bytes, reason: group.skip, processes: [] });
          continue;
        }
        groups.push({
          id: `${spec.key}:${group.id}`,
          title: group.title,
          subtitle: group.subtitle ?? null,
          appId: group.appId ?? null,
          bundleId: group.bundleId ?? null,
          items: visible.map((item, index) => ({
            id: `${reportId}:${spec.key}:${group.id}:${index}`,
            path: item.path ? expand(item.path) : '',
            kind: item.command ? 'command' : item.file ? 'file' : 'dir',
            command: item.command ?? null,
            sizeBytes: item.command === 'brew cleanup' ? state.cacheBytes : item.size,
            modifiedAt: now - item.daysAgo * DAY,
            reason: item.reason,
            selected: item.selected ?? spec.risk === 'rebuildable',
            deleteMode: item.deleteMode ?? (spec.risk === 'rebuildable' || item.command ? 'delete' : deleteMode()),
            needsAdmin: Boolean(item.admin),
            note: item.note ?? null,
            keptOpenFiles: item.keptOpen ?? 0
          }))
        });
      }
      for (const group of spec.protectedGroups ?? [])
        skipped.push({ title: group.title, appId: null, bytes: null, reason: 'protected', processes: [] });
      if (!groups.length && !skipped.length) continue;
      categories.push({
        key: spec.key,
        risk: spec.risk,
        bytes: groups.reduce(
          (sum, group) => sum + group.items.reduce((total, item) => total + (item.sizeBytes ?? 0), 0),
          0
        ),
        // Without Full Disk Access the sizes of gated categories are lower bounds.
        complete: fda || !spec.groups.some(group => group.items.some(item => item.fda)),
        groups,
        skipped
      });
    }
    return {
      reportId,
      scannedAt: now,
      expiresAt: now + REPORT_TTL,
      volume: volume(),
      categories,
      incomplete
    };
  }

  function summarize(report: CleanupReport): CleanupSummary {
    const items = report.categories.flatMap(category =>
      category.groups.flatMap(group => group.items.map(item => ({ category, item })))
    );
    const bytes = (list: typeof items) => list.reduce((sum, entry) => sum + (entry.item.sizeBytes ?? 0), 0);
    return {
      scannedAt: report.scannedAt,
      volume: volume(),
      reclaimableBytes: bytes(items.filter(entry => entry.category.risk !== 'personal')),
      suggestedBytes: bytes(items.filter(entry => entry.item.selected)),
      activeBackgroundItems: items.filter(entry => entry.item.reason === 'orphan_service').length,
      categories: report.categories.map(category => ({
        key: category.key,
        bytes: category.bytes
      }))
    };
  }

  function publish(next: CleanupSummary) {
    summary = next;
    void events.cleanupChanged.emit(next);
  }

  function steps(task: Task): Step[] | undefined {
    const execution = executions.get(task.id);
    if (!execution || task.op !== 'clean') return undefined;
    const lines = execution.items.map(item =>
      item.kind === 'command'
        ? `==> ${item.command}`
        : `${item.deleteMode === 'trash' ? 'Moving to Trash' : 'Deleting'} ${item.path}`
    );
    return [
      {
        phase: 'cleaning',
        from: 0,
        to: 92,
        ticks: Math.max(3, Math.ceil(lines.length / 2)),
        lines
      },
      { phase: 'finishing', from: 92, to: 100, ticks: 1, lines: ['==> Recorded'] }
    ];
  }

  /** Attach the outcome; a flagged in-use cache is skipped at execution time like Rust's recheck. */
  function completed(task: Task) {
    const execution = executions.get(task.id);
    if (!execution) return;
    const skipped: SkippedItem[] = [];
    const done = execution.items.filter(item => {
      if (flags.has('cleanup-in-use') && item.path.endsWith('com.spotify.client')) {
        skipped.push({ itemId: item.id, path: item.path, reason: 'in_use' });
        return false;
      }
      return true;
    });
    for (const item of done) {
      if (item.command === 'brew cleanup') state.cacheBytes = 0;
      else removed.add(item.path || (item.command ?? ''));
    }
    const moved = done.filter(item => item.kind !== 'command' && item.deleteMode === 'trash');
    const deleted = done.filter(item => item.kind === 'command' || item.deleteMode === 'delete');
    const bytes = (list: CleanupItem[]) => list.reduce((sum, item) => sum + (item.sizeBytes ?? 0), 0);
    freeBytes += bytes(deleted);
    trashed.set(task.id, { paths: moved.map(item => item.path), bytes: bytes(moved) });
    task.outcome = {
      freedBytes: bytes(done),
      trashedBytes: bytes(moved),
      trashed: moved.length,
      deleted: deleted.length,
      skipped,
      restorable: moved.length > 0
    };
    const report = reports.get(execution.reportId)?.report;
    if (report) publish({ ...summarize(build()), scannedAt: report.scannedAt });
  }

  const handlers: Record<string, (args: Record<string, unknown>) => unknown> = {
    cleanup_scan: async args => {
      const requestId = args.requestId as string;
      const report = build();
      const bytesOf = (key: CleanupCategory) => report.categories.find(category => category.key === key)?.bytes ?? 0;
      // Whole-scan progress like Rust: steps done / all steps, bytes of the categories finished so far.
      let foundBytes = 0;
      for (const [index, step] of SCAN_STEPS.entries()) {
        if (canceled.has(requestId)) throw appError('E_SCAN_CANCELED', 'scan_canceled');
        const previous = SCAN_STEPS[index - 1];
        if (previous && previous.category !== step.category) foundBytes += bytesOf(previous.category);
        void events.cleanupScanProgress.emit({
          requestId,
          category: step.category,
          location: step.location,
          checked: index,
          total: SCAN_STEPS.length,
          foundBytes
        });
        await delay(tickMs);
      }
      if (canceled.has(requestId)) throw appError('E_SCAN_CANCELED', 'scan_canceled');
      reports.set(report.reportId, {
        report,
        items: new Map(report.categories.flatMap(c => c.groups.flatMap(g => g.items.map(i => [i.id, i] as const))))
      });
      publish(summarize(report));
      return report;
    },
    cleanup_cancel: args => {
      canceled.add(args.requestId as string);
      return null;
    },
    cleanup_execute: args => {
      const reportId = args.reportId as string;
      const entry = reports.get(reportId);
      if (!entry) throw appError('E_PLAN_EXPIRED', 'report_expired');
      if (entry.report.expiresAt <= Date.now() || flags.has('cleanup-expired'))
        throw appError('E_PLAN_EXPIRED', 'report_expired');
      const ids = args.itemIds as string[];
      const items = ids.map(id => entry.items.get(id));
      if (!ids.length || items.some(item => !item)) throw appError('E_INVALID_ARG', 'item_ids');
      const task = enqueue('clean', { kind: 'app', token: reportId }, { planId: reportId, itemIds: ids }, null);
      executions.set(task.id, { reportId, items: items as CleanupItem[] });
      return task;
    },
    cleanup_reveal: () => null,
    cleanup_summary: (): CleanupSummary => ({
      ...(summary ?? {
        scannedAt: null,
        volume: null,
        reclaimableBytes: 0,
        suggestedBytes: 0,
        activeBackgroundItems: 0,
        categories: []
      }),
      volume: volume()
    }),
    cleanup_protect: args => {
      const input = args.input as ProtectInput;
      let rule: ProtectRule;
      if ('reportId' in input && input.reportId) {
        const item = reports.get(input.reportId)?.items.get(input.itemId);
        if (!item) throw appError('E_INVALID_ARG', 'item_id');
        rule = {
          id: `rule-${++ruleSeq}`,
          kind: 'path',
          value: item.path,
          label: item.path,
          createdAt: Date.now()
        };
      } else {
        const manual = input as { kind: ProtectRule['kind']; value: string };
        rule = {
          id: `rule-${++ruleSeq}`,
          kind: manual.kind,
          value: manual.value,
          label: manual.value,
          createdAt: Date.now()
        };
      }
      rules = [...rules, rule];
      return rule;
    },
    cleanup_unprotect: args => {
      rules = rules.filter(rule => rule.id !== args.ruleId);
      return null;
    },
    cleanup_protected_list: () => rules
  };

  /** Undo / purge for clean tasks; undefined lets the uninstall mock handle other tasks. */
  function restore(taskId: string): RestoreResult | undefined {
    const entry = trashed.get(taskId);
    if (!entry) return undefined;
    if (entry.done) throw appError('E_RESTORE_UNAVAILABLE', 'already_handled');
    entry.done = true;
    for (const path of entry.paths) removed.delete(path);
    return { restored: entry.paths.length, missing: [] };
  }
  function purge(taskId: string): number | undefined {
    const entry = trashed.get(taskId);
    if (!entry) return undefined;
    if (entry.done) throw appError('E_RESTORE_UNAVAILABLE', 'already_handled');
    entry.done = true;
    freeBytes += entry.bytes;
    return entry.bytes;
  }

  return { handlers, hooks: { steps, completed }, restore, purge };
}
