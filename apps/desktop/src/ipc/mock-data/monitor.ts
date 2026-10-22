// Computer monitoring for browser mode (pnpm dev:web) and component tests (monitor proposal §11–§16).
// A seeded generator stands in for Rust's sampler: one sample per tick while any view is subscribed.
// ?mock= switches: desktop (no battery), fanless (no fans), cpu-busy, mem-pressure, disk-low, hot,
// fan-unsupported (an unknown chip: speeds only), fan-helper-ready (helper already approved), fan-helper-broken
// (installed but not answering), fan-unsigned (a development build that cannot register the helper),
// disk-failing (the internal drive reports a failing SMART status, M3).
import { events } from '../bindings';
import type {
  AppError,
  CpuBreakdown,
  DiskDevice,
  FanHelperStatus,
  FanReason,
  FanState,
  HealthIssue,
  MenuBarItemStatus,
  MonitorMenuBar,
  MonitorModule,
  MonitorSample,
  MonitorSnapshot,
  MonitorTrendPoint,
  MonitorView,
  ProcessInfo,
  SystemInfo
} from '../bindings';
import type { AppSettings } from '../client';

const GB = 1e9;
const MB = 1e6;
const KB = 1e3;
const DAY = 86_400_000;
const MODULES: MonitorModule[] = ['cpu', 'gpu', 'memory', 'disk', 'network', 'battery', 'sensors', 'fans'];
/** Fans have no menu bar item: the sensors item shows the temperature and its popover the fans. */
const MENU_BAR = MODULES.filter((module): module is keyof MonitorMenuBar => module !== 'fans');
const HISTORY = 60;

const appError = (code: string, message: string): AppError => ({ code, message, detail: null });

