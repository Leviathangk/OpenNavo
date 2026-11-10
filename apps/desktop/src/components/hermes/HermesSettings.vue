<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { OnButton, OnIcon, OnModal, OnToggle } from '@opennavo/ui';
import { commands, unwrap, IpcError } from '@/ipc/client';
import { openExternal } from '@/utils/external';
import { useHermesStore } from '@/stores/hermes';
import { useHermesContent } from '@/composables/useHermesContent';
import { useToasts } from '@/composables/useToasts';
import {
  HERMES_EFFORTS,
  HERMES_PROVIDERS,
  HERMES_PROVIDER_MAP,
  defaultProviderEndpoint,
  hermesProviderSlug,
  HERMES_AUX_TASKS,
  type HermesAuxTask,
  providerModels
} from '@/hermes/providers';
import type { HermesProvider } from '@/hermes/providers';
import type { HermesMacSettings, HermesModelSettings } from '@/stores/hermes';
import type { HermesAuditEntry, HermesPermissions } from '@/ipc/bindings';
import type { HermesSettingsTab } from '@/stores/hermesUi';

const props = defineProps<{ open: boolean; tab: HermesSettingsTab }>();
const emit = defineEmits<{ close: [] }>();

const store = useHermesStore();
const content = useHermesContent();
const toasts = useToasts();

const activeTab = ref<HermesSettingsTab>(props.tab);
const draftModel = ref<HermesModelSettings>({ ...store.model });
const draftMac = ref<HermesMacSettings>({ ...store.mac });
const secret = ref('');
const uninstallOpen = ref(false);
const modelOpen = ref(false);
const auxOpen = ref(false);
const testing = ref(false);
const permissions = ref<HermesPermissions | null>(null);
const auditOpen = ref(false);
const auditEntries = ref<HermesAuditEntry[]>([]);
const auditLoading = ref(false);
const cliKind = ref('tools');
const cliOutput = ref('');
const cliLoading = ref(false);
const cronJobId = ref('');
const cronBusy = ref(false);
const skillName = ref('');
const toolName = ref('');
const actionBusy = ref(false);
const mcpName = ref('');
const mcpUrl = ref('');
const mcpBusy = ref(false);
const testMessage = ref('');
const testOk = ref<boolean | null>(null);
const modelPicker = ref<HTMLElement>();
const LEVELS = ['open', 'guarded', 'full'] as const;
const MAC_CAPABILITIES = ['terminal', 'screen', 'files', 'cron'] as const;

const provider = computed(() => HERMES_PROVIDER_MAP[draftModel.value.provider]);
const providerCopy = computed(() => content.value.providers[draftModel.value.provider]);
const localAuthDetected = computed(() => {
  const key = provider.value.localAuth;
  const status = store.loginStatus;
  if (!key || !status) return false;
  return key === 'opencode-go' ? status.opencodeGo : status.opencode;
});

const loginDetected = computed(() => {
  const key = provider.value.login;
  const status = store.loginStatus;
  if (!key || !status) return false;
  return key === 'claude-code' ? status.claudeCode : status[key];
});
const officialProvider = computed(() => {
  const slug = hermesProviderSlug(draftModel.value.provider);
  if (!slug) return undefined;
  return store.modelOptions?.providers?.find(item => item.slug === slug);
});
const officialAuthenticated = computed(() => officialProvider.value?.authenticated ?? false);
const models = computed(() => {
  const official = officialProvider.value?.models ?? [];
  return official.length ? official : providerModels(draftModel.value.provider);
});
const needsImport = computed(() => loginDetected.value && !officialAuthenticated.value);
const modelOptionsUpdated = computed(() => {
  const at = store.modelOptionsUpdatedAt;
  if (!at) return '';
  const minutes = Math.max(0, Math.round((Date.now() - at) / 60_000));
  if (minutes < 1) return content.value.settings.model.updatedNow;
  if (minutes < 60) return content.value.settings.model.updatedMinutes.replace('{count}', String(minutes));
  return content.value.settings.model.updatedHours.replace('{count}', String(Math.round(minutes / 60)));
});
const officialWarning = computed(() => {
  if (!officialProvider.value?.models?.length) return content.value.settings.model.fallbackModels;
  if (provider.value.login && needsImport.value) return '';
  if (provider.value.localAuth && (draftModel.value.keySet || localAuthDetected.value)) return '';
  return officialProvider.value?.warning ?? '';
});
const selectedCapability = computed(() => officialProvider.value?.capabilities?.[draftModel.value.model]);
const reasoningSupported = computed(() => selectedCapability.value?.reasoning !== false);
const fastSupported = computed(() => selectedCapability.value?.fast === true);
const selectedPricing = computed(() => officialProvider.value?.pricing?.[draftModel.value.model]);
async function refreshModels() {
  await store.loadModelOptions(true, draftModel.value, secret.value);
}
function selectModel(model: string) {
  draftModel.value.model = model;
  modelOpen.value = false;
}

function onModelPickerPointer(event: PointerEvent) {
  if (!modelOpen.value) return;
  const target = event.target as Node | null;
  if (target && modelPicker.value?.contains(target)) return;
  modelOpen.value = false;
}

function alignOfficialModel() {
  if (models.value.length && !models.value.includes(draftModel.value.model))
    draftModel.value.model = models.value[0] ?? draftModel.value.model;
}
const warning = computed(() => {
  if (draftMac.value.level === 'open') {
    return { title: content.value.common.open, text: content.value.settings.mac.warningOpen };
  }
  if (draftMac.value.level === 'guarded') {
    return { title: content.value.common.guarded, text: content.value.settings.mac.warningGuarded };
  }
  return { title: content.value.common.full, text: content.value.settings.mac.warningFull };
});

// Terminal and screen control run as the user and can read Hermes' own OAuth /
// session store even though the provider key itself is never written to disk.
const terminalCredentialWarning = computed(
  () => draftMac.value.enabled && draftMac.value.level !== 'open' && (draftMac.value.terminal || draftMac.value.screen)
);

const auxProviders = computed(() =>
  (store.modelOptions?.providers ?? []).filter(entry => (entry.models?.length ?? 0) > 0)
);
const auxModelCount = computed(() =>
  auxProviders.value.reduce((count, entry) => count + (entry.models?.length ?? 0), 0)
);
const visionAuxAuto = computed(() => store.auxiliary.vision.provider === 'auto' || !store.auxiliary.vision.model);
const auxSummary = computed(() => {
  const custom = store.auxiliaryCustomCount;
  if (!custom) return content.value.settings.model.auxAllAuto;
  return content.value.settings.model.auxCustom
    .replace('{count}', String(custom))
    .replace('{auto}', String(HERMES_AUX_TASKS.length - custom));
});
function openAux() {
  auxOpen.value = true;
  if (!auxProviders.value.length) void store.loadModelOptions(false, draftModel.value, secret.value);
}

function auxValue(task: HermesAuxTask) {
  const entry = store.auxiliary[task];
  return entry.provider === 'auto' || !entry.model ? 'auto' : `${entry.provider}::${entry.model}`;
}
function onAuxChange(task: HermesAuxTask, event: Event) {
  const value = (event.target as HTMLSelectElement).value;
  if (value === 'auto') {
    store.setAuxiliary(task, 'auto', '');
    return;
  }
  const separator = value.indexOf('::');
  if (separator < 0) return;
  store.setAuxiliary(task, value.slice(0, separator), value.slice(separator + 2));
}

function resetTest() {
  testMessage.value = '';
  testOk.value = null;
}

async function refreshLogin() {
  await store.loadLoginStatus();
}

interface HermesLoginEvent {
  provider: string;
  kind: 'started' | 'line' | 'url' | 'code' | 'done' | 'cancelled';
  line?: string | null;
  url?: string | null;
  code?: string | null;
  ok?: boolean | null;
  message?: string | null;
}

const loginOpen = ref(false);
const loginProvider = ref('');
const loginPhase = ref<'starting' | 'waiting' | 'success' | 'error' | 'cancelled'>('starting');
const loginLines = ref<string[]>([]);
const loginUrl = ref('');
const loginCode = ref('');
const loginInput = ref('');
const loginUnlisten = ref<(() => void) | null>(null);
const loginName = computed(
  () => content.value.providers[loginProvider.value as HermesProvider]?.name ?? loginProvider.value
);
const loginStatusText = computed(() => {
  if (loginPhase.value === 'success') return content.value.settings.model.loginSuccess;
  if (loginPhase.value === 'error') return content.value.settings.model.loginFailed;
  if (loginPhase.value === 'cancelled') return content.value.settings.model.loginCancelled;
  if (loginPhase.value === 'waiting') return content.value.settings.model.loginWaiting;
  return content.value.settings.model.loginStarting;
});

