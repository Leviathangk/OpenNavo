<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { OnButton, OnCard, OnChip, OnConfirm, OnModal, OnSegmented, OnToggle } from '@opennavo/ui';
import SettingRow from './SettingRow.vue';
import type { AuditCaller, AuditEntry, McpStatus } from '@/ipc/bindings';
import { commands } from '@/ipc/client';
import { useToasts } from '@/composables/useToasts';
import { useAppLocale } from '@/composables/useAppLocale';
import {
  MCP_CLIENTS,
  copiedMcpState,
  mcpConfig,
  mcpLegacyStale,
  mcpMovedClients,
  rememberCopiedMcpPath
} from '@/utils/mcpConfig';
import type { McpClient, McpCopiedState } from '@/utils/mcpConfig';

// Settings → Local MCP. The switches are never updated optimistically: every change shows the state the
// backend reports afterwards. When turning something off fails, the backend keeps it on, so this page
// says "still on" instead of looking off while AI apps can still connect.
type Failure = 'offFailed' | 'writeOffFailed' | 'changeFailed';

const { t } = useI18n();
const toasts = useToasts();
const { appLocale } = useAppLocale();
const status = ref<McpStatus | null>(null);
const busy = ref(false);
const failure = ref<Failure | null>(null);
const client = ref<McpClient>('claude');
const copied = ref<McpCopiedState>(copiedMcpState());
const loadFailed = ref(false);

// Every status update takes a new token; a read only lands if nothing newer arrived meanwhile, so a slow
// focus-triggered load() can never overwrite the state a switch change just reported.
let statusToken = 0;
async function refresh() {
  const token = ++statusToken;
  const next = await commands.mcpStatus();
  if (token === statusToken) status.value = next;
}
// The first read can fail (backend not ready, IPC error); show that with a retry instead of a blank page.
async function load() {
  try {
    await refresh();
    loadFailed.value = false;
  } catch {
    if (!status.value) loadFailed.value = true;
  }
}
// The app may be moved, or another window may copy a configuration, while this page stays open.
function recheck() {
  if (document.visibilityState === 'hidden') return;
  copied.value = copiedMcpState();
  if (!busy.value) void load();
}
onMounted(() => {
  void load();
  window.addEventListener('focus', recheck);
  document.addEventListener('visibilitychange', recheck);
});
onBeforeUnmount(() => {
  window.removeEventListener('focus', recheck);
  document.removeEventListener('visibilitychange', recheck);
});

async function change(kind: 'enabled' | 'write', next: boolean) {
  if (busy.value || !status.value) return;
  busy.value = true;
  failure.value = null;
  try {
    const result = kind === 'enabled' ? await commands.mcpSetEnabled(next) : await commands.mcpSetWriteEnabled(next);
    if (result.status === 'ok') {
      statusToken++;
      status.value = result.data;
      return;
    }
    await refresh();
    const stillOn = kind === 'enabled' ? status.value?.enabled : status.value?.writeEnabled;
    failure.value = !next && stillOn ? (kind === 'enabled' ? 'offFailed' : 'writeOffFailed') : 'changeFailed';
  } catch {
    await refresh().catch(() => undefined);
    failure.value = 'changeFailed';
  } finally {
    busy.value = false;
  }
}

const clients = computed(() => MCP_CLIENTS.map(value => ({ value, label: t(`settings.mcp.clients.${value}`) })));
const commandPath = computed(() => status.value?.commandPath ?? null);
// The backend asks the volume whether its paths are case-sensitive; until it answers, assume the common Mac.
const caseSensitive = computed(() => status.value?.caseSensitive ?? false);
const config = computed(() => (commandPath.value ? mcpConfig(client.value, commandPath.value) : ''));
const movedClients = computed(() => mcpMovedClients(commandPath.value, copied.value.paths, caseSensitive.value));
const movedLabels = computed(() => movedClients.value.map(value => t(`settings.mcp.clients.${value}`)).join(', '));
// The pre-per-client key names no client, so it gets its own sentence instead of four guessed ones.
const legacyStale = computed(
  () => !movedClients.value.length && mcpLegacyStale(commandPath.value, copied.value.legacy, caseSensitive.value)
);
// Writes need a listener that is actually up; an "on" switch with a failed bind accepts nothing.
const serving = computed(() => !!status.value?.enabled && !!status.value?.running);
const notRunning = computed(() => !!status.value?.enabled && !status.value.running);

