import { computed, ref, shallowRef } from 'vue';
import { defineStore } from 'pinia';
import { commands, unwrap } from '@/ipc/client';
import type { AppSettings } from '@/ipc/client';
import type {
  AppSignatureReport,
  AvProgress,
  AvScanKind,
  AvScanResult,
  AvScheduleStatus,
  InstallScanResult,
  PasteAction,
  PasteBlocked,
  PasteProtectionStatus,
  VirusTotalStatus,
  ProtectionEvent,
  QuarantineEntry,
  SecurityChanged,
  SecurityOverview,
  SecuritySettings,
  StartupAction,
  StartupItem,
  StartupSnapshot
} from '@/ipc/bindings';
import { useSettingsStore } from './settings';

/** Rust's defaults, shown until settings load: ask before the database download, check every 4 hours (plan D5, D9). */
const DEFAULTS: SecuritySettings = {
  avConsent: false,
  avDbCheckHours: 4,
  avKeepResident: false,
  watchDownloads: true,
  startupAlerts: true,
  avExclusions: [],
  // S2 (plan §6): scheduled scans and the optional features start off; scanning before installs is on.
  avSchedule: 'off',
  avScheduleTime: '03:00',
  avScheduleWeekday: 1,
  avScanBeforeInstall: true,
  pasteProtection: false,
  virusTotalEnabled: false
};
const SECURITY_KEYS = Object.keys(DEFAULTS) as (keyof SecuritySettings)[];

/**
 * Security (store/security plan §6): check-up, virus scans with the bundled ClamAV engine, startup items, protection
 * and quarantine. Rust owns every scan and action; this store mirrors their state from commands and events.
 */