type IoRates = 'diskReadBytesPerSec' | 'diskWriteBytesPerSec' | 'netDownBytesPerSec' | 'netUpBytesPerSec';
/** Typical disk and network load per process (bytes per second), varied a little each tick. */
const IO: Record<string, [diskRead: number, diskWrite: number, netDown: number, netUp: number]> = {
  Xcode: [6 * MB, 2.4 * MB, 0, 0],
  'Google Chrome': [0.3 * MB, 0.6 * MB, 1.1 * MB, 90 * KB],
  mds_stores: [14 * MB, 0.4 * MB, 0, 0],
  'Docker Desktop': [1.2 * MB, 3.1 * MB, 40 * KB, 12 * KB],
  Dropbox: [0.8 * MB, 0.2 * MB, 2.6 * MB, 640 * KB],
  Spotify: [0.1 * MB, 0.05 * MB, 320 * KB, 8 * KB],
  Slack: [0.05 * MB, 0.1 * MB, 60 * KB, 20 * KB]
};
/** One row of the fixture: a process, the app it belongs to for the merged view, and whether it is OpenNavo itself. */
type MockProcess = Omit<ProcessInfo, 'iconPath' | IoRates | 'count' | 'isSelf'> & { group?: string; isSelf?: boolean };
const PROCESSES: MockProcess[] = [
  {
    pid: 912,
    name: 'Xcode',
    bundleId: 'com.apple.dt.Xcode',
    cpu: 0.214,
    memoryBytes: 4.8 * GB,
    kind: 'app',
    quittable: true
  },
  {
    pid: 1204,
    name: 'Google Chrome',
    bundleId: 'com.google.Chrome',
    cpu: 0.093,
    memoryBytes: 3.6 * GB,
    kind: 'app',
    quittable: true,
    group: 'Google Chrome'
  },
  {
    pid: 1211,
    name: 'Google Chrome Helper (Renderer)',
    bundleId: null,
    cpu: 0.052,
    memoryBytes: 1.4 * GB,
    kind: 'background',
    quittable: false,
    group: 'Google Chrome'
  },
  {
    pid: 1212,
    name: 'Google Chrome Helper (GPU)',
    bundleId: null,
    cpu: 0.024,
    memoryBytes: 0.6 * GB,
    kind: 'background',
    quittable: false,
    group: 'Google Chrome'
  },
  {
    pid: 0,
    name: 'kernel_task',
    bundleId: null,
    cpu: 0.061,
    memoryBytes: 1.1 * GB,
    kind: 'system',
    quittable: false
  },
  {
    pid: 401,
    name: 'WindowServer',
    bundleId: null,
    cpu: 0.048,
    memoryBytes: 820 * MB,
    kind: 'system',
    quittable: false
  },
  {
    pid: 2210,
    name: 'Docker Desktop',
    bundleId: 'com.docker.docker',
    cpu: 0.037,
    memoryBytes: 2.9 * GB,
    kind: 'app',
    quittable: true
  },
  {
    pid: 1777,
    name: 'Slack',
    bundleId: 'com.tinyspeck.slackmacgap',
    cpu: 0.021,
    memoryBytes: 1.2 * GB,
    kind: 'app',
    quittable: true
  },
  {
    pid: 3110,
    name: 'Spotify',
    bundleId: 'com.spotify.client',
    cpu: 0.015,
    memoryBytes: 640 * MB,
    kind: 'app',
    quittable: true
  },
  {
    pid: 520,
    name: 'mds_stores',
    bundleId: null,
    cpu: 0.012,
    memoryBytes: 210 * MB,
    kind: 'system',
    quittable: false
  },
  {
    pid: 688,
    name: 'Dropbox',
    bundleId: 'com.getdropbox.dropbox',
    cpu: 0.004,
    memoryBytes: 2.2 * GB,
    kind: 'background',
    quittable: false
  },
  {
    pid: 4021,
    name: 'OpenNavo',
    bundleId: 'org.opennavo.desktop',
    cpu: 0.008,
    memoryBytes: 180 * MB,
    kind: 'app',
    quittable: false,
    isSelf: true
  }
];
// Rust aggregates thermal zones under a fixed vocabulary and stable keys (monitor M2); the frontend localizes the names.
const SENSORS: [key: string, name: string, group: 'cpu' | 'gpu' | 'soc' | 'battery' | 'ssd', offset: number][] = [
  ['cpu-performance', 'CPU performance cores', 'cpu', 0],
  ['cpu-efficiency', 'CPU efficiency cores', 'cpu', -7],
  ['gpu', 'GPU', 'gpu', -5],
  ['soc', 'SoC', 'soc', -3],
  ['ssd', 'SSD', 'ssd', -15],
  ['battery', 'Battery', 'battery', -21]
];
const FAN_LIMITS = [
  { id: 0, name: 'Left fan', minRpm: 1200, maxRpm: 5800 },
  { id: 1, name: 'Right fan', minRpm: 1200, maxRpm: 5800 }
];

/** A new mock replaces the previous one's timer, so a test never receives samples from an earlier fixture. */
/** SMART per physical drive (M3): the internal SSD reports wear and totals; the external one doesn't support SMART. */
const DEVICES: DiskDevice[] = [
  {
    id: 'disk0',
    name: 'APPLE SSD AP0512Z',
    internal: true,
    smart: 'verified',
    percentUsed: 0.03,
    temperatureC: 38,
    powerOnHours: 2140,
    dataWrittenBytes: 48.2e12,
    mountPoints: ['/']
  },
  {
    id: 'disk4',
    name: 'Samsung PSSD T7 Shield',
    internal: false,
    smart: 'unsupported',
    percentUsed: null,
    temperatureC: null,
    powerOnHours: null,
    dataWrittenBytes: null,
    mountPoints: ['/Volumes/T7 Shield']
  }
];

let stopPrevious: (() => void) | null = null;

const sumRates = (items: ProcessInfo[], pick: (entry: ProcessInfo) => number | null) =>
  items.some(entry => pick(entry) != null) ? items.reduce((sum, entry) => sum + (pick(entry) ?? 0), 0) : null;