async function copyConfig() {
  if (!commandPath.value) return;
  try {
    await navigator.clipboard.writeText(config.value);
  } catch {
    toasts.push({ tone: 'danger', title: t('settings.mcp.copyFailed') });
    return;
  }
  copied.value = { paths: rememberCopiedMcpPath(client.value, commandPath.value), legacy: null };
  toasts.push({
    tone: 'success',
    title: t('settings.mcp.copied', { client: t(`settings.mcp.clients.${client.value}`) })
  });
}

// Write request log: stored only on this Mac by the backend; the page only reads and clears it.
const auditOpen = ref(false);
const auditEntries = ref<AuditEntry[]>([]);
const auditFailed = ref(false);
const clearOpen = ref(false);
const clearing = ref(false);

async function openAudit() {
  auditFailed.value = false;
  const result = await commands.mcpAuditList().catch(() => null);
  if (result?.status === 'ok') auditEntries.value = result.data;
  else {
    auditEntries.value = [];
    auditFailed.value = true;
  }
  auditOpen.value = true;
}

async function clearAudit() {
  clearing.value = true;
  try {
    const result = await commands.mcpAuditClear().catch(() => null);
    if (result?.status === 'ok') {
      auditEntries.value = [];
      clearOpen.value = false;
      toasts.push({ tone: 'success', title: t('settings.mcp.audit.cleared') });
    } else toasts.push({ tone: 'danger', title: t('settings.mcp.audit.clearFailed') });
  } finally {
    clearing.value = false;
  }
}

function auditTime(at: number) {
  return new Intl.DateTimeFormat(appLocale.value, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(at));
}

function auditTool(tool: string) {
  if (tool === 'install_app') return t('settings.mcp.audit.install');
  if (tool === 'uninstall_app') return t('settings.mcp.audit.uninstall');
  return tool;
}

function auditCaller(caller: AuditCaller) {
  if (caller.signed && caller.signer) return caller.signer;
  if (caller.path) return t('settings.mcp.audit.unsigned', { path: caller.path });
  return t('settings.mcp.audit.unknownCaller');
}
</script>