export const useSecurityStore = defineStore('security', () => {
  const settings = useSettingsStore();
  const overview = shallowRef<SecurityOverview | null>(null);
  const startup = shallowRef<StartupSnapshot | null>(null);
  const quarantine = shallowRef<QuarantineEntry[]>([]);
  const events = shallowRef<ProtectionEvent[]>([]);
  /** The scan on screen: the running one, or the last finished one until another starts. */
  const scan = shallowRef<AvScanResult | null>(null);
  const progress = ref<AvProgress | null>(null);
  /** IDs of startup items or findings with an action in flight, so their buttons stay disabled. */
  const busy = ref(new Set<string>());
  /** Startup items Rust just reported as new (startup:added), for the in-app notice. */
  const added = shallowRef<StartupItem[]>([]);
  // S2 (store/security plan §6): scheduled scans, scans before installs, signature check-up and the optional features.
  const schedule = shallowRef<AvScheduleStatus | null>(null);
  const signatures = shallowRef<AppSignatureReport | null>(null);
  const checkingSignatures = ref(false);
  /** Apps whose signature changed in the latest check (app-signatures:changed), for the in-app notice. */
  const signatureChanges = shallowRef<AppSignatureReport['apps']>([]);
  /** The latest scan of a Homebrew download before its install (install:scanned). */
  const installScan = shallowRef<InstallScanResult | null>(null);
  /** A paste the protection paused in a terminal (paste:blocked), waiting for Allow Once or Discard. */
  const paste = shallowRef<PasteBlocked | null>(null);
  const pasteStatus = shallowRef<PasteProtectionStatus | null>(null);
  const virusTotal = shallowRef<VirusTotalStatus | null>(null);

  const attention = computed(() => overview.value?.attention ?? 0);
  const scanning = computed(() => scan.value?.state === 'running');

  const securitySettings = computed<SecuritySettings>(() => {
    const value = settings.value;
    if (!value) return DEFAULTS;
    return Object.fromEntries(SECURITY_KEYS.map(key => [key, value[key]])) as SecuritySettings;
  });

  // The security keys have the same types in Settings; TypeScript cannot relate the two indexed types itself.
  async function setSecuritySetting<K extends keyof SecuritySettings>(key: K, entry: SecuritySettings[K]) {
    if (!settings.value) await settings.load();
    await settings.update(key, entry as AppSettings[K]);
  }

  let scanRevision = 0;
  let scanTarget: string | null = null;
  let overviewLoad: Promise<void> | null = null;
  function loadOverview(): Promise<void> {
    if (overviewLoad) return overviewLoad;
    const revision = scanRevision;
    const restoreHistory = scan.value === null && scanTarget === null;
    overviewLoad = (async () => {
      overview.value = await unwrap(commands.securityOverview());
      const id = overview.value.lastScan?.scanId;
      if (!restoreHistory || !id || scan.value || scanTarget !== null || revision !== scanRevision) return;
      const result = await unwrap(commands.avScanGet(id));
      if (!scan.value && scanTarget === null && revision === scanRevision) scan.value = result;
    })().finally(() => {
      overviewLoad = null;
    });
    return overviewLoad;
  }

  /** A startup scan is running; what is shown meanwhile is the last one. */
  const startupRefreshing = ref(false);
  // A startup scan takes a while. The saved list shows at once; while a scan runs, another request waits for it
  // instead of starting over.
  let startupLoad: Promise<void> | null = null;
  let startupRevision = 0;
  function loadStartup() {
    startupLoad ??= (async () => {
      if (!startup.value) {
        const revision = startupRevision;
        const saved = await unwrap(commands.startupCached()).catch(() => null);
        if (saved && !startup.value && revision === startupRevision) startup.value = saved;
      }
      startupRefreshing.value = true;
      try {
        const revision = startupRevision;
        const result = await unwrap(commands.startupScan());
        // An action/cache refresh may overtake this scan's IPC response. Read the
        // persisted state again instead of putting an older switch state back.
        if (revision === startupRevision) startup.value = result;
        else await reloadStartup();
      } finally {
        startupRefreshing.value = false;
      }
    })().finally(() => {
      startupLoad = null;
    });
    return startupLoad;
  }
  /** What the last scan or action saved: switches and additions show at once, without another scan. */
  async function reloadStartup() {
    const revision = ++startupRevision;
    const saved = await unwrap(commands.startupCached());
    if (saved && revision === startupRevision) startup.value = saved;
  }
  /** Opens an app at login; it is listed at once. */
  async function addLoginItem(path: string) {
    ++startupRevision;
    const item = await unwrap(commands.startupLoginAdd(path));
    await reloadStartup();
    void loadOverview().catch(() => undefined);
    return item;
  }

  async function loadQuarantine() {
    quarantine.value = await unwrap(commands.quarantineList());
  }

  async function loadEvents() {
    events.value = await unwrap(commands.protectionEvents(50));
  }

  /** First download needs consent: the database is about 113 MB and the engine about 1.6 GB of memory while scanning. */
  async function consentAndDownload() {
    await setSecuritySetting('avConsent', true);
    await updateDatabase();
  }

  async function updateDatabase() {
    await unwrap(commands.avDbUpdate());
    await loadOverview();
  }

  async function startScan(kind: AvScanKind, paths: string[] | null = null) {
    const revision = ++scanRevision;
    const previousTarget = scanTarget;
    scanTarget = 'starting';
    try {
      const { scanId } = await unwrap(commands.avScanStart(kind, paths));
      if (revision === scanRevision) scanTarget = scanId;
      const result = await unwrap(commands.avScanGet(scanId));
      if (revision === scanRevision) {
        progress.value = null;
        scan.value = result;
      }
      void loadOverview().catch(() => undefined);
    } catch (error) {
      if (revision === scanRevision) scanTarget = previousTarget;
      throw error;
    }
  }

  async function cancelScan() {
    if (!scan.value || scan.value.state !== 'running') return;
    await unwrap(commands.avScanCancel(scan.value.scanId));
  }

  /** A scan someone else started, such as the scheduled one, replaces a finished scan on screen. */
  const takesOver = (scanId: string) => scan.value?.scanId !== scanId && scan.value?.state !== 'running';

  function applyProgress(payload: AvProgress) {
    if (takesOver(payload.scanId)) {
      if (scanTarget === payload.scanId) return;
      scanTarget = payload.scanId;
      const revision = ++scanRevision;
      void unwrap(commands.avScanGet(payload.scanId))
        .then(result => {
          if (revision === scanRevision && takesOver(result.scanId)) {
            scan.value = result;
            progress.value = payload;
          }
        })
        .catch(() => {
          if (revision === scanRevision) scanTarget = null;
        });
      return;
    }
    if (scan.value?.scanId !== payload.scanId) return;
    progress.value = payload;
    // Findings arrive with the progress count; read them when it moves so threats show up before the scan ends.
    if (payload.findings !== scan.value.findings.length)
      void unwrap(commands.avScanGet(payload.scanId))
        .then(result => {
          if (scan.value?.scanId === result.scanId && scan.value.state === 'running') scan.value = result;
        })
        .catch(() => undefined);
  }

  function applyFinished(result: AvScanResult) {
    if (scan.value && scan.value.scanId !== result.scanId && !takesOver(result.scanId)) return;
    ++scanRevision;
    scanTarget = result.scanId;
    scan.value = result;
    progress.value = null;
    void loadOverview().catch(() => undefined);
  }

  async function withBusy<T>(ids: string[], work: () => Promise<T>): Promise<T> {
    busy.value = new Set([...busy.value, ...ids]);
    try {
      return await work();
    } finally {
      const next = new Set(busy.value);
      for (const id of ids) next.delete(id);
      busy.value = next;
    }
  }

  async function actOnFindings(ids: string[], action: 'quarantine' | 'delete' | 'trust' | 'reveal') {
    const current = scan.value;
    if (!current) return null;
    return withBusy(ids, async () => {
      const outcome = await unwrap(commands.avFindingsAct(current.scanId, ids, action));
      const result = await unwrap(commands.avScanGet(current.scanId));
      if (scan.value?.scanId === current.scanId) scan.value = result;
      return outcome;
    });
  }

  async function actOnStartup(ids: string[], action: StartupAction | 'mark_reviewed') {
    return withBusy(ids, async () => {
      ++startupRevision;
      const outcome = await unwrap(commands.startupItemAct(ids, action));
      await reloadStartup();
      // SIP, FileVault and other posture probes do not determine whether this
      // action succeeded. Keep them out of the switch's pending state.
      void loadOverview().catch(() => undefined);
      return outcome;
    });
  }

  /**
   * The scan on screen, read again: restoring a threat from the quarantine reopens its finding and deleting it marks
   * it deleted, which Rust records on the scan, not on the quarantine list.
   */
  async function reloadScan() {
    const current = scan.value;
    if (!current) return;
    const result = await unwrap(commands.avScanGet(current.scanId));
    if (scan.value?.scanId === result.scanId) scan.value = result;
  }

  async function restore(ids: string[]) {
    return withBusy(ids, async () => {
      const outcome = await unwrap(commands.quarantineRestore(ids));
      await Promise.all([loadQuarantine(), reloadScan()]);
      return outcome;
    });
  }

  async function purge(ids: string[]) {
    return withBusy(ids, async () => {
      const outcome = await unwrap(commands.quarantineDelete(ids));
      await Promise.all([loadQuarantine(), reloadScan()]);
      return outcome;
    });
  }

  function applyAdded(items: StartupItem[]) {
    added.value = items;
    void loadOverview().catch(() => undefined);
    if (startup.value) void reloadStartup().catch(() => undefined);
  }

  /** security:changed — reread only what changed; the overview always, since the sidebar dot depends on it. */
  function changed({ reason }: SecurityChanged) {
    void loadOverview().catch(() => undefined);
    // The scan or action that sent this has saved its result already.
    if (reason === 'startup' && startup.value) void reloadStartup().catch(() => undefined);
    if (reason === 'quarantine') {
      void loadQuarantine().catch(() => undefined);
      void reloadScan().catch(() => undefined);
    }
    if (reason === 'events') void loadEvents().catch(() => undefined);
    if (reason === 'schedule') void loadSchedule().catch(() => undefined);
    if (reason === 'signatures') void loadSignatures().catch(() => undefined);
  }

  async function loadSchedule() {
    schedule.value = await unwrap(commands.avScheduleStatus());
  }

  /** The last signature report, without checking again. */
  async function loadSignatures() {
    signatures.value = await unwrap(commands.appSignatureGet());
  }

  /** Check every installed app's signature now; read-only, it may take a while on a large Applications folder. */
  async function checkSignatures() {
    checkingSignatures.value = true;
    try {
      signatures.value = await unwrap(commands.appSignatureCheck());
    } finally {
      checkingSignatures.value = false;
    }
  }

  function applySignatureChanges(apps: AppSignatureReport['apps']) {
    signatureChanges.value = apps;
    void loadSignatures().catch(() => undefined);
  }

  /** Scan-before-install results by task (S2): from install:scanned, and from Rust for tasks before this session. */
  const installScans = shallowRef(new Map<string, InstallScanResult>());
  const askedInstallScans = new Set<string>();

  function applyInstallScan(result: InstallScanResult) {
    installScan.value = result;
    installScans.value = new Map(installScans.value).set(result.taskId, result);
  }

  /** Each finished task is asked about once; tasks without a scan stay unlisted. */
  async function loadInstallScans(taskIds: readonly string[]) {
    const missing = taskIds.filter(id => !installScans.value.has(id) && !askedInstallScans.has(id));
    if (!missing.length) return;
    missing.forEach(id => askedInstallScans.add(id));
    const results = await Promise.all(missing.map(id => unwrap(commands.installScanGet(id)).catch(() => null)));
    const found = results.filter(result => result !== null);
    if (!found.length) return;
    const next = new Map(installScans.value);
    found.forEach(result => next.set(result.taskId, result));
    installScans.value = next;
  }

  async function loadPasteStatus() {
    pasteStatus.value = await unwrap(commands.pasteProtectionStatus());
  }

  /** Ask macOS for Accessibility access; only ever on the user's click (plan §6, D8). */
  async function requestPasteAccess() {
    pasteStatus.value = await unwrap(commands.pasteProtectionRequestAccess());
  }

  /** Allow the paused paste once or discard it; an expired one is gone either way. */
  async function actOnPaste(action: PasteAction) {
    const current = paste.value;
    if (!current) return;
    paste.value = null;
    await unwrap(commands.pasteProtectionAct(current.id, action));
  }

  async function loadVirusTotal() {
    virusTotal.value = await unwrap(commands.virusTotalStatus());
  }

  /** Store the user's own key in the Keychain; null removes it. */
  async function setVirusTotalKey(key: string | null) {
    virusTotal.value = await unwrap(commands.virusTotalKeySet(key));
  }

  /** Lookups need the switch on and the user's own key saved (D8). */
  const virusTotalReady = computed(() => securitySettings.value.virusTotalEnabled && Boolean(virusTotal.value?.hasKey));

  /** Only the SHA-256 goes to VirusTotal, and only on the user's click; files are never uploaded. */
  const lookupVirusTotal = (sha256: string) => unwrap(commands.virusTotalLookup(sha256));

  return {
    overview,
    startup,
    quarantine,
    events,
    scan,
    progress,
    busy,
    added,
    attention,
    scanning,
    securitySettings,
    setSecuritySetting,
    loadOverview,
    loadStartup,
    reloadStartup,
    startupRefreshing,
    addLoginItem,
    loadQuarantine,
    loadEvents,
    consentAndDownload,
    updateDatabase,
    startScan,
    cancelScan,
    applyProgress,
    applyFinished,
    applyAdded,
    actOnFindings,
    actOnStartup,
    restore,
    purge,
    changed,
    schedule,
    signatures,
    checkingSignatures,
    signatureChanges,
    installScan,
    paste,
    pasteStatus,
    virusTotal,
    loadSchedule,
    loadSignatures,
    checkSignatures,
    applySignatureChanges,
    applyInstallScan,
    installScans,
    loadInstallScans,
    loadPasteStatus,
    requestPasteAccess,
    actOnPaste,
    loadVirusTotal,
    setVirusTotalKey,
    virusTotalReady,
    lookupVirusTotal
  };
});
