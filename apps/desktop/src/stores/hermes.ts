import { computed, ref, watch } from 'vue';
import { defineStore } from 'pinia';
import type { PackageSummary } from '@opennavo/api';
import type { HermesEffort, HermesProvider } from '@/hermes/providers';
import {
  HERMES_AUX_TASKS,
  HERMES_EFFORTS,
  HERMES_PROVIDER_MAP,
  defaultProviderEndpoint,
  modelId,
  providerModels,
  shortModel,
  type HermesAuxTask
} from '@/hermes/providers';
import { commands, unwrap, IpcError } from '@/ipc/client';
import type { HermesLoginStatus, HermesModelOptions, HermesRuntimeStatus } from '@/ipc/bindings';
import { useToasts } from '@/composables/useToasts';
import rectangleIcon from '@/assets/hermes/rectangle.png';

export type HermesControlLevel = 'open' | 'guarded' | 'full';

export interface HermesMacSettings {
  enabled: boolean;
  level: HermesControlLevel;
  terminal: boolean;
  screen: boolean;
  files: boolean;
  install: boolean;
  cron: boolean;
  confirmWrites: boolean;
}

export interface HermesModelSettings {
  provider: HermesProvider;
  model: string;
  effort: HermesEffort;
  /** Provider priority/fast service tier, when the model supports it. */
  fast: boolean;
  endpoint: string;
  /** The key itself is owned by the runtime; the UI only remembers whether one was supplied. */
  keySet: boolean;
}

export interface HermesToolEvent {
  requestId: string;
  phase: 'start' | 'complete';
  toolId?: string;
  name: string;
  label?: string;
  duration?: number;
}

export interface HermesToolCall {
  name: string;
  label: string;
  time: string;
  toolId?: string;
}

export type HermesCard = 'cleanup' | 'updates' | 'explain';

export interface HermesMessage {
  id: string;
  role: 'user' | 'assistant';
  /** Raw user text, or an i18n key into the local Hermes content. */
  text?: string;
  textKey?: string;
  tools?: readonly HermesToolCall[];
  package?: PackageSummary;
  card?: HermesCard;
  footKey?: string;
  /** Files attached to this user turn. */
  attachments?: string[];
  /** This user message was injected into the running turn. */
  steering?: boolean;
  /** True while Hermes is still streaming this message. */
  streaming?: boolean;
  reasoning?: string;
  at: number;
}

export type HermesAuxSettings = Record<HermesAuxTask, { provider: string; model: string }>;

export function defaultAuxSettings(): HermesAuxSettings {
  return Object.fromEntries(HERMES_AUX_TASKS.map(task => [task, { provider: 'auto', model: '' }])) as HermesAuxSettings;
}

export interface HermesApprovalRequest {
  request_id?: string;
  command?: string;
  description?: string;
  choices?: string[];
}

export interface HermesSessionEvent {
  requestId: string;
  liveSessionId: string;
  sessionId: string;
}

export interface HermesContextUsage {
  used: number;
  max: number;
  percent: number;
}

export interface HermesChunk {
  requestId: string;
  delta: string;
}

export interface HermesInstallProgress {
  phase: string;
  downloaded: number;
  total: number | null;
  percent: number;
}

export interface HermesConversation {
  id: string;
  title?: string;
  messages: HermesMessage[];
  updatedAt: number;
  titleKey?: string;
  previewKey?: string;
  /** Hermes CLI session id; later turns resume the same agent context. */
  sessionId?: string;
  /** Live gateway session id for the in-flight turn (steer/interrupt). */
  liveSessionId?: string;
  /** Latest official context-window occupancy for this conversation. */
  context?: HermesContextUsage;
}

interface PersistedHermes {
  version?: number;
  model: HermesModelSettings;
  mac: HermesMacSettings;
  conversations: HermesConversation[];
  activeId: string | null;
  workingDir?: string;
  auxiliary?: HermesAuxSettings;
  forceAuxVision?: boolean;
}

const STORAGE_KEY = 'ONV_HERMES_STATE_V2';
const STORAGE_VERSION = 3;
const MODEL_OPTIONS_CACHE_KEY = 'ONV_HERMES_MODEL_OPTIONS_V2';

const RECTANGLE: PackageSummary = {
  kind: 'cask',
  token: 'rectangle',
  name: 'Rectangle',
  displayName: 'Rectangle',
  summary:
    'Free, open-source Mac window manager for moving, resizing and tiling windows with shortcuts and snap areas.',
  iconUrl: rectangleIcon,
  accentColor: '#1179BB',
  version: '2.0.4',
  primaryCategory: { slug: 'utilities', name: 'System Tools', icon: 'lucide:wrench' },
  installs30d: 9024,
  rank30d: 39,
  autoUpdates: true,
  deprecated: false,
  disabled: false,
  isFont: false,
  isLibrary: false,
  editorChoice: false,
  versionChangedAt: '2026-10-20T20:15:10.40794Z',
  sourceLocale: 'en-US',
  machineTranslated: false,
  recommendationPaused: false
};

const DEFAULT_MODEL: HermesModelSettings = {
  provider: 'nous',
  model: 'Hermes 4 · 405B',
  effort: 'medium',
  fast: false,
  endpoint: '',
  keySet: true
};