function appendLoginLine(line: string) {
  if (!line) return;
  loginLines.value = [...loginLines.value.slice(-39), line];
}

async function ensureLoginListener() {
  if (loginUnlisten.value || !(typeof window !== 'undefined' && '__TAURI_INTERNALS__' in window)) return;
  const { listen } = await import('@tauri-apps/api/event');
  loginUnlisten.value = await listen<HermesLoginEvent>('hermes-login', event => {
    const payload = event.payload;
    if (payload.provider !== loginProvider.value) return;
    if (payload.line) appendLoginLine(payload.line);
    if (payload.url) loginUrl.value = payload.url;
    if (payload.code) loginCode.value = payload.code;
    if (payload.kind === 'started') loginPhase.value = 'starting';
    if (payload.kind === 'line' || payload.kind === 'url' || payload.kind === 'code') loginPhase.value = 'waiting';
    if (payload.kind === 'cancelled') loginPhase.value = 'cancelled';
    if (payload.kind === 'done') {
      if (payload.ok) {
        loginPhase.value = 'success';
        void refreshLogin();
        store.resetSession();
        void store.loadModelOptions(true, draftModel.value, secret.value).then(alignOfficialModel);
      } else {
        loginPhase.value = 'error';
        if (payload.message) appendLoginLine(payload.message);
      }
    }
  });
}

async function openLogin() {
  const key = provider.value.login;
  if (!key) return;
  if (loginOpen.value && (loginPhase.value === 'starting' || loginPhase.value === 'waiting')) return;
  loginProvider.value = key;
  loginOpen.value = true;
  loginPhase.value = 'starting';
  loginLines.value = [];
  loginUrl.value = '';
  loginCode.value = '';
  loginInput.value = '';
  try {
    await ensureLoginListener();
    await unwrap(commands.hermesLoginStart(key));
    if (loginPhase.value === 'starting') loginPhase.value = 'waiting';
  } catch (error) {
    loginPhase.value = 'error';
    appendLoginLine(error instanceof IpcError ? (error.detail ?? error.message) : String(error));
  }
}

async function logout() {
  const key = provider.value.login;
  if (!key) return;
  try {
    await unwrap(commands.hermesLogout(key));
    await refreshLogin();
    store.resetSession();
    await store.loadModelOptions(true, draftModel.value, secret.value);
    alignOfficialModel();
    toasts.push({ tone: 'success', title: content.value.settings.model.logout, description: providerCopy.value.name });
  } catch (error) {
    const detail = error instanceof IpcError ? (error.detail ?? error.message) : String(error);
    toasts.push({ tone: 'warning', title: content.value.settings.model.logout, description: detail });
  }
}

async function cancelLogin() {
  try {
    await unwrap(commands.hermesLoginCancel());
  } catch {
    // The process may have already exited.
  }
  loginPhase.value = 'cancelled';
  loginOpen.value = false;
}

async function sendLoginInput() {
  const value = loginInput.value;
  if (!value.trim()) return;
  try {
    await unwrap(commands.hermesLoginInput(value));
    loginInput.value = '';
  } catch (error) {
    appendLoginLine(error instanceof IpcError ? (error.detail ?? error.message) : String(error));
  }
}

async function copyLoginCode() {
  if (!loginCode.value) return;
  await navigator.clipboard.writeText(loginCode.value);
}

async function refreshSecret() {
  if (provider.value.login) {
    draftModel.value.keySet = false;
    await refreshLogin();
    return;
  }
  if (provider.value.localAuth) {
    await refreshLogin();
  }
  if (draftModel.value.keySet) {
    return;
  }
  if (provider.value.oauth) {
    draftModel.value.keySet = false;
    return;
  }
  try {
    draftModel.value.keySet = await unwrap(commands.hermesSecretStatus(draftModel.value.provider));
  } catch {
    // A locked or unavailable Keychain must not block the settings sheet.
  }
}

watch(
  () => props.open,
  open => {
    if (!open) return;
    activeTab.value = props.tab;
    draftModel.value = { ...store.model };
    draftMac.value = { ...store.mac };
    secret.value = '';
    modelOpen.value = false;
    auxOpen.value = false;
    resetTest();
    void refreshSecret();
    void refreshPermissions();
    if (store.modelOptions) alignOfficialModel();
    else if (!store.modelOptionsLoading) {
      void store.loadModelOptions(false, draftModel.value, secret.value).then(alignOfficialModel);
    }
  }
);
watch(
  () => props.tab,
  tab => {
    if (props.open) {
      activeTab.value = tab;
      if (tab === 'mac') void refreshPermissions();
    }
  }
);

async function selectProvider(id: HermesProvider) {
  const info = HERMES_PROVIDER_MAP[id];
  draftModel.value = {
    ...draftModel.value,
    provider: id,
    model: info.models[0] ?? draftModel.value.model,
    endpoint: defaultProviderEndpoint(id),
    keySet: info.oauth
  };
  secret.value = '';
  modelOpen.value = false;
  resetTest();
  await refreshSecret();
  const slug = hermesProviderSlug(id);
  const hasCachedModels = store.modelOptions?.providers?.some(
    entry => entry.slug === slug && (entry.models?.length ?? 0) > 0
  );
  if (!hasCachedModels) await store.loadModelOptions(false, draftModel.value, secret.value);
  alignOfficialModel();
}

function toggleMaster(enabled: boolean) {
  draftMac.value.enabled = enabled;
  // Turning Mac control on is an explicit grant: enable every capability in one
  // step. Individual switches below can still be turned off afterwards.
  if (enabled) {
    for (const key of MAC_CAPABILITIES) draftMac.value[key] = true;
    if (draftMac.value.level === 'open') {
      draftMac.value.level = 'guarded';
      draftMac.value.confirmWrites = true;
    }
  }
}

function selectLevel(level: HermesMacSettings['level']) {
  draftMac.value.level = level;
  if (level === 'guarded') draftMac.value.confirmWrites = true;
  if (level === 'full') draftMac.value.confirmWrites = false;
}

async function save() {
  const entered = secret.value.trim();
  if (entered) {
    try {
      await unwrap(commands.hermesSecretSet(draftModel.value.provider, entered));
      draftModel.value.keySet = true;
    } catch (error) {
      const detail = error instanceof IpcError ? (error.detail ?? error.message) : String(error);
      toasts.push({ tone: 'warning', title: content.value.settings.model.keyError, description: detail });
    }
  }
  store.setApiKey(secret.value);
  const previous = store.model;
  const sessionWillReset =
    Boolean(store.activeId) &&
    (previous.provider !== draftModel.value.provider ||
      previous.model !== draftModel.value.model ||
      previous.effort !== draftModel.value.effort ||
      previous.fast !== draftModel.value.fast ||
      previous.endpoint !== draftModel.value.endpoint ||
      Boolean(entered));
  if (entered) store.resetSession();
  store.saveModel({ ...draftModel.value, keySet: draftModel.value.keySet || Boolean(entered) });
  store.saveMac({ ...draftMac.value });
  toasts.push({
    tone: 'success',
    title: content.value.common.settingsSaved,
    description: `${store.modelShort} · ${content.value.effort[store.model.effort]}`
  });
  if (sessionWillReset) {
    toasts.push({
      tone: 'info',
      title: content.value.settings.model.modelLabel,
      description: content.value.settings.model.sessionReset
    });
  }
  emit('close');
}

async function removeApiKey() {
  try {
    await unwrap(commands.hermesSecretDelete(draftModel.value.provider));
    draftModel.value.keySet = false;
    secret.value = '';
    resetTest();
    toasts.push({ tone: 'success', title: content.value.settings.model.keyRemoved });
  } catch (error) {
    const detail = error instanceof IpcError ? (error.detail ?? error.message) : String(error);
    toasts.push({ tone: 'warning', title: content.value.settings.model.removeKey, description: detail });
  }
}