/** Rust's merged view: processes of one app (or the same executable) as a single entry led by the main process. */
function mergeProcesses(listed: ProcessInfo[], table: MockProcess[]): ProcessInfo[] {
  const groups = new Map<string, ProcessInfo[]>();
  listed.forEach((entry, index) => {
    const key = table[index]?.group ?? entry.name;
    groups.set(key, [...(groups.get(key) ?? []), entry]);
  });
  return [...groups.entries()].map(([name, members]) => {
    const main = members.find(entry => entry.bundleId != null) ?? members[0]!;
    if (members.length === 1) return main;
    return {
      ...main,
      name: main.bundleId != null ? main.name : name,
      count: members.length,
      cpu: members.reduce((sum, entry) => sum + entry.cpu, 0),
      memoryBytes: members.reduce((sum, entry) => sum + entry.memoryBytes, 0),
      diskReadBytesPerSec: sumRates(members, entry => entry.diskReadBytesPerSec),
      diskWriteBytesPerSec: sumRates(members, entry => entry.diskWriteBytesPerSec),
      netDownBytesPerSec: sumRates(members, entry => entry.netDownBytesPerSec),
      netUpBytesPerSec: sumRates(members, entry => entry.netUpBytesPerSec),
      isSelf: members.some(entry => entry.isSelf)
    };
  });
}

/** Where the CPU went: the listed processes, children that ended before they could be listed, and the rest. */
function breakdown(listed: ProcessInfo[], usage: number): CpuBreakdown {
  const readable = Math.min(usage, listed.reduce((sum, entry) => sum + entry.cpu, 0) + usage * 0.12);
  const finished = Math.min(usage - readable, usage * 0.1);
  return {
    usage,
    readable,
    // The real list has a long tail below the top rows.
    activeCount: listed.filter(entry => entry.cpu >= 0.001).length + 9,
    finished,
    finishedFrom:
      finished > 0.01
        ? [
            { pid: 3201, name: 'node', cpu: finished * 0.7 },
            { pid: 3188, name: 'zsh', cpu: finished * 0.3 }
          ]
        : [],
    other: Math.max(0, usage - readable - finished)
  };
}

export interface MonitorMockOptions {
  tickMs: number;
  flags: ReadonlySet<string>;
  settings: () => AppSettings;
  updateSettings: (patch: Partial<AppSettings>) => void;
}