const DEFAULT_MAC: HermesMacSettings = {
  enabled: false,
  level: 'open',
  terminal: false,
  screen: false,
  files: false,
  install: false,
  cron: false,
  confirmWrites: true
};

let sequence = 0;
function id(prefix: string) {
  sequence += 1;
  return `${prefix}-${Date.now().toString(36)}-${sequence}`;
}

function message(entry: Omit<HermesMessage, 'id' | 'at'> & { at?: number }): HermesMessage {
  return { ...entry, id: id('m'), at: entry.at ?? Date.now() };
}

function defaults(): PersistedHermes {
  return {
    version: STORAGE_VERSION,
    model: { ...DEFAULT_MODEL },
    mac: { ...DEFAULT_MAC },
    conversations: [],
    activeId: null,
    workingDir: '',
    auxiliary: defaultAuxSettings(),
    forceAuxVision: false
  };
}

function read(): PersistedHermes {
  if (typeof localStorage === 'undefined') return defaults();
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return defaults();
    const parsed = JSON.parse(raw) as Partial<PersistedHermes>;
    const restoredModel = { ...DEFAULT_MODEL, ...parsed.model };
    if (!HERMES_EFFORTS.includes(restoredModel.effort)) restoredModel.effort = 'medium';
    restoredModel.fast = Boolean(restoredModel.fast);
    const auxiliary = defaultAuxSettings();
    for (const task of HERMES_AUX_TASKS) {
      const entry = parsed.auxiliary?.[task];
      if (entry && typeof entry.provider === 'string' && typeof entry.model === 'string') {
        auxiliary[task] = { provider: entry.provider, model: entry.model };
      }
    }
    return {
      model: restoredModel,
      workingDir: typeof parsed.workingDir === 'string' ? parsed.workingDir : '',
      auxiliary,
      mac: parsed.version === STORAGE_VERSION ? { ...DEFAULT_MAC, ...parsed.mac } : { ...DEFAULT_MAC },
      conversations: Array.isArray(parsed.conversations)
        ? parsed.conversations
            .map(conversation => ({
              ...conversation,
              messages: conversation.messages.map(entry => ({ ...entry, streaming: false })),
              liveSessionId: undefined
            }))
            .filter(
              conversation => !(conversation.messages.length > 0 && conversation.messages.every(entry => entry.textKey))
            )
        : [],
      activeId: parsed.activeId ?? null
    };
  } catch {
    return defaults();
  }
}

interface HermesModelOptionsCache {
  options: HermesModelOptions;
  at: number;
}

