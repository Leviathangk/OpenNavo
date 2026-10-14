import { computed, ref } from 'vue';
import { defineStore } from 'pinia';
import type {
  FanChange,
  FanHelperStatus,
  FanMode,
  FanState,
  MenuBarItemSetting,
  MonitorMenuBar,
  MonitorModule,
  MonitorSample,
  MonitorTrendPoint,
  MonitorView,
  SystemInfo
} from '@/ipc/bindings';
import { commands, unwrap } from '@/ipc/client';
import { i18n } from '@/i18n';
import { useToasts } from '@/composables/useToasts';
import { useSettingsStore } from './settings';

/**
 * Computer monitoring (monitor proposal §11–§16): one sampler in Rust and three outlets here — the page,
 * a module's detail sheet and the menu bar popover. Each outlet subscribes while it is visible.
 */
/** A module shown on its own: a card, a menu bar item, a settings row. */
export type DisplayModule = keyof MonitorMenuBar;
/** Display order of cards, menu bar items and settings rows (§12). Fans are part of sensors, as in Stats. */
export const MODULES: readonly DisplayModule[] = ['cpu', 'gpu', 'memory', 'disk', 'network', 'battery', 'sensors'];
/** What Rust samples for a module: sensors show the temperatures and the fans. */
const sampled = (module: MonitorModule): MonitorModule[] => (module === 'sensors' ? ['sensors', 'fans'] : [module]);
/** Chart history kept in memory: the last 60 seconds (decision 8). */
export const HISTORY = 60;
/** The detail's long chart (M3): one point per 5 seconds over the last hour, kept in memory by Rust. */
export const TREND_STEP_MS = 5000;
export const TREND_POINTS = 720;
const HIDDEN_ITEM: MenuBarItemSetting = { enabled: false, style: 'text' };

const mean = (values: readonly number[]) =>
  values.length ? values.reduce((sum, value) => sum + value, 0) / values.length : null;
/** A trend point from a live sample, so an open hour chart keeps moving between loads. */
const trendPoint = (sample: MonitorSample): MonitorTrendPoint => ({
  at: sample.at,
  cpu: sample.cpu.usage,
  gpu: sample.gpu?.usage ?? null,
  memory: sample.memory.totalBytes ? sample.memory.usedBytes / sample.memory.totalBytes : null,
  diskRead: sample.disk.readBytesPerSec,
  diskWrite: sample.disk.writeBytesPerSec,
  netDown: sample.network.downBytesPerSec,
  netUp: sample.network.upBytesPerSec,
  temperature: sample.sensors?.length ? Math.max(...sample.sensors.map(reading => reading.celsius)) : null,
  fanRpm: mean((sample.fans ?? []).map(fan => fan.rpm)),
  battery: sample.battery?.percent ?? null
});

