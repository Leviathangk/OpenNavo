import { computed, onScopeDispose, ref, shallowRef } from 'vue';
import { defineStore } from 'pinia';
import { check } from '@tauri-apps/plugin-updater';
import type { Update } from '@tauri-apps/plugin-updater';
import { relaunch } from '@tauri-apps/plugin-process';
import { i18n } from '@/i18n';
import { commands, unwrap } from '@/ipc/client';
import { useSettingsStore } from './settings';
import { useTasksStore } from './tasks';

type State = 'idle' | 'checking' | 'latest' | 'available' | 'downloading' | 'installed' | 'failed';
interface CheckHistory {
  succeededAt?: number;
  attemptedAt?: number;
  promptedVersion?: string;
  promptedAt?: number;
}

const HOUR = 3_600_000;
const STORAGE_KEY = 'ONV_DESKTOP_app_update_check';

function sameDay(timestamp: number | undefined, now: number) {
  return timestamp !== undefined && new Date(timestamp).toDateString() === new Date(now).toDateString();
}

/** OpenNavo self-update state is shared by the global dialog and Settings → About. */
export const useAppUpdaterStore = defineStore('appUpdater', () => {
  const settings = useSettingsStore();
  const tasks = useTasksStore();
  const state = ref<State>('idle');
  const update = shallowRef<Update | null>(null);
  const currentVersion = ref<string | null>(null);
  const percent = ref<number | null>(null);
  const dialogOpen = ref(false);
  const installFailed = ref(false);
  const busy = computed(() => tasks.active.length > 0);
  const working = computed(() => state.value === 'checking' || state.value === 'downloading');
  let history: CheckHistory = {};
  let historyKey = STORAGE_KEY;
  let initialized = false;
  let pending: Promise<void> | null = null;
  let previewTimer: ReturnType<typeof setInterval> | undefined;

  // Preview is deliberately development-only and never downloads or replaces the native app.
  const preview = import.meta.env.DEV && import.meta.env.VITE_OPENNAVO_UPDATE_PREVIEW === '1';

  async function initialize() {
    if (initialized) return;
    const info = await unwrap(commands.appInfo());
    currentVersion.value = info.version;
    historyKey = `${STORAGE_KEY}:${info.version}`;
    try {
      const saved: unknown = JSON.parse(localStorage.getItem(historyKey) ?? '{}');
      if (saved && typeof saved === 'object') {
        const value = saved as CheckHistory;
        history = {
          succeededAt: typeof value.succeededAt === 'number' ? value.succeededAt : undefined,
          attemptedAt: typeof value.attemptedAt === 'number' ? value.attemptedAt : undefined,
          promptedVersion: typeof value.promptedVersion === 'string' ? value.promptedVersion : undefined,
          promptedAt: typeof value.promptedAt === 'number' ? value.promptedAt : undefined
        };
      }
    } catch {
      // Restricted storage still permits checks; retain this session's history in memory.
    }
    initialized = true;
  }

  function persist() {
    try {
      localStorage.setItem(historyKey, JSON.stringify(history));
    } catch {
      // Keep the updater usable when browser storage is unavailable.
    }
  }

  function show() {
    if (!update.value) return;
    dialogOpen.value = true;
    history.promptedVersion = update.value.version;
    history.promptedAt = Date.now();
    persist();
  }

  function dismiss() {
    if (state.value === 'downloading') return;
    dialogOpen.value = false;
  }

  async function checkNow(automatic = false): Promise<void> {
    if (pending) return pending;
    if (state.value === 'downloading' || state.value === 'installed') return;
    if (automatic && (!settings.value?.autoCheckAppUpdates || document.visibilityState === 'hidden')) return;

    pending = (async () => {
      try {
        await initialize();
        const now = Date.now();
        if (automatic) {
          // Throttle from request start for both success and failure. Using the
          // completion time would skip the hourly timer after a slow response.
          if (history.attemptedAt && now >= history.attemptedAt && now - history.attemptedAt < HOUR) return;
        }
        history.attemptedAt = now;
        persist();
        state.value = 'checking';
        installFailed.value = false;
        const next = await check({ timeout: 15_000 });
        const previous = update.value;
        update.value = next;
        void previous?.close().catch(() => undefined);
        history.succeededAt = Date.now();
        persist();
        state.value = next ? 'available' : 'latest';
        if (!next) dialogOpen.value = false;
        // A user disabling the setting while a request is in flight also suppresses its prompt.
        if (next && (!automatic || settings.value?.autoCheckAppUpdates)) {
          const alreadyPrompted = history.promptedVersion === next.version && sameDay(history.promptedAt, Date.now());
          if (!automatic || !alreadyPrompted) show();
        }
      } catch {
        if (!automatic) state.value = 'failed';
        else state.value = update.value ? 'available' : 'idle';
      }
    })();
    try {
      await pending;
    } finally {
      pending = null;
    }
  }

  async function install(previewFailure = false) {
    if (!update.value || working.value || state.value === 'installed') return;
    state.value = 'downloading';
    installFailed.value = false;
    percent.value = null;
    if (preview) {
      percent.value = 0;
      previewTimer = setInterval(() => {
        percent.value = Math.min(100, (percent.value ?? 0) + 5);
        if (previewFailure && percent.value >= 50) {
          clearInterval(previewTimer);
          previewTimer = undefined;
          state.value = 'available';
          installFailed.value = true;
          return;
        }
        if (percent.value === 100) {
          clearInterval(previewTimer);
          previewTimer = undefined;
          state.value = 'installed';
        }
      }, 400);
      return;
    }
    let total = 0;
    let done = 0;
    try {
      await update.value.downloadAndInstall(event => {
        if (event.event === 'Started') total = event.data.contentLength ?? 0;
        if (event.event === 'Progress') {
          done += event.data.chunkLength;
          percent.value = total ? Math.min(100, Math.round((done / total) * 100)) : null;
        }
        if (event.event === 'Finished') percent.value = 100;
      });
      state.value = 'installed';
    } catch {
      // Retain the signed update handle so retry does not require another manifest request.
      state.value = 'available';
      installFailed.value = true;
    }
  }

  async function restart() {
    if (busy.value || state.value !== 'installed') return;
    if (preview) {
      dialogOpen.value = false;
      state.value = 'available';
      percent.value = null;
      return;
    }
    await relaunch();
  }

  async function showPreview() {
    if (!preview) return;
    await initialize();
    const { Update } = await import('@tauri-apps/plugin-updater');
    update.value = new Update({
      rid: 0,
      currentVersion: currentVersion.value ?? '0.0.0',
      version: '0.1.4',
      body: i18n.global.t('updater.previewNotes'),
      rawJson: {}
    });
    state.value = 'available';
    dialogOpen.value = true;
  }

  onScopeDispose(() => {
    clearInterval(previewTimer);
    if (!preview) void update.value?.close().catch(() => undefined);
  });

  return {
    state,
    update,
    currentVersion,
    percent,
    dialogOpen,
    installFailed,
    busy,
    working,
    preview,
    initialize,
    checkNow,
    show,
    dismiss,
    install,
    restart,
    showPreview
  };
});