<template>
  <div
    v-if="!status && loadFailed"
    role="alert"
    class="flex flex-col items-start gap-8px text-12.5px text-status-danger"
    data-test="mcp-load-failed"
  >
    {{ t('settings.mcp.statusFailed') }}
    <OnButton size="sm" data-test="mcp-load-retry" @click="load">{{ t('common.retry') }}</OnButton>
  </div>
  <div v-if="status" class="flex flex-col gap-16px">
    <OnCard padding="none">
      <SettingRow :label="t('settings.mcp.enable')" :hint="t('settings.mcp.enableHint')" label-id="setting-mcpEnabled">
        <OnChip v-if="failure === 'offFailed' && status.enabled" tone="warning" dot data-test="mcp-still-on">{{
          t('settings.mcp.stillOn')
        }}</OnChip>
        <OnToggle
          :model-value="status.enabled"
          :disabled="busy"
          aria-labelledby="setting-mcpEnabled"
          data-test="mcp-enabled"
          @update:model-value="value => change('enabled', value)"
        />
      </SettingRow>
      <SettingRow :label="t('settings.mcp.write')" :hint="t('settings.mcp.writeHint')" label-id="setting-mcpWrite">
        <OnChip
          v-if="failure === 'writeOffFailed' && status.writeEnabled"
          tone="warning"
          dot
          data-test="mcp-write-still-on"
          >{{ t('settings.mcp.stillOn') }}</OnChip
        >
        <OnToggle
          :model-value="status.writeEnabled"
          :disabled="busy || (!serving && !status.writeEnabled)"
          aria-labelledby="setting-mcpWrite"
          data-test="mcp-write"
          @update:model-value="value => change('write', value)"
        />
      </SettingRow>
      <div
        v-if="failure || status.error || notRunning"
        role="alert"
        class="border-t border-t-solid border-line-subtle px-18px py-12px text-12.5px text-status-danger"
        data-test="mcp-failure"
      >
        <span v-if="failure" class="block">{{ t(`settings.mcp.${failure}`) }}</span>
        <span v-if="notRunning" class="block" data-test="mcp-not-running">{{ t('settings.mcp.notRunning') }}</span>
        <span v-if="status.error" data-test="mcp-error" class="mt-2px block font-mono text-11.5px text-ink-tertiary">{{
          status.error
        }}</span>
      </div>
    </OnCard>

    <OnCard padding="none">
      <header class="border-b border-b-solid border-line-subtle px-18px py-14px">
        <h2 class="m-0 text-15px font-600 text-ink-primary">{{ t('settings.mcp.config') }}</h2>
        <p class="m-0 mt-4px text-12.5px text-ink-tertiary">{{ t('settings.mcp.configHint') }}</p>
      </header>
      <div class="flex flex-col gap-12px px-18px py-14px">
        <p v-if="movedClients.length" role="status" class="m-0 text-12.5px text-status-warning" data-test="mcp-moved">
          {{ t('settings.mcp.moved') }}
          <span class="block" data-test="mcp-moved-clients">{{
            t('settings.mcp.movedClients', { clients: movedLabels })
          }}</span>
        </p>
        <p v-else-if="legacyStale" role="status" class="m-0 text-12.5px text-status-warning" data-test="mcp-moved">
          {{ t('settings.mcp.movedLegacy') }}
        </p>
        <div class="flex flex-wrap items-center justify-between gap-8px">
          <OnSegmented v-model="client" :options="clients" size="sm" :aria-label="t('settings.mcp.config')" />
          <OnButton size="sm" icon="copy" :disabled="!commandPath" data-test="mcp-copy" @click="copyConfig">{{
            t('settings.mcp.copy')
          }}</OnButton>
        </div>
        <pre
          v-if="commandPath"
          class="m-0 overflow-x-auto rounded-default border border-solid border-line-subtle bg-surface-inset px-14px py-12px font-mono text-12px leading-[1.7] text-ink-secondary"
          data-test="mcp-config"
          >{{ config }}</pre
        >
        <p v-else class="m-0 text-12.5px text-status-danger">{{ t('settings.mcp.pathUnavailable') }}</p>
      </div>
    </OnCard>
    <OnCard padding="none">
      <SettingRow :label="t('settings.mcp.audit.title')" :hint="t('settings.mcp.audit.hint')">
        <OnButton size="sm" data-test="mcp-audit-view" @click="openAudit">{{ t('settings.mcp.audit.view') }}</OnButton>
        <OnButton size="sm" variant="ghost" data-test="mcp-audit-clear" @click="clearOpen = true">{{
          t('settings.mcp.audit.clear')
        }}</OnButton>
      </SettingRow>
    </OnCard>

    <OnModal v-model:open="auditOpen" size="lg" :title="t('settings.mcp.audit.title')">
      <p v-if="auditFailed" role="alert" class="m-0 text-12.5px text-status-danger" data-test="mcp-audit-failed">
        {{ t('settings.mcp.audit.loadFailed') }}
      </p>
      <p v-else-if="!auditEntries.length" class="m-0 text-12.5px text-ink-tertiary" data-test="mcp-audit-empty">
        {{ t('settings.mcp.audit.empty') }}
      </p>
      <ul v-else class="m-0 flex list-none flex-col gap-10px p-0" data-test="mcp-audit-list">
        <li
          v-for="entry in auditEntries"
          :key="entry.id"
          class="rounded-default border border-solid border-line-subtle px-14px py-10px text-12.5px"
          data-test="mcp-audit-entry"
        >
          <div class="flex flex-wrap items-center justify-between gap-8px">
            <span class="font-600 text-ink-primary">{{ auditTool(entry.tool) }}</span>
            <OnChip :tone="entry.decision === 'allowed' ? 'success' : 'neutral'" data-test="mcp-audit-decision">{{
              t(`settings.mcp.audit.decisions.${entry.decision}`)
            }}</OnChip>
          </div>
          <div class="mt-4px break-all font-mono text-12px text-ink-secondary">{{ entry.object }}</div>
          <div class="mt-4px text-ink-secondary">{{ auditCaller(entry.caller) }}</div>
          <div v-if="entry.caller.mayComeFrom" class="text-ink-tertiary">
            {{ t('settings.mcp.audit.mayComeFrom', { name: entry.caller.mayComeFrom }) }}
          </div>
          <div v-if="entry.caller.claimedName" class="text-ink-tertiary">
            {{ t('settings.mcp.audit.claims', { name: entry.caller.claimedName }) }}
          </div>
          <div class="mt-4px flex flex-wrap justify-between gap-8px text-11.5px text-ink-tertiary">
            <span>{{
              entry.result ? t('settings.mcp.audit.result', { result: entry.result }) : t('settings.mcp.audit.noResult')
            }}</span>
            <time :datetime="new Date(entry.at).toISOString()">{{ auditTime(entry.at) }}</time>
          </div>
        </li>
      </ul>
    </OnModal>

    <OnConfirm
      v-model:open="clearOpen"
      tone="danger"
      :title="t('settings.mcp.audit.clearTitle')"
      :description="t('settings.mcp.audit.clearDescription')"
      :confirm-label="t('settings.mcp.audit.clearConfirm')"
      :loading="clearing"
      @confirm="clearAudit"
    />
  </div>
</template>