export const useMonitorStore = defineStore('monitor', () => {
  const settings = useSettingsStore();

  const system = ref<SystemInfo | null>(null);
  const available = ref<MonitorModule[]>([]);
  const history = ref<MonitorSample[]>([]);
  const views = ref<Partial<Record<MonitorView, MonitorModule[]>>>({});
  const helper = ref<FanHelperStatus | null>(null);
  /** Latest fan change pushed by Rust, including automatic hand-backs to the system and why. */
  const fanChange = ref<FanChange | null>(null);
  /** Fan states from a command or fan:changed, newer than the last sample until the next one arrives. */
  const fanOverride = ref<FanState[] | null>(null);
  /** Null until a detail switches to the hour view. */
  const trend = ref<MonitorTrendPoint[] | null>(null);

  const sample = computed(() => history.value.at(-1) ?? null);
  const modules = computed(() =>
    MODULES.filter(module => sampled(module).some(part => available.value.includes(part)))
  );
  const fans = computed(() => fanOverride.value ?? sample.value?.fans ?? null);
  const menuBar = computed(() => settings.value?.monitorMenuBar ?? null);
  const item = (module: DisplayModule) => menuBar.value?.[module] ?? HIDDEN_ITEM;
  const menuBarCount = computed(() => MODULES.filter(module => item(module).enabled).length);
  /** One value per kept sample, oldest first; null where Rust had no reading yet (first delta, sleep). */
  const series = (pick: (sample: MonitorSample) => number | null | undefined) =>
    history.value.map(entry => pick(entry) ?? null);

  const generations: Partial<Record<MonitorView, number>> = {};
  async function subscribe(view: MonitorView, wanted: MonitorModule[]) {
    const generation = (generations[view] ?? 0) + 1;
    generations[view] = generation;
    views.value = { ...views.value, [view]: wanted };
    const snapshot = await unwrap(commands.monitorSubscribe(view, [...new Set(wanted.flatMap(sampled))]));
    if (generations[view] !== generation) return;
    system.value = snapshot.system;
    available.value = snapshot.available;
    // Rust keeps one history for all outlets; take it unless this window already holds more.
    if (snapshot.history.length >= history.value.length) history.value = snapshot.history.slice(-HISTORY);
  }

  /** Which modules this Mac has, for screens that show no samples (settings): a module-less subscription, released at once. */
  async function loadAvailable() {
    if (available.value.length || views.value.page) return;
    const request = subscribe('page', []);
    const generation = generations.page;
    try {
      await request;
    } finally {
      // A page opened while this request was pending owns a newer subscription.
      if (generations.page === generation) await unsubscribe('page');
    }
  }

  async function unsubscribe(view: MonitorView) {
    if (!(view in views.value)) return;
    generations[view] = (generations[view] ?? 0) + 1;
    const next = { ...views.value };
    delete next[view];
    views.value = next;
    await unwrap(commands.monitorUnsubscribe(view)).catch(() => undefined);
  }

  function applySample(next: MonitorSample) {
    history.value = [...history.value.slice(-(HISTORY - 1)), next];
    fanOverride.value = null;
    const last = trend.value?.at(-1);
    if (trend.value && (!last || next.at - last.at >= TREND_STEP_MS))
      trend.value = [
        ...trend.value.filter(point => point.at > next.at - TREND_POINTS * TREND_STEP_MS),
        trendPoint(next)
      ];
  }

  async function loadTrend() {
    trend.value = await unwrap(commands.monitorTrend());
  }
  /** One slot per 5 seconds over the last hour, oldest first; null where nothing was sampled (paused, asleep). */
  function trendSeries(pick: (point: MonitorTrendPoint) => number | null) {
    const points = trend.value ?? [];
    const end = sample.value?.at ?? points.at(-1)?.at ?? 0;
    const slots: (number | null)[] = Array.from({ length: TREND_POINTS }, () => null);
    for (const point of points) {
      const index = TREND_POINTS - 1 - Math.floor((end - point.at) / TREND_STEP_MS);
      if (index >= 0 && index < TREND_POINTS) slots[index] = pick(point);
    }
    return slots;
  }

  function applyFanChange(change: FanChange) {
    fanChange.value = change;
    fanOverride.value = change.fans;
  }

  async function setMenuBar(module: DisplayModule, patch: Partial<MenuBarItemSetting>) {
    const current = menuBar.value;
    if (!current) return;
    await settings.update('monitorMenuBar', {
      ...current,
      [module]: { ...current[module], ...patch }
    });
    // With this off, closing the window quits OpenNavo and every item goes with it (§12): offer to keep it running.
    if (patch.enabled && settings.value?.keepInMenuBarOnClose === false) offerKeepInMenuBar();
  }

  function offerKeepInMenuBar() {
    const { t } = i18n.global;
    useToasts().pushAction(
      {
        id: 'monitor-keep-in-menu-bar',
        tone: 'info',
        title: t('monitor.menuBar.keepTitle'),
        description: t('monitor.menuBar.keepBody'),
        actionLabel: t('monitor.menuBar.keepAction')
      },
      () => void settings.update('keepInMenuBarOnClose', true).catch(() => undefined)
    );
  }

  const dismissIntro = () => settings.update('monitorMenuBarIntro', true);
  /** Which menu bar items Rust has created; visible is null where macOS gives no answer (notch, menu bar permission). */
  const menuBarStatus = () => unwrap(commands.monitorMenuBarStatus());
  const quitApp = (pid: number) => unwrap(commands.monitorQuitApp(pid));
  const openActivityMonitor = () => unwrap(commands.monitorOpenActivityMonitor());
  const openMenuBarSettings = () => unwrap(commands.monitorOpenMenuBarSettings());

  let helperRevision = 0;
  let helperRequest: { revision: number; promise: Promise<FanHelperStatus> } | null = null;
  let helperMutation: Promise<FanHelperStatus> | null = null;
  function loadHelper(): Promise<FanHelperStatus> {
    // Settings and the fan sheet may both poll. Never queue more probes behind a slow XPC exchange.
    if (helperMutation) return helperMutation;
    if (helperRequest?.revision === helperRevision) return helperRequest.promise;
    const revision = helperRevision;
    const promise = unwrap(commands.fanHelperStatus())
      .then(result => {
        // A probe started before install/uninstall must not overwrite the newer result.
        if (revision === helperRevision) helper.value = result;
        return result;
      })
      .finally(() => {
        if (helperRequest?.promise === promise) helperRequest = null;
      });
    helperRequest = { revision, promise };
    return promise;
  }
  function changeHelper(operation: () => Promise<FanHelperStatus>): Promise<FanHelperStatus> {
    // Preserve a different action from another view instead of reporting the pending action as its result.
    if (helperMutation) return helperMutation.then(() => changeHelper(operation));
    helperRevision++;
    helperMutation = operation()
      .then(result => {
        helper.value = result;
        return result;
      })
      .finally(() => {
        helperMutation = null;
      });
    return helperMutation;
  }
  const installHelper = () => changeHelper(() => unwrap(commands.fanHelperInstall()));
  /** Opens Login Items & Extensions, where macOS asks the user to allow the helper (requires_approval). */
  const openApproval = () => unwrap(commands.fanHelperOpenApproval());
  async function uninstallHelper() {
    await changeHelper(async () => {
      await unwrap(commands.fanHelperUninstall());
      return unwrap(commands.fanHelperStatus());
    });
  }
  /** A helper that is installed but not answering (status error) is registered again from scratch. */
  async function reinstallHelper() {
    return changeHelper(async () => {
      await unwrap(commands.fanHelperUninstall());
      return unwrap(commands.fanHelperInstall());
    });
  }
  async function setFanMode(fanId: number | null, mode: FanMode) {
    fanOverride.value = await unwrap(commands.fanSetMode(fanId, mode));
  }
  async function setFanSpeed(fanId: number | null, rpm: number) {
    fanOverride.value = await unwrap(commands.fanSetSpeed(fanId, rpm));
    // Rust keeps the speed in settings (fanLastManual) for "restore" after a relaunch; there is no settings event.
    // The menu bar popover has no settings access and never loads them.
    if (settings.value) await settings.load().catch(() => undefined);
  }
  async function restoreLastFans() {
    fanOverride.value = await unwrap(commands.fanRestoreLast());
  }

  return {
    system,
    available,
    history,
    trend,
    views,
    helper,
    fanChange,
    sample,
    modules,
    fans,
    menuBar,
    menuBarCount,
    item,
    series,
    subscribe,
    unsubscribe,
    loadAvailable,
    applySample,
    applyFanChange,
    loadTrend,
    trendSeries,
    setMenuBar,
    dismissIntro,
    menuBarStatus,
    quitApp,
    openActivityMonitor,
    openMenuBarSettings,
    loadHelper,
    installHelper,
    reinstallHelper,
    openApproval,
    uninstallHelper,
    setFanMode,
    setFanSpeed,
    restoreLastFans
  };
});