async function testConnection() {
  if (testing.value) return;
  testing.value = true;
  testMessage.value = '';
  testOk.value = null;
  try {
    const result = await unwrap(
      commands.hermesTestConnection({
        provider: draftModel.value.provider,
        apiKey: secret.value.trim() || null,
        endpoint: draftModel.value.endpoint.trim() || null
      })
    );
    testOk.value = result.ok;
    testMessage.value = result.message;
  } catch (error) {
    testOk.value = false;
    testMessage.value = error instanceof IpcError ? (error.detail ?? error.message) : String(error);
  } finally {
    testing.value = false;
  }
}

function stopMac() {
  store.stopMac();
  draftMac.value.enabled = false;
  toasts.push({ tone: 'warning', title: content.value.common.stopped, description: content.value.common.stoppedBody });
}

async function requestScreenPermission() {
  try {
    await commands.hermesPermissionsRequestScreen();
    await refreshPermissions();
  } catch {
    // The system may deny without surfacing an error.
  }
}

async function refreshPermissions() {
  try {
    permissions.value = await unwrap(commands.hermesPermissionsStatus());
  } catch {
    permissions.value = null;
  }
}

function permissionText(value: boolean | undefined) {
  if (value === undefined) return content.value.settings.mac.unknown;
  return value ? content.value.settings.mac.granted : content.value.settings.mac.notGranted;
}

function permissionDot(value: boolean | undefined) {
  if (value === undefined) return 'bg-status-warning';
  return value ? 'bg-status-success' : 'bg-status-warning';
}

function openPermission(pane: string) {
  void openExternal(`x-apple.systempreferences:com.apple.preference.security?${pane}`);
}

function checkPermissions() {
  void openExternal('x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility');
  toasts.push({
    tone: 'info',
    title: content.value.settings.mac.rechecked,
    description: content.value.settings.mac.recheckedBody
  });
}

async function updateRuntime() {
  const ok = await store.installRuntime();
  if (ok) {
    toasts.push({ tone: 'success', title: content.value.install.complete, description: store.runtime?.version ?? '' });
  }
}

async function uninstallHermes() {
  const ok = await store.uninstallRuntime();
  uninstallOpen.value = false;
  if (ok) emit('close');
}

async function loadCli(kind: string) {
  cliKind.value = kind;
  cliLoading.value = true;
  try {
    cliOutput.value = await unwrap(commands.hermesCliRead(kind));
  } catch (error) {
    const detail = error instanceof IpcError ? (error.detail ?? error.message) : String(error);
    cliOutput.value = detail;
    toasts.push({ tone: 'warning', title: content.value.settings.diagnostics.title, description: detail });
  } finally {
    cliLoading.value = false;
  }
}

async function runCronAction(action: 'pause' | 'resume' | 'remove') {
  if (!cronJobId.value.trim() || cronBusy.value) return;
  if (action === 'remove' && !window.confirm(content.value.settings.diagnostics.cronConfirm)) return;
  cronBusy.value = true;
  try {
    await unwrap(commands.hermesCronAction(action, cronJobId.value.trim()));
    await loadCli('cron');
  } catch (error) {
    const detail = error instanceof IpcError ? (error.detail ?? error.message) : String(error);
    toasts.push({ tone: 'warning', title: content.value.settings.diagnostics.cron, description: detail });
  } finally {
    cronBusy.value = false;
  }
}

async function runMcp(action: 'list' | 'add' | 'remove') {
  if (action !== 'list' && !mcpName.value.trim()) return;
  if (action === 'remove' && !window.confirm(content.value.settings.diagnostics.mcpRemoveConfirm)) return;
  mcpBusy.value = true;
  try {
    const output = await unwrap(commands.hermesMcpAction(action, mcpName.value.trim(), mcpUrl.value.trim()));
    if (action === 'list') cliOutput.value = output;
    else await loadCli('tools');
  } catch (error) {
    const detail = error instanceof IpcError ? (error.detail ?? error.message) : String(error);
    toasts.push({ tone: 'warning', title: 'MCP', description: detail });
  } finally {
    mcpBusy.value = false;
  }
}

async function runSkillAction(action: 'install' | 'uninstall') {
  if (!skillName.value.trim() || actionBusy.value) return;
  if (action === 'install' && !window.confirm(content.value.settings.diagnostics.installConfirm)) return;
  if (action === 'uninstall' && !window.confirm(content.value.settings.diagnostics.uninstallConfirm)) return;
  actionBusy.value = true;
  try {
    await unwrap(commands.hermesSkillAction(action, skillName.value.trim()));
    await loadCli('skills');
  } catch (error) {
    const detail = error instanceof IpcError ? (error.detail ?? error.message) : String(error);
    toasts.push({ tone: 'warning', title: content.value.settings.diagnostics.skills, description: detail });
  } finally {
    actionBusy.value = false;
  }
}

async function runToolAction(action: 'enable' | 'disable') {
  if (!toolName.value.trim() || actionBusy.value) return;
  if (action === 'disable' && !window.confirm(content.value.settings.diagnostics.disableConfirm)) return;
  actionBusy.value = true;
  try {
    await unwrap(commands.hermesToolAction(action, toolName.value.trim()));
    await loadCli('tools');
  } catch (error) {
    const detail = error instanceof IpcError ? (error.detail ?? error.message) : String(error);
    toasts.push({ tone: 'warning', title: content.value.settings.diagnostics.tools, description: detail });
  } finally {
    actionBusy.value = false;
  }
}

async function openAudit() {
  auditOpen.value = true;
  auditLoading.value = true;
  try {
    auditEntries.value = await unwrap(commands.hermesAuditRead(100));
  } catch (error) {
    const detail = error instanceof IpcError ? (error.detail ?? error.message) : String(error);
    toasts.push({ tone: 'warning', title: content.value.settings.mac.audit, description: detail });
  } finally {
    auditLoading.value = false;
  }
}

async function openLogs() {
  try {
    await unwrap(commands.hermesLogsOpen());
  } catch (error) {
    const detail = error instanceof IpcError ? (error.detail ?? error.message) : String(error);
    toasts.push({ tone: 'warning', title: content.value.settings.mac.openLogs, description: detail });
  }
}

async function openAuditFile() {
  try {
    await unwrap(commands.hermesAuditOpen());
  } catch (error) {
    const detail = error instanceof IpcError ? (error.detail ?? error.message) : String(error);
    toasts.push({ tone: 'warning', title: content.value.settings.mac.audit, description: detail });
  }
}

function onKeydown(event: KeyboardEvent) {
  if (props.open && event.key === 'Escape') emit('close');
}
onMounted(() => {
  document.addEventListener('keydown', onKeydown);
  document.addEventListener('pointerdown', onModelPickerPointer);
});
onBeforeUnmount(() => {
  document.removeEventListener('keydown', onKeydown);
  document.removeEventListener('pointerdown', onModelPickerPointer);
  loginUnlisten.value?.();
});
</script>