function readModelOptionsCache(): HermesModelOptionsCache | null {
  if (typeof localStorage === 'undefined') return null;
  try {
    const raw = localStorage.getItem(MODEL_OPTIONS_CACHE_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as Partial<HermesModelOptionsCache>;
    if (!parsed.options || !Array.isArray(parsed.options.providers) || typeof parsed.at !== 'number') return null;
    return { options: parsed.options, at: parsed.at };
  } catch {
    return null;
  }
}

/** Local Hermes session store. The assistant runtime is intentionally replaceable; today the demo replies locally. */
export const useHermesStore = defineStore('hermes', () => {
  const initial = read();
  const cachedOptions = readModelOptionsCache();
  const model = ref<HermesModelSettings>(initial.model);
  const mac = ref<HermesMacSettings>(initial.mac);
  const conversations = ref<HermesConversation[]>(initial.conversations);
  const activeId = ref<string | null>(initial.activeId);
  const thinking = ref(false);
  const approvalRequest = ref<HermesApprovalRequest | null>(null);
  const historyReady = ref(false);
  const stopping = ref(false);
  const stopRequested = ref(false);
  const runtime = ref<HermesRuntimeStatus | null>(null);
  const installing = ref(false);
  const installProgress = ref<HermesInstallProgress | null>(null);
  const workingDir = ref(initial.workingDir ?? '');
  const auxiliary = ref<HermesAuxSettings>(initial.auxiliary ?? defaultAuxSettings());
  const forceAuxVision = ref(Boolean(initial.forceAuxVision));
  const loginStatus = ref<HermesLoginStatus | null>(null);
  const modelOptions = ref<HermesModelOptions | null>(cachedOptions?.options ?? null);
  const modelOptionsUpdatedAt = ref<number | null>(cachedOptions?.at ?? null);
  const modelOptionsLoading = ref(false);
  const modelOptionsError = ref<string | null>(null);
  const apiKey = ref('');
  const toasts = useToasts();
  let replyTimer: ReturnType<typeof setTimeout> | undefined;
  let turnSeq = 0;
  let activeTurn = 0;
  let stopKillTimer: number | undefined;
  let stopFinalizeTimer: number | undefined;
  let historySaveTimer: number | undefined;
  let historyLoaded = false;
  let historyLoadStarted = false;
  let historySaveEnabled = true;
  let historySaveErrorShown = false;
  let resolveHistoryReady: () => void = () => {};
  const historyReadyPromise = new Promise<void>(resolve => {
    resolveHistoryReady = resolve;
  });
  const pendingSteers = new Map<string, string[]>();
  const queuedAfterTurn = new Map<string, string[]>();

  const active = computed(() => conversations.value.find(item => item.id === activeId.value) ?? null);
  const messages = computed(() => active.value?.messages ?? []);
  const history = computed(() =>
    [...conversations.value].filter(item => item.messages.length > 0).sort((a, b) => b.updatedAt - a.updatedAt)
  );
  const modelShort = computed(() => shortModel(model.value.model));
  const macActive = computed(() => mac.value.enabled && mac.value.level !== 'open');
  const macStatus = computed(() => (mac.value.enabled ? mac.value.level : 'paused'));
  const runtimeReady = computed(() => Boolean(runtime.value?.ready));
  const contextUsage = computed(() => active.value?.context ?? null);
  const auxiliaryCustomCount = computed(
    () =>
      HERMES_AUX_TASKS.filter(task => auxiliary.value[task].provider !== 'auto' && Boolean(auxiliary.value[task].model))
        .length
  );

  if (typeof window === 'undefined' || !('__TAURI_INTERNALS__' in window)) {
    historyLoaded = true;
    resolveHistoryReady();
  }

  function flushHistorySave() {
    if (!historyLoaded || !historySaveEnabled || typeof window === 'undefined' || !('__TAURI_INTERNALS__' in window))
      return;
    if (historySaveTimer) window.clearTimeout(historySaveTimer);
    const json = JSON.stringify({
      version: STORAGE_VERSION,
      conversations: conversations.value,
      activeId: activeId.value
    });
    void commands.hermesHistorySave(json).then(result => {
      if (result.status === 'error' && !historySaveErrorShown) {
        historySaveErrorShown = true;
        toasts.push({
          tone: 'warning',
          title: 'Hermes history',
          description: 'Could not write history.json; keeping the local copy.'
        });
      }
      if (result.status === 'ok') historySaveErrorShown = false;
    });
  }

  function scheduleHistorySave() {
    if (!historyLoaded || !historySaveEnabled || typeof window === 'undefined' || !('__TAURI_INTERNALS__' in window))
      return;
    if (historySaveTimer) window.clearTimeout(historySaveTimer);
    historySaveTimer = window.setTimeout(() => {
      const json = JSON.stringify({
        version: STORAGE_VERSION,
        conversations: conversations.value,
        activeId: activeId.value
      });
      void commands.hermesHistorySave(json).then(result => {
        if (result.status === 'error' && !historySaveErrorShown) {
          historySaveErrorShown = true;
          toasts.push({
            tone: 'warning',
            title: 'Hermes history',
            description: 'Could not write history.json; keeping the local copy.'
          });
        }
        if (result.status === 'ok') historySaveErrorShown = false;
      });
    }, 300);
  }

  function persist() {
    if (typeof localStorage === 'undefined') return;
    try {
      localStorage.setItem(
        STORAGE_KEY,
        JSON.stringify({
          version: STORAGE_VERSION,
          model: model.value,
          mac: mac.value,
          conversations: conversations.value,
          activeId: activeId.value,
          workingDir: workingDir.value,
          auxiliary: auxiliary.value,
          forceAuxVision: forceAuxVision.value
        } satisfies PersistedHermes)
      );
      scheduleHistorySave();
    } catch {
      // A full or unavailable localStorage must not break the assistant.
    }
  }

  watch([model, mac, conversations, activeId, workingDir, auxiliary, forceAuxVision], persist, { deep: true });

  async function loadRuntime() {
    try {
      runtime.value = await unwrap(commands.hermesRuntimeStatus());
    } catch (error) {
      runtime.value = {
        ready: false,
        installed: false,
        version: null,
        path: null,
        dataPath: null,
        computerUse: false,
        outdated: false,
        message: error instanceof IpcError ? error.message : String(error)
      };
    }
    if (runtime.value?.installed && !runtime.value.ready && runtime.value.message) {
      console.warn('Hermes runtime:', runtime.value.message);
      toasts.push({ tone: 'warning', title: 'Hermes runtime', description: runtime.value.message });
    }
  }

  async function installRuntime(): Promise<boolean> {
    if (installing.value) return false;
    installing.value = true;
    installProgress.value = { phase: 'preparing', downloaded: 0, total: null, percent: 0 };
    let unlisten: (() => void) | undefined;
    if (typeof window !== 'undefined' && '__TAURI_INTERNALS__' in window) {
      try {
        const { listen } = await import('@tauri-apps/api/event');
        unlisten = await listen<HermesInstallProgress>('hermes-install-progress', event => {
          installProgress.value = event.payload;
        });
      } catch {
        // Progress is an enhancement; the install command still completes.
      }
    }
    try {
      runtime.value = await unwrap(commands.hermesRuntimeInstall());
      toasts.push({ tone: 'success', title: 'Hermes', description: 'Hermes runtime installed.' });
      return runtime.value.ready;
    } catch (error) {
      const detail = error instanceof IpcError ? (error.detail ?? error.message) : String(error);
      const cancelled = /cancel/i.test(detail);
      toasts.push({ tone: cancelled ? 'info' : 'danger', title: 'Hermes install', description: detail });
      return false;
    } finally {
      unlisten?.();
      installing.value = false;
    }
  }

  async function cancelInstall() {
    try {
      await unwrap(commands.hermesRuntimeInstallCancel());
    } catch {
      // The install may already have finished.
    }
  }

  async function loadHistory() {
    if (historyLoadStarted) {
      await historyReadyPromise;
      return;
    }
    historyLoadStarted = true;
    if (typeof window === 'undefined' || !('__TAURI_INTERNALS__' in window)) {
      historyLoaded = true;
      historyReady.value = true;
      resolveHistoryReady();
      return;
    }
    let hasBackend = false;
    try {
      const raw = await unwrap(commands.hermesHistoryLoad());
      if (raw) {
        hasBackend = true;
        const parsed = JSON.parse(raw) as { conversations?: unknown; activeId?: unknown };
        if (!Array.isArray(parsed.conversations)) throw new Error('history conversations missing');
        const restored = parsed.conversations.map(conversation => {
          if (!conversation || typeof conversation !== 'object') throw new Error('invalid conversation');
          const entry = conversation as HermesConversation;
          if (typeof entry.id !== 'string' || !Array.isArray(entry.messages))
            throw new Error('invalid conversation shape');
          return {
            ...entry,
            messages: entry.messages.map(entryMessage => {
              if (
                !entryMessage ||
                typeof entryMessage !== 'object' ||
                typeof entryMessage.id !== 'string' ||
                (entryMessage.role !== 'user' && entryMessage.role !== 'assistant')
              ) {
                throw new Error('invalid message');
              }
              return { ...entryMessage, streaming: false };
            }),
            liveSessionId: undefined
          };
        });
        const localLatest = conversations.value.reduce((latest, item) => Math.max(latest, item.updatedAt || 0), 0);
        const backendLatest = restored.reduce((latest, item) => Math.max(latest, item.updatedAt || 0), 0);
        if (localLatest > backendLatest && conversations.value.length) {
          // Keep local unsaved work and write it back after load instead of
          // letting an older file erase the current conversation.
          hasBackend = false;
        } else {
          conversations.value = restored;
          activeId.value = typeof parsed.activeId === 'string' ? parsed.activeId : null;
        }
      }
    } catch {
      historySaveEnabled = false;
      toasts.push({
        tone: 'warning',
        title: 'Hermes history',
        description: 'history.json is unreadable; keeping the local copy and leaving the file untouched.'
      });
    } finally {
      historyLoaded = true;
      historyReady.value = true;
      if (!hasBackend && historySaveEnabled) scheduleHistorySave();
      resolveHistoryReady();
    }
  }

  async function uninstallRuntime(): Promise<boolean> {
    try {
      runtime.value = await unwrap(commands.hermesRuntimeUninstall());
      localStorage.removeItem(STORAGE_KEY);
      localStorage.removeItem(MODEL_OPTIONS_CACHE_KEY);
      modelOptions.value = null;
      modelOptionsUpdatedAt.value = null;
      conversations.value = [];
      activeId.value = null;
      model.value = { ...DEFAULT_MODEL };
      mac.value = { ...DEFAULT_MAC };
      toasts.push({ tone: 'success', title: 'Hermes', description: 'Hermes runtime and data removed.' });
      return true;
    } catch (error) {
      const detail = error instanceof IpcError ? (error.detail ?? error.message) : String(error);
      toasts.push({ tone: 'danger', title: 'Hermes uninstall', description: detail });
      return false;
    }
  }

  async function loadModelOptions(refresh = false, source: HermesModelSettings = model.value, keyOverride?: string) {
    if (!runtime.value?.ready || modelOptionsLoading.value) return;
    modelOptionsLoading.value = true;
    modelOptionsError.value = null;
    try {
      const request = unwrap(
        commands.hermesModelOptions({
          provider: source.provider,
          model: modelId(source.model),
          effort: source.effort,
          fast: source.fast,
          apiKey: (keyOverride ?? apiKey.value).trim() || null,
          endpoint: source.endpoint.trim() || null,
          refresh,
          includeUnconfigured: true
        })
      );
      const options = await Promise.race([
        request,
        new Promise<never>((_, reject) =>
          window.setTimeout(() => reject(new Error('加载官方模型目录超时，请稍后重试。')), 55_000)
        )
      ]);
      modelOptions.value = options;
      modelOptionsUpdatedAt.value = Date.now();
      try {
        localStorage.setItem(
          MODEL_OPTIONS_CACHE_KEY,
          JSON.stringify({ options, at: modelOptionsUpdatedAt.value } satisfies HermesModelOptionsCache)
        );
      } catch {
        // A full localStorage should not break the catalog.
      }
    } catch (error) {
      modelOptionsError.value = error instanceof IpcError ? (error.detail ?? error.message) : String(error);
    } finally {
      modelOptionsLoading.value = false;
    }
  }

  async function loadLoginStatus() {
    try {
      loginStatus.value = await unwrap(commands.hermesLoginStatus());
    } catch {
      loginStatus.value = null;
    }
  }

  function setAuxiliary(task: HermesAuxTask, nextProvider: string, nextModel: string) {
    auxiliary.value = {
      ...auxiliary.value,
      [task]: { provider: nextProvider, model: nextModel }
    };
  }

  function resetAuxiliary() {
    auxiliary.value = defaultAuxSettings();
    forceAuxVision.value = false;
  }

  function setForceAuxVision(value: boolean) {
    forceAuxVision.value = value;
  }

  function resetSession() {
    if (active.value) {
      active.value.sessionId = undefined;
      active.value.liveSessionId = undefined;
      pendingSteers.delete(active.value.id);
      queuedAfterTurn.delete(active.value.id);
    }
  }

  function setWorkingDir(value: string) {
    const changed = value !== workingDir.value;
    workingDir.value = value;
    if (changed) resetSession();
    persist();
  }

  function setApiKey(value: string) {
    apiKey.value = value;
  }

  function select(idValue: string) {
    if (conversations.value.some(item => item.id === idValue)) activeId.value = idValue;
  }

  function newChat() {
    activeId.value = null;
  }

  async function deleteConversation(idValue: string) {
    if (historyLoadStarted && !historyLoaded) await historyReadyPromise;
    const index = conversations.value.findIndex(item => item.id === idValue);
    if (index < 0) return;
    if (activeId.value === idValue) {
      stopThinking();
      activeId.value = null;
    }
    conversations.value = conversations.value.filter(item => item.id !== idValue);
    persist();
    flushHistorySave();
  }

  async function renameConversation(idValue: string, title: string) {
    if (historyLoadStarted && !historyLoaded) await historyReadyPromise;
    const target = conversations.value.find(item => item.id === idValue);
    if (!target) return;
    const next = title.trim();
    if (!next) return;
    target.title = next;
    target.updatedAt = Date.now();
    persist();
    flushHistorySave();
  }

  async function clearHistory() {
    if (historyLoadStarted && !historyLoaded) await historyReadyPromise;
    stopThinking();
    conversations.value = [];
    activeId.value = null;
    persist();
    flushHistorySave();
  }

  function exportHistory() {
    if (typeof document === 'undefined') return;
    const payload = JSON.stringify(
      { version: STORAGE_VERSION, exportedAt: new Date().toISOString(), conversations: conversations.value },
      null,
      2
    );
    const blob = new Blob([payload], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const link = document.createElement('a');
    link.href = url;
    link.download = `opennavo-hermes-${new Date().toISOString().slice(0, 10)}.json`;
    link.click();
    URL.revokeObjectURL(url);
  }

  function ensureConversation(): HermesConversation {
    if (active.value) return active.value;
    const conversation: HermesConversation = { id: id('c'), messages: [], updatedAt: Date.now() };
    conversations.value = [conversation, ...conversations.value];
    activeId.value = conversation.id;
    return conversation;
  }

  function scenarioFor(text: string): 'rectangle' | 'cleanup' | 'updates' | 'explain' | 'fallback' {
    if (/清理|磁盘|空间|缓存|clean|disk|storage|cache/i.test(text)) return 'cleanup';
    if (/更新|升级|update|upgrade/i.test(text)) return 'updates';
    if (/页面|这个|解释|做什么|page|explain|what can/i.test(text)) return 'explain';
    if (/窗口|分屏|管理|window|manager|snap/i.test(text)) return 'rectangle';
    return 'fallback';
  }

  function assistantFor(scenario: ReturnType<typeof scenarioFor>): HermesMessage {
    if (scenario === 'rectangle') {
      return message({
        role: 'assistant',
        textKey: 'messages.rectangle.assistant',
        tools: [
          { name: 'catalog.search', label: 'search', time: '0.8s' },
          { name: 'library.list', label: 'library', time: '0.4s' },
          { name: 'catalog.get', label: 'detail', time: '1.2s' }
        ],
        package: RECTANGLE,
        footKey: 'messages.rectangle.foot'
      });
    }
    if (scenario === 'cleanup') {
      return message({
        role: 'assistant',
        textKey: 'messages.cleanup.assistant',
        tools: [
          { name: 'cleanup.scan', label: 'scan', time: '1.4s' },
          { name: 'cleanup.classify', label: 'classify', time: '0.7s' }
        ],
        card: 'cleanup',
        footKey: 'messages.cleanup.foot'
      });
    }
    if (scenario === 'updates') {
      return message({
        role: 'assistant',
        textKey: 'messages.updates.assistant',
        tools: [
          { name: 'updates.check', label: 'updates', time: '0.9s' },
          { name: 'tasks.preview', label: 'preview', time: '0.5s' }
        ],
        card: 'updates',
        footKey: 'messages.updates.foot'
      });
    }
    if (scenario === 'explain') {
      return message({
        role: 'assistant',
        textKey: 'messages.explain.assistant',
        card: 'explain',
        footKey: 'messages.explain.foot'
      });
    }
    return message({ role: 'assistant', textKey: 'messages.fallback.assistant' });
  }

  async function flushSteers(conversation: HermesConversation) {
    const live = conversation.liveSessionId;
    if (!live) return;
    const queued = pendingSteers.get(conversation.id) ?? [];
    pendingSteers.delete(conversation.id);
    for (const text of queued) {
      try {
        await unwrap(commands.hermesSteer(live, text));
      } catch (error) {
        toasts.push({
          tone: 'warning',
          title: 'Hermes steer',
          description: error instanceof IpcError ? (error.detail ?? error.message) : String(error)
        });
      }
    }
  }

  async function send(text: string, attachments: string[] = []) {
    if (historyLoadStarted && !historyLoaded) {
      await historyReadyPromise;
    }
    const value = text.trim();
    if (!value && !attachments.length) return;
    if (thinking.value) {
      if (attachments.length) {
        toasts.push({ tone: 'warning', title: 'Hermes', description: '请等待当前回复结束再发送附件。' });
        return;
      }
      const steerConversation = ensureConversation();
      const steerMessage = message({ role: 'user', text: value, steering: true });
      const streamingIndex = steerConversation.messages.findLastIndex(
        entry => entry.role === 'assistant' && entry.streaming
      );
      if (streamingIndex >= 0) {
        steerConversation.messages.splice(streamingIndex, 0, steerMessage);
      } else {
        steerConversation.messages = [...steerConversation.messages, steerMessage];
      }
      steerConversation.updatedAt = Date.now();
      if (steerConversation.liveSessionId) {
        try {
          await unwrap(commands.hermesSteer(steerConversation.liveSessionId, value));
        } catch (error) {
          const detail = error instanceof IpcError ? (error.detail ?? error.message) : String(error);
          const index = steerConversation.messages.findIndex(entry => entry.id === steerMessage.id);
          if (index >= 0) steerConversation.messages.splice(index, 1);
          queuedAfterTurn.set(steerConversation.id, [...(queuedAfterTurn.get(steerConversation.id) ?? []), value]);
          toasts.push({
            tone: 'info',
            title: 'Hermes',
            description: detail.includes('does not support steer')
              ? '当前模型不支持中途引导，这条消息已排队到本轮结束后发送。'
              : detail
          });
          if (thinking.value) {
            stopRequested.value = true;
            stopping.value = true;
            armStopFallbacks(activeTurn, steerConversation.id);
            await commands.hermesInterrupt(steerConversation.liveSessionId).catch(() => undefined);
          }
        }
      } else {
        pendingSteers.set(steerConversation.id, [...(pendingSteers.get(steerConversation.id) ?? []), value]);
      }
      return;
    }
    const prompt = value || '请查看附件。';
    const conversation = ensureConversation();
    conversation.messages = [
      ...conversation.messages,
      message({ role: 'user', text: value, attachments: attachments.length ? [...attachments] : undefined })
    ];
    conversation.updatedAt = Date.now();
    thinking.value = true;
    const turn = ++turnSeq;
    activeTurn = turn;

    const tauri = typeof window !== 'undefined' && '__TAURI_INTERNALS__' in window;
    if (!runtime.value && tauri) await loadRuntime();
    if (!runtime.value?.ready && tauri) {
      thinking.value = false;
      toasts.push({
        tone: 'warning',
        title: 'Hermes runtime',
        description: 'Install the Hermes runtime before chatting.'
      });
      return;
    }
    if (runtime.value?.ready) {
      const requestId =
        typeof crypto !== 'undefined' && 'randomUUID' in crypto
          ? crypto.randomUUID()
          : `hermes-${Date.now()}-${Math.random().toString(36).slice(2)}`;
      const assistant = message({ role: 'assistant', text: '', streaming: true });
      conversation.messages = [...conversation.messages, assistant];
      conversation.updatedAt = Date.now();
      let unlisten: (() => void) | undefined;
      if (typeof window !== 'undefined' && '__TAURI_INTERNALS__' in window) {
        try {
          const { listen } = await import('@tauri-apps/api/event');
          const unlistenChunk = await listen<HermesChunk>('hermes-chunk', event => {
            if (event.payload.requestId !== requestId) return;
            const target = conversations.value.find(item => item.id === conversation.id);
            const entry = target?.messages.find(item => item.id === assistant.id);
            if (!target || !entry || !entry.streaming) return;
            entry.text = `${entry.text ?? ''}${event.payload.delta}`;
            target.updatedAt = Date.now();
          });
          const unlistenSession = await listen<HermesSessionEvent>('hermes-session', event => {
            if (event.payload.requestId !== requestId) return;
            const target = conversations.value.find(item => item.id === conversation.id);
            if (!target) return;
            target.liveSessionId = event.payload.liveSessionId;
            void flushSteers(target);
          });
          const unlistenApproval = await listen<HermesApprovalRequest>('hermes-approval', event => {
            approvalRequest.value = event.payload;
          });
          const unlistenTool = await listen<HermesToolEvent>('hermes-tool', event => {
            if (event.payload.requestId !== requestId) return;
            const target = conversations.value.find(item => item.id === conversation.id);
            const entry = target?.messages.find(item => item.id === assistant.id);
            if (!entry) return;
            const calls = [...(entry.tools ?? [])];
            const existing = event.payload.toolId
              ? calls.find(call => call.toolId === event.payload.toolId)
              : [...calls].reverse().find(call => call.name === event.payload.name && call.time === '…');
            if (event.payload.phase === 'start') {
              if (!existing) {
                calls.push({
                  name: event.payload.name,
                  label: event.payload.label || event.payload.name,
                  time: '…',
                  toolId: event.payload.toolId
                });
              }
            } else if (existing) {
              existing.label = event.payload.label || existing.label;
              existing.time = `${event.payload.duration?.toFixed(1) ?? '0.0'}s`;
            } else {
              calls.push({
                name: event.payload.name,
                label: event.payload.label || event.payload.name,
                time: `${event.payload.duration?.toFixed(1) ?? '0.0'}s`,
                toolId: event.payload.toolId
              });
            }
            entry.tools = calls;
          });
          const unlistenReasoning = await listen<{ requestId: string; delta: string }>('hermes-reasoning', event => {
            if (event.payload.requestId !== requestId) return;
            const target = conversations.value.find(item => item.id === conversation.id);
            const entry = target?.messages.find(item => item.id === assistant.id);
            if (entry) entry.reasoning = `${entry.reasoning ?? ''}${event.payload.delta}`;
          });
          unlisten = () => {
            unlistenChunk();
            unlistenSession();
            unlistenApproval();
            unlistenTool();
            unlistenReasoning();
          };
        } catch {
          // Streaming is an enhancement; the final IPC reply still renders.
        }
      }
      try {
        const reply = await unwrap(
          commands.hermesChat({
            prompt,
            requestId,
            sessionId: conversation.sessionId ?? null,
            provider: model.value.provider,
            model: modelId(model.value.model),
            effort: model.value.effort,
            fast: model.value.fast,
            apiKey: apiKey.value.trim() || null,
            endpoint: model.value.endpoint.trim() || null,
            cwd: workingDir.value.trim() || null,
            attachments: [...attachments],
            auxiliary: HERMES_AUX_TASKS.map(task => ({ task, ...auxiliary.value[task] })),
            forceAuxVision: forceAuxVision.value,
            macEnabled: mac.value.enabled,
            controlLevel: mac.value.level,
            macTerminal: mac.value.terminal,
            macScreen: mac.value.screen,
            macFiles: mac.value.files,
            macCron: mac.value.cron,
            confirmWrites: mac.value.confirmWrites
          })
        );
        if (activeTurn !== turn) return;
        const target = conversations.value.find(item => item.id === conversation.id);
        const entry = target?.messages.find(item => item.id === assistant.id);
        if (target && entry) {
          entry.text = reply.text || entry.text || 'Hermes returned an empty response.';
          entry.streaming = false;
          if (reply.sessionId) target.sessionId = reply.sessionId;
          if (reply.liveSessionId) target.liveSessionId = reply.liveSessionId;
          if (reply.usage?.context_used && reply.usage.context_max) {
            const used = reply.usage.context_used;
            const max = reply.usage.context_max;
            target.context = {
              used,
              max,
              percent: reply.usage.context_percent ?? Math.round((used / max) * 100)
            };
          }
          target.updatedAt = Date.now();
        }
      } catch (error) {
        if (activeTurn !== turn) return;
        if (stopping.value || stopRequested.value) {
          const target = conversations.value.find(item => item.id === conversation.id);
          const entry = target?.messages.find(item => item.id === assistant.id);
          if (target && entry) {
            entry.text = entry.text || '已停止。';
            entry.streaming = false;
            target.updatedAt = Date.now();
          }
          return;
        }
        const detail = error instanceof IpcError ? (error.detail ?? error.message) : String(error);
        const expiredOpenCode =
          (model.value.provider === 'opencode-go' || model.value.provider === 'opencode') &&
          /api key has expired|401/i.test(detail);
        const messageText = expiredOpenCode
          ? `${detail}

OpenCode Go 的 API Key 已过期。请在 [OpenCode Console](https://opencode.ai/console) 创建新的 API Key，然后在 Hermes 设置 → OpenCode Go 中填写。`
          : detail;
        const target = conversations.value.find(item => item.id === conversation.id);
        const entry = target?.messages.find(item => item.id === assistant.id);
        if (target && entry) {
          entry.text = entry.text || messageText;
          entry.streaming = false;
          target.updatedAt = Date.now();
        }
        toasts.push({
          tone: 'danger',
          title: expiredOpenCode ? 'OpenCode API Key' : 'Hermes runtime',
          description: expiredOpenCode ? 'OpenCode Go Key 已过期，请创建新的 API Key。' : detail
        });
      } finally {
        unlisten?.();
        if (activeTurn === turn) {
          clearStopTimers();
          thinking.value = false;
          stopping.value = false;
          stopRequested.value = false;
          approvalRequest.value = null;
          const queued = queuedAfterTurn.get(conversation.id);
          if (queued?.length) {
            const [next, ...rest] = queued;
            if (rest.length) queuedAfterTurn.set(conversation.id, rest);
            else queuedAfterTurn.delete(conversation.id);
            if (next) void send(next);
          }
        }
      }
      return;
    }

    const reply = assistantFor(scenarioFor(value));
    replyTimer = setTimeout(() => {
      const target = conversations.value.find(item => item.id === conversation.id);
      if (target) {
        target.messages = [...target.messages, reply];
        target.updatedAt = Date.now();
      }
      thinking.value = false;
      stopping.value = false;
      stopRequested.value = false;
      clearStopTimers();
      replyTimer = undefined;
    }, 520);
  }

  function clearStopTimers() {
    if (stopKillTimer) clearTimeout(stopKillTimer);
    if (stopFinalizeTimer) clearTimeout(stopFinalizeTimer);
    stopKillTimer = undefined;
    stopFinalizeTimer = undefined;
  }

  function stopThinking() {
    if (replyTimer) clearTimeout(replyTimer);
    replyTimer = undefined;
    clearStopTimers();
    activeTurn = ++turnSeq;
    thinking.value = false;
    stopping.value = false;
    stopRequested.value = false;
    approvalRequest.value = null;
    if (active.value) {
      active.value.liveSessionId = undefined;
      pendingSteers.delete(active.value.id);
      queuedAfterTurn.delete(active.value.id);
    }
    const last = messages.value.at(-1);
    if (last?.streaming) {
      last.streaming = false;
      if (!last.text) last.text = '已停止。';
    }
    if (runtime.value?.ready) void commands.hermesStop().catch(() => undefined);
  }

  async function respondApproval(choice: 'once' | 'session' | 'always' | 'deny') {
    if (!approvalRequest.value) return;
    try {
      await unwrap(commands.hermesApprovalRespond(choice));
    } catch (error) {
      toasts.push({
        tone: 'warning',
        title: 'Hermes approval',
        description: error instanceof IpcError ? (error.detail ?? error.message) : String(error)
      });
    } finally {
      approvalRequest.value = null;
    }
  }

  function armStopFallbacks(turn: number, conversationId: string | undefined) {
    // Arm the fallbacks before awaiting IPC: a hung interrupt must not disable them.
    clearStopTimers();
    stopKillTimer = window.setTimeout(() => {
      if (activeTurn !== turn || !thinking.value || !stopping.value) return;
      void commands.hermesStop().catch(() => undefined);
    }, 1200);
    stopFinalizeTimer = window.setTimeout(() => {
      if (activeTurn !== turn || !thinking.value) return;
      if (conversationId && active.value?.id !== conversationId) return;
      stopThinking();
    }, 10_000);
  }

  async function stop() {
    if (!thinking.value) return;
    if (stopping.value) {
      // A steer failure can mark the turn as stopping before this button is pressed.
      armStopFallbacks(activeTurn, active.value?.id);
      return;
    }
    const turn = activeTurn;
    const stoppedConversation = active.value?.id;
    approvalRequest.value = null;
    stopRequested.value = true;
    stopping.value = true;
    armStopFallbacks(turn, stoppedConversation);
    const live = active.value?.liveSessionId;
    try {
      if (live) await unwrap(commands.hermesInterrupt(live));
      else await unwrap(commands.hermesStop());
    } catch {
      await commands.hermesStop().catch(() => undefined);
    }
  }

  function saveModel(next: HermesModelSettings) {
    const previous = model.value;
    const changed =
      previous.provider !== next.provider ||
      previous.model !== next.model ||
      previous.effort !== next.effort ||
      previous.fast !== next.fast ||
      previous.endpoint !== next.endpoint;
    model.value = { ...next };
    if (changed) resetSession();
  }

  function saveMac(next: HermesMacSettings) {
    mac.value = { ...next };
  }

  function useProvider(provider: HermesProvider) {
    const info = HERMES_PROVIDER_MAP[provider];
    resetSession();
    model.value = {
      ...model.value,
      provider,
      model: info.models[0] ?? model.value.model,
      endpoint: defaultProviderEndpoint(provider),
      keySet: info.oauth
    };
  }

  function setEffort(effort: HermesEffort) {
    model.value = { ...model.value, effort };
    resetSession();
  }

  function stopMac() {
    mac.value = { ...mac.value, enabled: false };
    stopThinking();
  }

  // Persist the v3 migration immediately so an old full-control record cannot
  // come back merely because nothing else changed this launch.
  if (typeof localStorage !== 'undefined') persist();

  return {
    model,
    mac,
    conversations,
    activeId,
    active,
    messages,
    history,
    thinking,
    historyReady,
    approvalRequest,
    respondApproval,
    stopping,
    stop,
    runtime,
    runtimeReady,
    contextUsage,
    auxiliary,
    forceAuxVision,
    auxiliaryCustomCount,
    setAuxiliary,
    setForceAuxVision,
    resetAuxiliary,
    installing,
    installProgress,
    installRuntime,
    cancelInstall,
    uninstallRuntime,
    loginStatus,
    workingDir,
    setWorkingDir,
    loadLoginStatus,
    modelOptions,
    modelOptionsLoading,
    modelOptionsError,
    modelOptionsUpdatedAt,
    loadModelOptions,
    apiKey,
    loadRuntime,
    loadHistory,
    setApiKey,
    modelShort,
    macActive,
    macStatus,
    providers: providerModels,
    select,
    newChat,
    deleteConversation,
    renameConversation,
    clearHistory,
    exportHistory,
    send,
    stopThinking,
    saveModel,
    saveMac,
    resetSession,
    useProvider,
    setEffort,
    stopMac
  };
});