export function createMonitorMock({ tickMs, flags, settings, updateSettings }: MonitorMockOptions) {
  stopPrevious?.();
  let seed = 11;
  const rnd = () => {
    seed = (seed * 16807) % 2147483647;
    return (seed - 1) / 2147483646;
  };
  const clamp = (value: number, low: number, high: number) => Math.min(high, Math.max(low, value));
  const hasBattery = !flags.has('desktop');
  const hasFans = !flags.has('fanless');
  const available = MODULES.filter(module => (module === 'battery' ? hasBattery : module === 'fans' ? hasFans : true));
  const system: SystemInfo = {
    modelName: 'MacBook Pro',
    modelId: 'Mac16,7',
    chip: 'Apple M4 Pro',
    performanceCores: 10,
    efficiencyCores: 4,
    logicalCores: 14,
    gpuCores: 20,
    memoryBytes: 48 * GB,
    osVersion: '26.0',
    bootedAt: Date.now() - 6 * DAY - 4 * 3_600_000
  };
  const views = new Map<MonitorView, MonitorModule[]>();
  const history: MonitorSample[] = [];
  let processes = [...PROCESSES];
  let cpu = flags.has('cpu-busy') ? 0.9 : 0.18;
  let temp = flags.has('hot') ? 96 : 52;
  const fanChip = hasFans && !flags.has('fan-unsupported');
  let helper: FanHelperStatus = {
    status: !fanChip
      ? 'unsupported'
      : flags.has('fan-helper-ready')
        ? 'enabled'
        : flags.has('fan-helper-broken')
          ? 'error'
          : 'not_installed',
    // As in Rust: no chip family is verified, and that no longer blocks manual control.
    support: fanChip ? 'unverified' : 'unsupported',
    // Same gate as Rust: chip support only, independent of whether the helper is installed.
    manualAllowed: fanChip
  };
  let fans: FanState[] = FAN_LIMITS.map(limit => ({
    ...limit,
    rpm: 1310,
    targetRpm: null,
    mode: 'auto'
  }));
  let timer: ReturnType<typeof setInterval> | null = null;
  const sampleMs = Math.max(4, tickMs * 4);

  function sample(): MonitorSample {
    cpu = clamp(cpu + (rnd() - 0.5) * 0.09, flags.has('cpu-busy') ? 0.86 : 0.05, 0.97);
    temp = clamp(temp + (rnd() - 0.5) * 2, flags.has('hot') ? 95 : 44, flags.has('hot') ? 99 : 82);
    fans = fans.map(fan => {
      const goal =
        fan.mode === 'manual' && fan.targetRpm != null
          ? fan.targetRpm
          : clamp(1200 + Math.max(0, temp - 55) * 130, fan.minRpm, fan.maxRpm);
      return {
        ...fan,
        rpm: Math.round(clamp(fan.rpm + (goal - fan.rpm) * 0.35 + (rnd() - 0.5) * 24, fan.minRpm, fan.maxRpm))
      };
    });
    const totalMemory = system.memoryBytes;
    const used = (flags.has('mem-pressure') ? 0.94 : 0.62) * totalMemory;
    const free = flags.has('disk-low') ? 9.8 * GB : 76.2 * GB;
    const issues: HealthIssue[] = [];
    const report = (entry: Omit<HealthIssue, 'subject'>, subject: string | null = null) =>
      issues.push({ ...entry, subject });
    if (flags.has('cpu-busy')) report({ key: 'cpu_busy', level: 'attention', value: cpu, topPid: 912 });
    if (flags.has('mem-pressure'))
      report({ key: 'memory_pressure', level: 'attention', value: used / totalMemory, topPid: 912 });
    if (flags.has('disk-low'))
      report({ key: 'disk_low', level: free < 5 * GB ? 'critical' : 'attention', value: free, topPid: null });
    if (flags.has('hot')) report({ key: 'temperature', level: 'attention', value: temp, topPid: 912 });
    if (flags.has('disk-failing'))
      report({ key: 'disk_smart', level: 'critical', value: 0, topPid: null }, DEVICES[0]!.name);
    // Rust reads power, per-process I/O and SMART details only for modules a visible view shows (M3).
    const wanted = new Set([...views.values()].flat());
    const withProcesses = [...views.keys()].length > 0;
    const gpu = clamp(0.18 + (rnd() - 0.5) * 0.1, 0.02, 0.95);
    // ±15%: enough to look alive, never enough to reorder the top processes between ticks.
    const jitter = (value: number) => (value ? value * (0.85 + rnd() * 0.3) : 0);
    // The table gives each process's weight; the list never claims more than three quarters of what the CPU is doing.
    const weight = processes.reduce((sum, entry) => sum + entry.cpu, 0);
    const scale = weight > 0 ? Math.min(1, (0.75 * cpu) / weight) : 0;
    const listed: ProcessInfo[] | null =
      withProcesses || wanted.size
        ? processes.map(({ group: _group, isSelf, ...entry }) => {
            const [read, write, down, up] = IO[entry.name] ?? [0, 0, 0, 0];
            return {
              ...entry,
              cpu: entry.cpu * scale,
              count: 1,
              isSelf: isSelf ?? false,
              iconPath: null,
              diskReadBytesPerSec: wanted.has('disk') ? jitter(read) : null,
              diskWriteBytesPerSec: wanted.has('disk') ? jitter(write) : null,
              netDownBytesPerSec: wanted.has('network') ? jitter(down) : null,
              netUpBytesPerSec: wanted.has('network') ? jitter(up) : null
            };
          })
        : null;
    return {
      at: Date.now(),
      cpu: {
        usage: cpu,
        user: cpu * 0.66,
        system: cpu * 0.34,
        cores: Array.from({ length: 14 }, (_, index) =>
          clamp((index < 10 ? cpu + 0.06 : cpu * 0.5) + (rnd() - 0.5) * 0.3, 0.02, 1)
        ),
        load: [2.14, 1.86, 1.61],
        powerWatts: wanted.has('cpu') ? 3 + cpu * 18 : null
      },
      gpu: { usage: gpu, powerWatts: wanted.has('gpu') ? 0.4 + gpu * 14 : null },
      memory: {
        totalBytes: totalMemory,
        usedBytes: used,
        appBytes: used * 0.58,
        wiredBytes: used * 0.22,
        compressedBytes: used * 0.2,
        cachedBytes: 6.1 * GB,
        swapUsedBytes: flags.has('mem-pressure') ? 6.4 * GB : 1.2 * GB,
        pressure: flags.has('mem-pressure') ? 'warning' : 'normal'
      },
      disk: {
        volumes: [
          {
            name: 'Macintosh HD',
            mountPoint: '/',
            totalBytes: 494.4 * GB,
            freeBytes: free,
            internal: true
          },
          {
            name: 'T7 Shield',
            mountPoint: '/Volumes/T7 Shield',
            totalBytes: 1000 * GB,
            freeBytes: 412 * GB,
            internal: false
          }
        ],
        readBytesPerSec: (4 + rnd() * 20) * MB,
        writeBytesPerSec: (1 + rnd() * 6) * MB,
        devices: DEVICES.map((device, index) =>
          index === 0 && flags.has('disk-failing') ? { ...device, smart: 'failing' as const } : device
        )
      },
      network: {
        interface: 'en0',
        kind: 'wifi',
        localIp: '192.168.1.23',
        downBytesPerSec: (0.4 + rnd() * 2.2) * MB,
        upBytesPerSec: (30 + rnd() * 120) * KB,
        totalDownBytes: 12.4 * GB,
        totalUpBytes: 1.1 * GB
      },
      battery: hasBattery
        ? {
            percent: 0.86,
            charging: true,
            onAc: true,
            minutesRemaining: 35,
            healthPercent: 0.92,
            cycleCount: 214,
            designCapacityMah: 8579,
            maxCapacityMah: 7893,
            temperatureC: 31,
            voltageV: 12.6,
            powerWatts: 12.4,
            adapterWatts: 96
          }
        : null,
      sensors: SENSORS.map(([key, name, group, offset]) => ({
        key,
        name,
        group,
        celsius: temp + offset
      })),
      fans: hasFans ? fans : null,
      processes: listed,
      processGroups: listed ? mergeProcesses(listed, processes) : null,
      cpuBreakdown: listed ? breakdown(listed, cpu) : null,
      health: {
        level: issues.some(issue => issue.level === 'critical') ? 'critical' : issues.length ? 'attention' : 'good',
        issues
      }
    };
  }

  function tick() {
    history.push(sample());
    if (history.length > HISTORY) history.shift();
    void events.monitorSample.emit(history[history.length - 1]!);
  }
  function reschedule() {
    if (views.size && !timer) timer = setInterval(tick, sampleMs);
    if (!views.size && timer) {
      clearInterval(timer);
      timer = null;
    }
  }
  const stop = () => {
    if (timer) clearInterval(timer);
    timer = null;
    views.clear();
  };
  stopPrevious = stop;

  // Seed a full minute so charts are not empty on first open.
  const seededAt = Date.now();
  for (let index = 0; index < HISTORY; index += 1)
    history.push({ ...sample(), at: seededAt - (HISTORY - 1 - index) * 1000 });

  function fanChanged(reason: FanReason) {
    void events.fanChanged.emit({ fans, reason });
    return fans;
  }
  // Same order and codes as Rust: chip gate, then the helper. Heat never blocks manual control.
  function requireManual() {
    if (!helper.manualAllowed) throw appError('E_FAN_UNSUPPORTED', 'fan_control');
    if (helper.status !== 'enabled') throw appError('E_FAN_HELPER_NOT_ENABLED', 'helper_not_enabled');
  }
  const pick = (fanId: number | null) => (fan: FanState) => fanId == null || fan.id === fanId;

  const handlers: Record<string, (args: Record<string, unknown>) => unknown> = {
    monitor_subscribe: (args): MonitorSnapshot => {
      views.set(args.view as MonitorView, args.modules as MonitorModule[]);
      reschedule();
      return { system, available, history: history.slice(-HISTORY) };
    },
    /** The last hour at 5-second steps; ten minutes asleep half an hour ago leave a gap, as in Rust (M3). */
    monitor_trend: (): MonitorTrendPoint[] => {
      const now = Math.floor(Date.now() / 5000) * 5000;
      return Array.from({ length: 720 }, (_, index) => {
        const at = now - (719 - index) * 5000;
        const wave = Math.sin(index / 37) * 0.5 + 0.5;
        return {
          at,
          cpu: clamp(cpu * 0.6 + wave * 0.35, 0.02, 1),
          gpu: clamp(0.1 + wave * 0.2, 0, 1),
          memory: flags.has('mem-pressure') ? 0.94 : 0.62,
          diskRead: (2 + wave * 18) * MB,
          diskWrite: (1 + wave * 4) * MB,
          netDown: (0.2 + wave * 2) * MB,
          netUp: (20 + wave * 90) * KB,
          temperature: 46 + wave * 22,
          fanRpm: hasFans ? 1200 + wave * 1800 : null,
          battery: hasBattery ? clamp(0.62 + index * 0.0004, 0, 1) : null
        };
      }).filter((_, index) => index < 300 || index >= 420);
    },
    monitor_unsubscribe: args => {
      views.delete(args.view as MonitorView);
      reschedule();
      return null;
    },
    monitor_quit_app: args => {
      const entry = processes.find(process => process.pid === args.pid);
      if (!entry || entry.kind !== 'app' || !entry.quittable) throw appError('E_INVALID_ARG', 'process_not_quittable');
      processes = processes.filter(process => process.pid !== entry.pid);
      return null;
    },
    monitor_open_activity_monitor: () => null,
    monitor_open_menu_bar_settings: () => null,
    monitor_open_in_main: () => null,
    monitor_menu_bar_status: (): MenuBarItemStatus[] =>
      MENU_BAR.filter(module => available.includes(module) && settings().monitorMenuBar[module].enabled).map(
        module => ({
          module,
          installed: true,
          visible: null
        })
      ),
    fan_helper_status: () => {
      // The user approved in System Settings while OpenNavo waited (simulated on the next check).
      if (helper.status === 'requires_approval') helper = { ...helper, status: 'enabled' };
      return helper;
    },
    fan_helper_install: () => {
      if (!helper.manualAllowed) throw appError('E_FAN_UNSUPPORTED', 'fan_control');
      // Rust: only a developer-signed OpenNavo.app carries a helper it can register.
      if (flags.has('fan-unsigned')) throw appError('E_FAN_HELPER_UNSIGNED', 'helper_bundle');
      // Registering asks macOS, which waits for the user's approval in System Settings.
      if (helper.status === 'not_installed' || helper.status === 'error')
        helper = { ...helper, status: 'requires_approval' };
      return helper;
    },
    fan_helper_open_approval: () => null,
    fan_helper_uninstall: () => {
      fans = fans.map(fan => ({ ...fan, mode: 'auto', targetRpm: null }));
      helper = { ...helper, status: 'not_installed' };
      fanChanged('helper_uninstalled');
      return null;
    },
    fan_set_mode: args => {
      const mode = args.mode as FanState['mode'];
      if (mode === 'manual') requireManual();
      fans = fans.map(fan =>
        pick(args.fanId as number | null)(fan)
          ? {
              ...fan,
              mode,
              targetRpm: mode === 'manual' ? (fan.targetRpm ?? Math.max(fan.minRpm, fan.rpm)) : null
            }
          : fan
      );
      return fanChanged('user');
    },
    fan_set_speed: args => {
      requireManual();
      const rpm = args.rpm as number;
      fans = fans.map(fan =>
        pick(args.fanId as number | null)(fan)
          ? { ...fan, mode: 'manual', targetRpm: clamp(Math.round(rpm), fan.minRpm, fan.maxRpm) }
          : fan
      );
      updateSettings({
        fanLastManual: Object.fromEntries(fans.map(fan => [fan.id, fan.targetRpm ?? fan.minRpm]))
      });
      return fanChanged('user');
    },
    fan_restore_last: () => {
      requireManual();
      const last = settings().fanLastManual;
      if (!last) throw appError('E_FAN_NO_MANUAL_SPEED', 'no_manual_speed');
      fans = fans.map(fan => (last[fan.id] != null ? { ...fan, mode: 'manual', targetRpm: last[fan.id]! } : fan));
      return fanChanged('user');
    }
  };

  return { handlers, stop };
}