<template>
  <Teleport to="body">
    <div v-if="open" class="fixed inset-0 z-600 flex items-center justify-center p-24px" @click.self="emit('close')">
      <div class="absolute inset-0 bg-black/60 backdrop-blur-[3px]" aria-hidden="true" @click="emit('close')"></div>
      <div
        class="relative z-1 flex max-h-[calc(100vh-48px)] w-780px max-w-full flex-col overflow-hidden rounded-huge border border-solid border-line-strong bg-component-modal-bg shadow-modal"
        role="dialog"
        aria-modal="true"
        :aria-label="content.settings.title"
      >
        <header
          class="flex shrink-0 items-start gap-12px border-b border-b-solid border-line-subtle px-24px pb-14px pt-20px"
        >
          <div class="min-w-0 flex-1">
            <h2 class="m-0 text-20px font-600 tracking-[-0.02em] text-ink-primary">{{ content.settings.title }}</h2>
            <p class="m-0 mt-4px text-12px leading-[1.5] text-ink-tertiary">{{ content.settings.subtitle }}</p>
          </div>
          <OnButton
            variant="ghost"
            size="sm"
            icon="lucide:x"
            icon-only
            :aria-label="content.common.close"
            @click="emit('close')"
          />
        </header>

        <div class="flex min-h-0 flex-1">
          <nav class="flex w-148px shrink-0 flex-col gap-4px border-r border-r-solid border-line-subtle p-12px">
            <button
              type="button"
              class="h-32px rounded-small px-10px text-left font-sans text-12px outline-none transition-colors focus-visible:shadow-focus-ring"
              :class="
                activeTab === 'model'
                  ? 'bg-surface-raised font-600 text-ink-primary'
                  : 'text-ink-tertiary hover:bg-surface-raised hover:text-ink-secondary'
              "
              @click="activeTab = 'model'"
            >
              {{ content.settings.tabs.model }}
            </button>
            <button
              type="button"
              class="h-32px rounded-small px-10px text-left font-sans text-12px outline-none transition-colors focus-visible:shadow-focus-ring"
              :class="
                activeTab === 'mac'
                  ? 'bg-surface-raised font-600 text-ink-primary'
                  : 'text-ink-tertiary hover:bg-surface-raised hover:text-ink-secondary'
              "
              @click="activeTab = 'mac'"
            >
              {{ content.settings.tabs.mac }}
            </button>
            <button
              type="button"
              class="h-32px rounded-small px-10px text-left font-sans text-12px outline-none transition-colors focus-visible:shadow-focus-ring"
              :class="
                activeTab === 'tools'
                  ? 'bg-surface-raised font-600 text-ink-primary'
                  : 'text-ink-tertiary hover:bg-surface-raised hover:text-ink-secondary'
              "
              @click="
                activeTab = 'tools';
                loadCli(cliKind);
              "
            >
              {{ content.settings.tabs.tools }}
            </button>
          </nav>

          <div class="min-w-0 flex-1 overflow-y-auto px-24px pb-22px pt-18px">
            <template v-if="activeTab === 'model'">
              <section class="mb-22px">
                <h3 class="m-0 text-12px font-600 text-ink-primary">{{ content.settings.model.providerTitle }}</h3>
                <p class="m-0 mt-3px text-11px leading-[1.5] text-ink-tertiary">
                  {{ content.settings.model.providerHint }}
                </p>
                <div class="mt-10px grid grid-cols-2 gap-8px">
                  <button
                    v-for="item in HERMES_PROVIDERS"
                    :key="item.id"
                    type="button"
                    class="rounded-small border border-solid bg-surface-card p-10px text-left outline-none transition-colors focus-visible:shadow-focus-ring"
                    :class="
                      draftModel.provider === item.id
                        ? 'border-[rgba(170,170,255,0.45)] bg-[rgba(123,114,238,0.10)]'
                        : 'border-line-subtle hover:border-line-default'
                    "
                    @click="selectProvider(item.id)"
                  >
                    <b class="block text-12px font-600 text-ink-primary">{{ content.providers[item.id].name }}</b>
                    <span class="mt-2px block text-11px text-ink-tertiary">{{ content.providers[item.id].sub }}</span>
                  </button>
                </div>
              </section>

              <div class="mb-14px flex flex-col gap-6px">
                <label class="text-11px text-ink-tertiary" for="hermes-model">{{
                  content.settings.model.modelLabel
                }}</label>
                <div ref="modelPicker" class="relative">
                  <div class="flex items-center gap-4px">
                    <input
                      id="hermes-model"
                      v-model="draftModel.model"
                      class="h-34px min-w-0 flex-1 rounded-small border border-solid border-line-default bg-surface-inset px-10px font-sans text-12px text-ink-primary outline-none placeholder:text-ink-tertiary focus-visible:shadow-focus-ring"
                      autocomplete="off"
                      @focus="modelOpen = true"
                      @click="modelOpen = true"
                    />
                    <button
                      type="button"
                      class="flex h-34px w-30px shrink-0 items-center justify-center rounded-small border border-solid border-line-default bg-surface-inset text-ink-tertiary outline-none hover:text-ink-primary focus-visible:shadow-focus-ring"
                      :aria-label="content.settings.model.modelLabel"
                      :aria-expanded="modelOpen"
                      @click="modelOpen = !modelOpen"
                    >
                      <OnIcon name="lucide:chevron-down" :size="13" />
                    </button>
                  </div>
                  <div
                    v-if="modelOpen"
                    class="absolute left-0 right-0 top-[38px] z-10 max-h-220px overflow-y-auto rounded-small border border-solid border-line-default bg-component-modal-bg p-4px shadow-modal"
                  >
                    <button
                      v-for="model in models"
                      :key="model"
                      type="button"
                      class="flex w-full items-center gap-8px rounded-tiny px-8px py-7px text-left font-sans text-12px outline-none hover:bg-surface-raised focus-visible:shadow-focus-ring"
                      :class="
                        model === draftModel.model
                          ? 'bg-surface-raised font-600 text-ink-primary'
                          : 'text-ink-secondary'
                      "
                      @click="selectModel(model)"
                    >
                      <span class="min-w-0 flex-1 truncate">{{ model }}</span>
                      <OnIcon
                        v-if="model === draftModel.model"
                        name="lucide:check"
                        :size="13"
                        class="text-ink-accent"
                      />
                    </button>
                    <p v-if="!models.length" class="m-0 px-8px py-7px text-11px text-ink-tertiary">
                      {{ content.settings.model.noModels }}
                    </p>
                  </div>
                </div>
                <span class="text-11px leading-[1.45] text-ink-tertiary">{{ content.settings.model.modelHint }}</span>
                <div class="flex flex-wrap items-center gap-7px text-11px leading-[1.45] text-ink-tertiary">
                  <span>{{ content.settings.model.official }}</span>
                  <button
                    type="button"
                    class="cursor-pointer border-0 bg-transparent p-0 font-sans text-11px text-ink-tertiary underline underline-offset-2 hover:text-ink-secondary disabled:cursor-default disabled:no-underline"
                    :disabled="store.modelOptionsLoading"
                    @click="refreshModels"
                  >
                    {{
                      store.modelOptionsLoading
                        ? content.settings.model.loadingModels
                        : content.settings.model.refreshModels
                    }}
                  </button>
                  <span v-if="store.modelOptionsError" class="break-words text-status-danger">{{
                    store.modelOptionsError
                  }}</span>
                  <span v-else-if="officialWarning" class="break-words text-status-warning">{{ officialWarning }}</span>
                  <span v-else-if="modelOptionsUpdated" class="text-ink-tertiary">{{ modelOptionsUpdated }}</span>
                </div>
                <div
                  v-if="selectedCapability || selectedPricing"
                  class="flex flex-wrap items-center gap-6px text-11px text-ink-tertiary"
                >
                  <span v-if="selectedCapability?.reasoning" class="rounded-tiny bg-surface-raised px-6px py-2px">{{
                    content.settings.model.reasoning
                  }}</span>
                  <span v-if="selectedCapability?.fast" class="rounded-tiny bg-surface-raised px-6px py-2px">{{
                    content.settings.model.fast
                  }}</span>
                  <span v-if="selectedPricing?.free" class="rounded-tiny bg-surface-raised px-6px py-2px">{{
                    content.settings.model.free
                  }}</span>
                  <span v-else-if="selectedPricing?.input"
                    >{{ selectedPricing.input }} / {{ selectedPricing.output }}
                    {{ content.settings.model.perMtok }}</span
                  >
                </div>
              </div>

              <div v-if="reasoningSupported" class="mb-14px flex flex-col gap-6px">
                <label class="text-11px text-ink-tertiary" for="hermes-effort">{{ content.effort.label }}</label>
                <select
                  id="hermes-effort"
                  v-model="draftModel.effort"
                  class="h-34px w-full rounded-small border border-solid border-line-default bg-surface-inset px-10px font-sans text-12px text-ink-primary outline-none focus-visible:shadow-focus-ring"
                >
                  <option v-for="effort in HERMES_EFFORTS" :key="effort" :value="effort">
                    {{ content.effort[effort] }}
                  </option>
                </select>
                <span class="text-11px leading-[1.45] text-ink-tertiary">{{ content.effort.hint }}</span>
              </div>
              <div v-else class="mb-14px flex flex-col gap-6px">
                <span class="text-11px text-ink-tertiary">{{ content.effort.label }}</span>
                <span class="text-11px leading-[1.45] text-ink-tertiary">{{ content.effort.unsupported }}</span>
              </div>

              <div
                v-if="fastSupported"
                class="mb-14px flex items-center gap-12px border-b border-b-solid border-line-subtle py-10px"
              >
                <div class="min-w-0 flex-1">
                  <b class="block text-12px font-500 text-ink-primary">{{ content.effort.fast }}</b>
                  <span class="mt-2px block text-11px leading-[1.45] text-ink-tertiary">{{
                    content.effort.fastHint
                  }}</span>
                </div>
                <OnToggle v-model="draftModel.fast" :aria-label="content.effort.fast" />
              </div>

              <div class="mb-14px flex flex-col gap-6px">
                <label class="text-11px text-ink-tertiary" for="hermes-auth">{{ providerCopy.authLabel }}</label>
                <div v-if="provider.login" class="flex flex-col gap-7px">
                  <div
                    class="flex items-center gap-8px rounded-small border border-solid border-line-default bg-surface-inset px-10px py-8px"
                  >
                    <span
                      class="h-6px w-6px shrink-0 rounded-full"
                      :class="officialAuthenticated ? 'bg-status-success' : 'bg-status-warning'"
                    ></span>
                    <span class="text-12px text-ink-secondary">{{
                      officialAuthenticated
                        ? content.settings.model.detected
                        : needsImport
                          ? content.settings.model.needsConnect
                          : content.settings.model.notDetected
                    }}</span>
                    <div class="ml-auto flex items-center gap-4px">
                      <OnButton variant="ghost" size="sm" @click="refreshLogin">{{
                        content.settings.model.refresh
                      }}</OnButton>
                      <OnButton variant="ghost" size="sm" @click="openLogin">{{
                        content.settings.model.login
                      }}</OnButton>
                      <OnButton v-if="officialAuthenticated" variant="ghost" size="sm" @click="logout">{{
                        content.settings.model.logout
                      }}</OnButton>
                    </div>
                  </div>
                  <span class="text-11px leading-[1.45] text-ink-tertiary">{{ content.settings.model.loginHint }}</span>
                </div>
                <template v-else-if="draftModel.provider === 'local' || draftModel.provider === 'custom'">
                  <input
                    id="hermes-auth"
                    v-model="draftModel.endpoint"
                    class="h-34px w-full rounded-small border border-solid border-line-default bg-surface-inset px-10px font-sans text-12px text-ink-primary outline-none placeholder:text-ink-tertiary focus-visible:shadow-focus-ring"
                    :placeholder="provider.endpoint ?? 'https://…'"
                    @input="resetTest"
                  />
                  <input
                    id="hermes-key"
                    v-model="secret"
                    type="password"
                    class="h-34px w-full rounded-small border border-solid border-line-default bg-surface-inset px-10px font-sans text-12px text-ink-primary outline-none placeholder:text-ink-tertiary focus-visible:shadow-focus-ring"
                    :placeholder="draftModel.keySet ? content.settings.model.keySaved : content.settings.model.keyLabel"
                    autocomplete="off"
                  />
                </template>
                <input
                  v-else
                  id="hermes-auth"
                  v-model="secret"
                  type="password"
                  class="h-34px w-full rounded-small border border-solid border-line-default bg-surface-inset px-10px font-sans text-12px text-ink-primary outline-none placeholder:text-ink-tertiary focus-visible:shadow-focus-ring"
                  :placeholder="draftModel.keySet ? content.settings.model.keySaved : 'sk-…'"
                  autocomplete="off"
                  @input="resetTest"
                />
                <span
                  v-if="provider.localAuth && localAuthDetected && !draftModel.keySet"
                  class="text-11px leading-[1.45] text-status-warning"
                  >{{ content.settings.model.localOpenCode }}</span
                >
                <button
                  v-if="provider.localAuth"
                  type="button"
                  class="inline-flex items-center gap-4px self-start border-0 bg-transparent p-0 font-sans text-11px text-ink-tertiary outline-none hover:text-ink-secondary focus-visible:shadow-focus-ring"
                  @click="openExternal('https://opencode.ai/console')"
                >
                  {{ content.settings.model.openCodeConsole }} <OnIcon name="lucide:arrow-up-right" :size="12" />
                </button>
                <span class="text-11px leading-[1.45] text-ink-tertiary">{{ providerCopy.hint }}</span>
              </div>

              <div class="mb-18px flex flex-col gap-6px">
                <div class="flex items-center gap-10px">
                  <OnButton variant="ghost" size="sm" :disabled="testing" @click="testConnection">{{
                    testing ? content.settings.model.testing : content.common.test
                  }}</OnButton>
                  <OnButton
                    v-if="!provider.login && draftModel.keySet"
                    variant="ghost"
                    size="sm"
                    @click="removeApiKey"
                    >{{ content.settings.model.removeKey }}</OnButton
                  >
                  <span
                    class="text-11px"
                    :class="
                      testOk === false
                        ? 'text-status-warning'
                        : testOk === true
                          ? 'text-status-success'
                          : 'text-ink-tertiary'
                    "
                    >{{ testMessage || content.common.untested }}</span
                  >
                </div>
              </div>

              <div class="mb-18px flex flex-col gap-7px">
                <div class="flex items-start gap-10px">
                  <div class="min-w-0 flex-1">
                    <b class="block text-11px font-500 text-ink-secondary">{{ content.settings.model.auxTitle }}</b>
                    <span class="mt-2px block text-11px leading-[1.45] text-ink-tertiary">{{
                      content.settings.model.auxHint
                    }}</span>
                  </div>
                  <OnButton variant="ghost" size="sm" @click="openAux">{{
                    content.settings.model.auxConfigure
                  }}</OnButton>
                </div>
                <span class="text-11px leading-[1.45] text-ink-tertiary">{{ auxSummary }}</span>
              </div>

              <p
                class="m-0 rounded-small border border-solid border-[rgba(250,178,25,0.20)] bg-[rgba(250,178,25,0.05)] px-12px py-10px text-11px leading-[1.6] text-ink-secondary"
              >
                {{ content.settings.model.note }}
              </p>
            </template>

            <template v-else>
              <div
                class="mb-18px flex gap-10px rounded-default border border-solid border-[rgba(255,93,81,0.22)] bg-[rgba(255,93,81,0.06)] p-12px"
              >
                <OnIcon name="lucide:triangle-alert" :size="15" class="mt-1px text-status-danger" />
                <div class="min-w-0">
                  <b class="block text-12px font-600 text-ink-primary">{{ warning.title }}</b>
                  <p class="m-0 mt-3px text-11px leading-[1.55] text-ink-secondary">{{ warning.text }}</p>
                  <p v-if="terminalCredentialWarning" class="m-0 mt-3px text-11px leading-[1.55] text-status-danger">
                    {{ content.settings.mac.terminalCredentialWarning }}
                  </p>
                </div>
              </div>

              <div class="mb-18px flex items-center gap-12px border-b border-b-solid border-line-subtle py-10px">
                <div class="min-w-0 flex-1">
                  <b class="block text-12px font-500 text-ink-primary">{{ content.settings.mac.master }}</b>
                  <span class="mt-2px block text-11px leading-[1.45] text-ink-tertiary">{{
                    content.settings.mac.masterHint
                  }}</span>
                </div>
                <OnToggle
                  :model-value="draftMac.enabled"
                  :aria-label="content.settings.mac.master"
                  @update:model-value="toggleMaster"
                />
              </div>

              <section class="mb-18px">
                <h3 class="m-0 mb-10px text-12px font-600 text-ink-primary">{{ content.settings.mac.level }}</h3>
                <div class="flex gap-3px rounded-small border border-solid border-line-subtle bg-surface-inset p-3px">
                  <button
                    v-for="level in LEVELS"
                    :key="level"
                    type="button"
                    class="h-28px flex-1 rounded-tiny font-sans text-11px outline-none transition-colors focus-visible:shadow-focus-ring"
                    :class="
                      draftMac.level === level
                        ? 'bg-surface-control font-600 text-ink-primary'
                        : 'text-ink-tertiary hover:text-ink-secondary'
                    "
                    @click="selectLevel(level)"
                  >
                    {{ content.settings.mac.levels[level] }}
                  </button>
                </div>
              </section>

              <div class="mb-10px flex items-center gap-12px border-b border-b-solid border-line-subtle py-10px">
                <div class="min-w-0 flex-1">
                  <b class="block text-12px font-500 text-ink-primary">{{ content.settings.mac.confirm }}</b>
                  <span class="mt-2px block text-11px leading-[1.45] text-ink-tertiary">{{
                    content.settings.mac.confirmHint
                  }}</span>
                </div>
                <OnToggle v-model="draftMac.confirmWrites" :aria-label="content.settings.mac.confirm" />
              </div>
              <div class="mb-18px flex items-center gap-12px border-b border-b-solid border-line-subtle py-10px">
                <div class="min-w-0 flex-1">
                  <b class="block text-12px font-500 text-ink-primary">{{ content.settings.mac.panic }}</b>
                  <span class="mt-2px block text-11px leading-[1.45] text-ink-tertiary">{{
                    content.settings.mac.panicHint
                  }}</span>
                </div>
                <kbd
                  class="rounded-tiny border border-solid border-line-default bg-surface-inset px-6px py-3px font-sans text-11px text-ink-secondary"
                  >⌘⇧.</kbd
                >
                <OnButton variant="danger" size="sm" @click="stopMac">{{ content.common.stop }}</OnButton>
              </div>

              <section class="mb-18px">
                <h3 class="m-0 mb-6px text-12px font-600 text-ink-primary">{{ content.settings.mac.capabilities }}</h3>
                <div class="flex items-center gap-12px border-b border-b-solid border-line-subtle py-10px">
                  <div class="min-w-0 flex-1">
                    <b class="block text-12px font-500 text-ink-primary">{{ content.settings.mac.terminal }}</b
                    ><span class="mt-2px block text-11px text-ink-tertiary">{{
                      content.settings.mac.terminalHint
                    }}</span>
                  </div>
                  <OnToggle
                    v-model="draftMac.terminal"
                    :disabled="!draftMac.enabled"
                    :aria-label="content.settings.mac.terminal"
                  />
                </div>
                <div class="flex items-center gap-12px border-b border-b-solid border-line-subtle py-10px">
                  <div class="min-w-0 flex-1">
                    <b class="block text-12px font-500 text-ink-primary">{{ content.settings.mac.screen }}</b
                    ><span class="mt-2px block text-11px text-ink-tertiary">{{ content.settings.mac.screenHint }}</span>
                  </div>
                  <OnToggle
                    v-model="draftMac.screen"
                    :disabled="!draftMac.enabled"
                    :aria-label="content.settings.mac.screen"
                  />
                </div>
                <div class="flex items-center gap-12px border-b border-b-solid border-line-subtle py-10px">
                  <div class="min-w-0 flex-1">
                    <b class="block text-12px font-500 text-ink-primary">{{ content.settings.mac.files }}</b
                    ><span class="mt-2px block text-11px text-ink-tertiary">{{ content.settings.mac.filesHint }}</span>
                  </div>
                  <OnToggle
                    v-model="draftMac.files"
                    :disabled="!draftMac.enabled"
                    :aria-label="content.settings.mac.files"
                  />
                </div>
                <div class="flex items-center gap-12px border-b border-b-solid border-line-subtle py-10px">
                  <div class="min-w-0 flex-1">
                    <b class="block text-12px font-500 text-ink-primary">{{ content.settings.mac.cron }}</b
                    ><span class="mt-2px block text-11px text-ink-tertiary">{{ content.settings.mac.cronHint }}</span>
                  </div>
                  <OnToggle
                    v-model="draftMac.cron"
                    :disabled="!draftMac.enabled"
                    :aria-label="content.settings.mac.cron"
                  />
                </div>
              </section>

              <section class="mb-18px">
                <h3 class="m-0 mb-6px text-12px font-600 text-ink-primary">{{ content.settings.mac.permissions }}</h3>
                <div
                  class="flex items-center gap-8px border-b border-b-solid border-line-subtle py-7px text-11px text-ink-secondary"
                >
                  <span class="h-6px w-6px rounded-full" :class="permissionDot(permissions?.accessibility)"></span
                  >{{ content.settings.mac.accessibility
                  }}<b class="ml-auto text-11px font-500 text-ink-tertiary">{{
                    permissionText(permissions?.accessibility)
                  }}</b>
                  <button
                    type="button"
                    class="flex text-ink-tertiary outline-none hover:text-ink-secondary"
                    :aria-label="content.settings.mac.accessibilitySettings"
                    @click="openPermission('Privacy_Accessibility')"
                  >
                    <OnIcon name="lucide:arrow-up-right" :size="12" />
                  </button>
                </div>
                <div
                  class="flex items-center gap-8px border-b border-b-solid border-line-subtle py-7px text-11px text-ink-secondary"
                >
                  <span class="h-6px w-6px rounded-full" :class="permissionDot(permissions?.screenRecording)"></span
                  >{{ content.settings.mac.screenRecording
                  }}<b class="ml-auto text-11px font-500 text-ink-tertiary">{{
                    permissionText(permissions?.screenRecording)
                  }}</b>
                  <button
                    v-if="!permissions?.screenRecording"
                    type="button"
                    class="text-11px text-ink-tertiary outline-none hover:text-ink-secondary"
                    @click="requestScreenPermission"
                  >
                    {{ content.settings.mac.requestPermission }}
                  </button>
                  <button
                    type="button"
                    class="flex text-ink-tertiary outline-none hover:text-ink-secondary"
                    :aria-label="content.settings.mac.screenRecordingSettings"
                    @click="openPermission('Privacy_ScreenCapture')"
                  >
                    <OnIcon name="lucide:arrow-up-right" :size="12" />
                  </button>
                </div>
                <div
                  class="flex items-center gap-8px border-b border-b-solid border-line-subtle py-7px text-11px text-ink-secondary"
                >
                  <span class="h-6px w-6px rounded-full" :class="permissionDot(permissions?.fullDisk)"></span
                  >{{ content.settings.mac.fullDisk
                  }}<b class="ml-auto text-11px font-500 text-ink-tertiary">{{
                    permissionText(permissions?.fullDisk)
                  }}</b>
                  <button
                    type="button"
                    class="flex text-ink-tertiary outline-none hover:text-ink-secondary"
                    :aria-label="content.settings.mac.fullDiskSettings"
                    @click="openPermission('Privacy_AllFiles')"
                  >
                    <OnIcon name="lucide:arrow-up-right" :size="12" />
                  </button>
                </div>
                <div
                  class="flex items-center gap-8px border-b border-b-solid border-line-subtle py-7px text-11px text-ink-secondary"
                >
                  <span class="h-6px w-6px rounded-full bg-status-warning"></span>{{ content.settings.mac.automation
                  }}<b class="ml-auto text-11px font-500 text-ink-tertiary">{{ content.settings.mac.onDemand }}</b>
                </div>
                <div class="mt-8px flex items-center gap-10px">
                  <OnButton variant="ghost" size="sm" @click="checkPermissions">{{
                    content.settings.mac.openSettings
                  }}</OnButton>
                  <OnButton variant="ghost" size="sm" @click="refreshPermissions">{{
                    content.settings.mac.refreshPermissions
                  }}</OnButton>
                  <button
                    type="button"
                    class="inline-flex items-center gap-5px font-sans text-11px text-ink-tertiary outline-none hover:text-ink-secondary focus-visible:shadow-focus-ring"
                    @click="openAudit"
                  >
                    {{ content.settings.mac.audit }} <OnIcon name="lucide:arrow-up-right" :size="12" />
                  </button>
                  <button
                    type="button"
                    class="inline-flex items-center gap-5px font-sans text-11px text-ink-tertiary outline-none hover:text-ink-secondary focus-visible:shadow-focus-ring"
                    @click="openLogs"
                  >
                    {{ content.settings.mac.openLogs }} <OnIcon name="lucide:arrow-up-right" :size="12" />
                  </button>
                </div>
              </section>

              <section class="mb-18px">
                <h3 class="m-0 mb-6px text-12px font-600 text-ink-primary">{{ content.install.dataTitle }}</h3>
                <p class="m-0 text-11px leading-[1.5] text-ink-tertiary">{{ content.install.dataBody }}</p>
                <code
                  class="mt-8px block break-all rounded-small bg-surface-inset px-9px py-7px font-mono text-10.5px text-ink-tertiary"
                  >{{ store.runtime?.dataPath ?? '…' }}</code
                >
                <div v-if="store.runtime?.outdated" class="mt-10px flex items-center gap-8px">
                  <span class="text-11px text-status-warning">{{ content.install.updateAvailable }}</span>
                  <OnButton variant="accent" size="sm" :disabled="store.installing" @click="updateRuntime">{{
                    content.install.updateRuntime
                  }}</OnButton>
                </div>
                <OnButton class="mt-10px" variant="danger" size="sm" @click="uninstallOpen = true">{{
                  content.install.uninstall
                }}</OnButton>
              </section>
            </template>
            <template v-if="activeTab === 'tools'">
              <section class="mb-18px">
                <h3 class="m-0 text-12px font-600 text-ink-primary">{{ content.settings.diagnostics.title }}</h3>
                <p class="m-0 mt-3px text-11px leading-[1.5] text-ink-tertiary">
                  {{ content.settings.diagnostics.body }}
                </p>
                <div class="mt-10px flex flex-wrap gap-7px">
                  <OnButton variant="ghost" size="sm" @click="loadCli('tools')">{{
                    content.settings.diagnostics.tools
                  }}</OnButton>
                  <OnButton variant="ghost" size="sm" @click="loadCli('skills')">{{
                    content.settings.diagnostics.skills
                  }}</OnButton>
                  <OnButton variant="ghost" size="sm" @click="loadCli('cron')">{{
                    content.settings.diagnostics.cron
                  }}</OnButton>
                </div>
                <div class="mt-10px flex flex-wrap items-center gap-6px">
                  <input
                    v-model="cronJobId"
                    class="h-30px w-220px rounded-small border border-solid border-line-default bg-surface-inset px-9px font-mono text-11px text-ink-primary outline-none focus-visible:shadow-focus-ring"
                    :placeholder="content.settings.diagnostics.cronJobId"
                    @keydown.enter="runCronAction('pause')"
                  />
                  <OnButton
                    variant="ghost"
                    size="sm"
                    :disabled="cronBusy || !cronJobId.trim()"
                    @click="runCronAction('pause')"
                    >{{ content.settings.diagnostics.pause }}</OnButton
                  >
                  <OnButton
                    variant="ghost"
                    size="sm"
                    :disabled="cronBusy || !cronJobId.trim()"
                    @click="runCronAction('resume')"
                    >{{ content.settings.diagnostics.resume }}</OnButton
                  >
                  <OnButton
                    variant="ghost"
                    size="sm"
                    :disabled="cronBusy || !cronJobId.trim()"
                    @click="runCronAction('remove')"
                    >{{ content.settings.diagnostics.remove }}</OnButton
                  >
                </div>
                <div class="mt-10px flex flex-wrap items-center gap-6px">
                  <input
                    v-model="skillName"
                    class="h-30px w-260px rounded-small border border-solid border-line-default bg-surface-inset px-9px font-mono text-11px text-ink-primary outline-none focus-visible:shadow-focus-ring"
                    :placeholder="content.settings.diagnostics.skillName"
                  />
                  <OnButton
                    variant="ghost"
                    size="sm"
                    :disabled="actionBusy || !skillName.trim()"
                    @click="runSkillAction('install')"
                    >{{ content.settings.diagnostics.install }}</OnButton
                  >
                  <OnButton
                    variant="ghost"
                    size="sm"
                    :disabled="actionBusy || !skillName.trim()"
                    @click="runSkillAction('uninstall')"
                    >{{ content.settings.diagnostics.uninstall }}</OnButton
                  >
                </div>
                <div class="mt-10px flex flex-wrap items-center gap-6px">
                  <input
                    v-model="toolName"
                    class="h-30px w-260px rounded-small border border-solid border-line-default bg-surface-inset px-9px font-mono text-11px text-ink-primary outline-none focus-visible:shadow-focus-ring"
                    :placeholder="content.settings.diagnostics.toolName"
                  />
                  <OnButton
                    variant="ghost"
                    size="sm"
                    :disabled="actionBusy || !toolName.trim()"
                    @click="runToolAction('enable')"
                    >{{ content.settings.diagnostics.enable }}</OnButton
                  >
                  <OnButton
                    variant="ghost"
                    size="sm"
                    :disabled="actionBusy || !toolName.trim()"
                    @click="runToolAction('disable')"
                    >{{ content.settings.diagnostics.disable }}</OnButton
                  >
                </div>
                <div class="mt-10px flex flex-wrap items-center gap-6px">
                  <input
                    v-model="mcpName"
                    class="h-30px w-180px rounded-small border border-solid border-line-default bg-surface-inset px-9px font-mono text-11px text-ink-primary outline-none focus-visible:shadow-focus-ring"
                    :placeholder="content.settings.diagnostics.mcpName"
                  />
                  <input
                    v-model="mcpUrl"
                    class="h-30px w-260px rounded-small border border-solid border-line-default bg-surface-inset px-9px font-mono text-11px text-ink-primary outline-none focus-visible:shadow-focus-ring"
                    :placeholder="content.settings.diagnostics.mcpUrl"
                  />
                  <OnButton variant="ghost" size="sm" :disabled="mcpBusy" @click="runMcp('list')">{{
                    content.settings.diagnostics.mcpList
                  }}</OnButton>
                  <OnButton
                    variant="ghost"
                    size="sm"
                    :disabled="mcpBusy || !mcpName.trim() || !mcpUrl.trim()"
                    @click="runMcp('add')"
                    >{{ content.settings.diagnostics.mcpAdd }}</OnButton
                  >
                  <OnButton
                    variant="ghost"
                    size="sm"
                    :disabled="mcpBusy || !mcpName.trim()"
                    @click="runMcp('remove')"
                    >{{ content.settings.diagnostics.mcpRemove }}</OnButton
                  >
                </div>
              </section>
              <pre
                class="m-0 max-h-460px overflow-auto whitespace-pre-wrap break-words rounded-small border border-solid border-line-subtle bg-surface-inset px-12px py-10px font-mono text-11px leading-[1.55] text-ink-secondary"
                >{{ cliLoading ? content.common.loading : cliOutput || content.settings.diagnostics.empty }}</pre
              >
            </template>
          </div>
        </div>

        <footer class="flex shrink-0 items-center gap-8px border-t border-t-solid border-line-subtle px-24px py-14px">
          <span class="min-w-0 flex-1 text-11px leading-[1.45] text-ink-tertiary">{{ content.settings.footer }}</span>
          <OnButton variant="ghost" @click="emit('close')">{{ content.common.cancel }}</OnButton>
          <OnButton variant="accent" @click="save">{{ content.common.save }}</OnButton>
        </footer>
      </div>
    </div>

    <OnModal
      :open="auditOpen"
      :title="content.settings.mac.auditTitle"
      :description="content.settings.mac.auditNote"
      size="lg"
      role="dialog"
      @close="auditOpen = false"
    >
      <div v-if="auditLoading" class="py-24px text-center text-11px text-ink-tertiary">
        {{ content.common.loading }}
      </div>
      <div v-else-if="!auditEntries.length" class="py-24px text-center text-11px text-ink-tertiary">
        {{ content.settings.mac.auditEmpty }}
      </div>
      <div v-else class="max-h-420px overflow-auto">
        <div
          v-for="entry in auditEntries"
          :key="`${entry.at}-${entry.sessionId ?? ''}`"
          class="flex flex-col gap-3px border-b border-b-solid border-line-subtle py-8px text-11px"
        >
          <div class="flex items-center gap-8px">
            <span class="text-ink-tertiary">{{ entry.at }}</span>
            <b class="text-ink-primary">{{ entry.provider }} · {{ entry.model }}</b>
            <span class="ml-auto text-ink-tertiary">{{ entry.outcome }}</span>
          </div>
          <div class="text-ink-tertiary">
            {{ entry.toolsets || 'safe' }} · {{ entry.controlLevel }} · {{ entry.promptChars }} chars
          </div>
        </div>
      </div>
      <template #footer>
        <OnButton variant="ghost" @click="openAuditFile">{{ content.settings.mac.auditOpenFile }}</OnButton>
        <OnButton variant="accent" @click="auditOpen = false">{{ content.common.close }}</OnButton>
      </template>
    </OnModal>

    <OnModal
      :open="uninstallOpen"
      :title="content.install.uninstall"
      :description="content.install.uninstallConfirm"
      size="sm"
      role="alertdialog"
      @close="uninstallOpen = false"
    >
      <template #footer>
        <OnButton variant="ghost" @click="uninstallOpen = false">{{ content.common.cancel }}</OnButton>
        <OnButton variant="danger" @click="uninstallHermes">{{ content.install.uninstall }}</OnButton>
      </template>
    </OnModal>
  </Teleport>
  <Teleport to="body">
    <div
      v-if="open && auxOpen"
      class="fixed inset-0 z-700 flex items-center justify-center p-24px"
      @click.self="auxOpen = false"
    >
      <div class="absolute inset-0 bg-black/60 backdrop-blur-[3px]" aria-hidden="true" @click="auxOpen = false"></div>
      <div
        class="relative z-1 flex max-h-[calc(100vh-48px)] w-640px max-w-full flex-col overflow-hidden rounded-huge border border-solid border-line-strong bg-component-modal-bg shadow-modal"
        role="dialog"
        aria-modal="true"
        :aria-label="content.settings.model.auxTitle"
      >
        <header
          class="flex shrink-0 items-start gap-12px border-b border-b-solid border-line-subtle px-20px pb-12px pt-18px"
        >
          <div class="min-w-0 flex-1">
            <h3 class="m-0 text-16px font-600 text-ink-primary">{{ content.settings.model.auxTitle }}</h3>
            <p class="m-0 mt-3px text-11px leading-[1.5] text-ink-tertiary">{{ content.settings.model.auxHint }}</p>
          </div>
          <OnButton
            variant="ghost"
            size="sm"
            icon="lucide:x"
            icon-only
            :aria-label="content.common.close"
            @click="auxOpen = false"
          />
        </header>
        <div class="min-h-0 flex-1 overflow-y-auto px-20px py-12px">
          <p
            v-if="!auxProviders.length"
            class="m-0 mb-10px rounded-small border border-solid border-line-subtle bg-surface-inset px-10px py-8px text-11px leading-[1.5] text-ink-tertiary"
          >
            {{ content.settings.model.auxNoModels }}
          </p>
          <p v-else class="m-0 mb-10px text-11px text-ink-tertiary">
            {{ content.settings.model.auxAvailable.replace('{count}', String(auxModelCount)) }}
          </p>
          <div
            v-for="task in HERMES_AUX_TASKS"
            :key="task"
            class="flex items-center gap-12px border-b border-b-solid border-line-subtle py-9px last:border-b-0"
          >
            <div class="min-w-0 flex-1">
              <b class="block text-12px font-500 text-ink-primary">{{ content.settings.model.auxTasks[task].label }}</b>
              <span class="mt-1px block text-11px text-ink-tertiary">{{
                content.settings.model.auxTasks[task].hint
              }}</span>
            </div>
            <select
              class="h-30px w-230px shrink-0 rounded-small border border-solid border-line-default bg-surface-inset px-8px font-sans text-11px text-ink-primary outline-none focus-visible:shadow-focus-ring"
              :value="auxValue(task)"
              @change="onAuxChange(task, $event)"
            >
              <option value="auto">{{ content.settings.model.auxAuto }}</option>
              <optgroup v-for="entry in auxProviders" :key="entry.slug" :label="entry.name">
                <option
                  v-for="model in entry.models"
                  :key="`${entry.slug}-${model}`"
                  :value="`${entry.slug}::${model}`"
                >
                  {{ entry.name }} · {{ model }}
                </option>
              </optgroup>
            </select>
          </div>
          <div
            class="mt-12px flex items-start gap-12px rounded-small border border-solid border-line-subtle bg-surface-inset px-12px py-10px"
          >
            <div class="min-w-0 flex-1">
              <b class="block text-12px font-500 text-ink-primary">{{ content.settings.model.visionPriority }}</b>
              <span class="mt-2px block text-11px leading-[1.5] text-ink-tertiary">{{
                content.settings.model.visionPriorityHint
              }}</span>
              <span v-if="visionAuxAuto" class="mt-3px block text-11px text-status-warning">{{
                content.settings.model.visionPriorityAuto
              }}</span>
            </div>
            <OnToggle
              :model-value="store.forceAuxVision"
              :disabled="visionAuxAuto"
              :aria-label="content.settings.model.visionPriority"
              @update:model-value="store.setForceAuxVision"
            />
          </div>
        </div>
        <footer
          class="flex shrink-0 items-center justify-between gap-10px border-t border-t-solid border-line-subtle px-20px py-12px"
        >
          <OnButton variant="ghost" size="sm" @click="store.resetAuxiliary()">{{
            content.settings.model.auxReset
          }}</OnButton>
          <OnButton variant="accent" size="sm" @click="auxOpen = false">{{ content.common.close }}</OnButton>
        </footer>
      </div>
    </div>
  </Teleport>
  <Teleport to="body">
    <div
      v-if="open && loginOpen"
      class="fixed inset-0 z-700 flex items-center justify-center p-24px"
      @click.self="cancelLogin"
    >
      <div class="absolute inset-0 bg-black/60 backdrop-blur-[3px]" aria-hidden="true" @click="cancelLogin"></div>
      <div
        class="relative z-1 flex max-h-[calc(100vh-48px)] w-560px max-w-full flex-col overflow-hidden rounded-huge border border-solid border-line-strong bg-component-modal-bg shadow-modal"
        role="dialog"
        aria-modal="true"
        :aria-label="content.settings.model.loginTitle.replace('{name}', loginName)"
      >
        <header
          class="flex shrink-0 items-start gap-12px border-b border-b-solid border-line-subtle px-20px pb-12px pt-18px"
        >
          <div class="min-w-0 flex-1">
            <h3 class="m-0 text-16px font-600 text-ink-primary">
              {{ content.settings.model.loginTitle.replace('{name}', loginName) }}
            </h3>
            <p class="m-0 mt-4px flex items-center gap-7px text-11px leading-[1.5] text-ink-tertiary">
              <span
                class="h-6px w-6px shrink-0 rounded-full"
                :class="
                  loginPhase === 'success'
                    ? 'bg-status-success'
                    : loginPhase === 'error'
                      ? 'bg-status-danger'
                      : loginPhase === 'cancelled'
                        ? 'bg-ink-tertiary'
                        : 'animate-pulse bg-status-warning'
                "
              ></span>
              {{ loginStatusText }}
            </p>
          </div>
          <OnButton
            variant="ghost"
            size="sm"
            icon="lucide:x"
            icon-only
            :aria-label="content.common.close"
            @click="cancelLogin"
          />
        </header>
        <div class="min-h-0 flex-1 overflow-y-auto px-20px py-14px">
          <div
            v-if="loginCode"
            class="mb-10px rounded-small border border-solid border-line-subtle bg-surface-inset px-12px py-10px"
          >
            <span class="block text-10px text-ink-tertiary">{{ content.settings.model.loginCode }}</span>
            <div class="mt-4px flex items-center gap-8px">
              <b class="font-mono text-16px tracking-[0.12em] text-ink-primary">{{ loginCode }}</b>
              <OnButton variant="ghost" size="sm" @click="copyLoginCode">{{
                content.settings.model.loginCopy
              }}</OnButton>
            </div>
          </div>
          <div v-if="loginUrl" class="mb-10px flex items-center gap-8px">
            <OnButton variant="accent" size="sm" @click="openExternal(loginUrl)">{{
              content.settings.model.loginOpenBrowser
            }}</OnButton>
            <span class="min-w-0 truncate text-11px text-ink-tertiary">{{ loginUrl }}</span>
          </div>
          <div
            v-if="loginLines.length"
            class="mb-10px rounded-small border border-solid border-line-subtle bg-surface-inset px-10px py-8px"
          >
            <span class="block text-10px text-ink-tertiary">{{ content.settings.model.loginOutput }}</span>
            <pre
              class="m-0 mt-5px max-h-150px overflow-y-auto whitespace-pre-wrap break-words font-mono text-10.5px leading-[1.55] text-ink-secondary"
              >{{ loginLines.join('\n') }}</pre
            >
          </div>
          <form
            v-if="loginPhase !== 'success' && loginPhase !== 'error' && loginPhase !== 'cancelled'"
            class="flex items-center gap-7px"
            @submit.prevent="sendLoginInput"
          >
            <input
              v-model="loginInput"
              class="h-32px min-w-0 flex-1 rounded-small border border-solid border-line-default bg-surface-inset px-9px font-sans text-12px text-ink-primary outline-none placeholder:text-ink-tertiary focus-visible:shadow-focus-ring"
              :placeholder="content.settings.model.loginInputPlaceholder"
              autocomplete="off"
            />
            <OnButton type="submit" variant="ghost" size="sm" :disabled="!loginInput.trim()">{{
              content.settings.model.loginSend
            }}</OnButton>
          </form>
        </div>
        <footer
          class="flex shrink-0 items-center justify-between gap-10px border-t border-t-solid border-line-subtle px-20px py-12px"
        >
          <span class="text-10px text-ink-tertiary">{{ content.settings.model.loginHint }}</span>
          <OnButton v-if="loginPhase === 'success'" variant="accent" size="sm" @click="loginOpen = false">{{
            content.settings.model.loginClose
          }}</OnButton>
          <OnButton v-else variant="ghost" size="sm" @click="cancelLogin">{{
            content.settings.model.loginCancel
          }}</OnButton>
        </footer>
      </div>
    </div>
  </Teleport>
</template>
