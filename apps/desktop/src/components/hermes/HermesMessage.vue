<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { RouterLink } from 'vue-router';
import { OnAppIcon, OnButton, OnChip, OnGetButton, OnIcon, OnModal } from '@opennavo/ui';
import { OnMarkdown } from '@opennavo/ui/markdown';
import { formatCount } from '@opennavo/shared';
import HermesMark from './HermesMark.vue';
import HermesMediaCard from './HermesMediaCard.vue';
import HermesCopyButton from './HermesCopyButton.vue';
import { useHermesContent } from '@/composables/useHermesContent';
import { useAppLocale } from '@/composables/useAppLocale';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { useHermesStore } from '@/stores/hermes';
import { useTasksStore } from '@/stores';
import { commands, unwrap } from '@/ipc/client';
import { mediaKind, mediaName, parseHermesMedia, type HermesContentPart, type HermesMediaKind } from '@/hermes/media';
import type { HermesDecision, HermesMessage } from '@/stores/hermes';

const props = defineProps<{ message: HermesMessage }>();

const content = useHermesContent();
const hermes = useHermesStore();
const tasks = useTasksStore();
const pkgState = usePackageState();
const toasts = useToasts();
const { appLocale } = useAppLocale();

const confirmOpen = ref(false);
const demoCards = import.meta.env.MODE === 'web';
interface MessageAttachment {
  path: string;
  name: string;
  kind: HermesMediaKind;
  image: boolean;
}
const attachmentPreviews = ref<Record<string, string>>({});
const previewAttachment = ref<MessageAttachment | null>(null);
let attachmentPreviewRequest = 0;
const pkg = computed(() => props.message.package ?? null);
const packageState = computed(() =>
  pkg.value ? pkgState.stateOf(pkg.value.kind, pkg.value.token, pkg.value.disabled) : null
);
function messageText(key?: string): string {
  return key ? ((content.value.messages as Record<string, string>)[key.replace(/^messages\./, '')] ?? key) : '';
}

function toolText(key: string): string {
  return (content.value.tools as Record<string, string>)[key] ?? key;
}

const text = computed(() => messageText(props.message.textKey) || props.message.text || '');
const attachments = computed<MessageAttachment[]>(() =>
  (props.message.attachments ?? []).map(path => {
    const kind = mediaKind(path);
    return { path, name: mediaName(path), kind, image: kind === 'image' };
  })
);
const contentParts = computed<HermesContentPart[]>(() =>
  parseHermesMedia(text.value, props.message.streaming === true)
);
const mediaRemote = computed(() => hermes.panelActive);
const mediaProfileId = computed(() => (mediaRemote.value ? hermes.gatewayProfileId : null));
const mediaProfile = computed(() => hermes.active?.panelProfile?.trim() || hermes.activeAgentProfileName || null);
const mediaSessionId = computed(() => hermes.mediaSessionId);
const mediaCwd = computed(() => (mediaRemote.value ? null : hermes.workingDir.trim() || null));
const foot = computed(() => messageText(props.message.footKey));
const time = computed(() =>
  new Date(props.message.at).toLocaleTimeString(appLocale.value, { hour: '2-digit', minute: '2-digit' })
);
const toolCount = computed(() =>
  content.value.tools.calls.replace('{count}', String(props.message.tools?.length ?? 0))
);
const packageNote = computed(() => {
  if (!packageState.value) return '';
  if (packageState.value.state === 'running') return content.value.cards.package.installing;
  if (packageState.value.state === 'open' || packageState.value.state === 'installed')
    return content.value.cards.package.doneNote;
  if (hermes.mac.confirmWrites) return content.value.cards.package.confirmNote;
  return content.value.cards.package.note;
});

const updates = [
  { token: 'codex', name: 'Codex', version: '0.167.0', accent: '#111111' },
  { token: 'claude-code', name: 'Claude Code', version: '2.1.311', accent: '#DA7757' },
  { token: 'figma', name: 'Figma', version: '126.11.8', accent: '#2DCABD' },
  { token: 'raycast', name: 'Raycast', version: '2.11.1', accent: '#7B4646' }
];

function runPackageAction() {
  if (!pkg.value || !packageState.value) return;
  void pkgState.act(pkg.value.kind, pkg.value.token, packageState.value.state);
}

function onPackageAction() {
  if (!pkg.value || !packageState.value) return;
  if (!hermes.mac.enabled) {
    toasts.push({
      tone: 'warning',
      title: content.value.common.macPaused,
      description: content.value.common.macPausedBody
    });
    return;
  }
  if (hermes.mac.confirmWrites) {
    confirmOpen.value = true;
    return;
  }
  runPackageAction();
}

function onPackageCancel() {
  if (!pkg.value) return;
  const task = tasks.forPackage(pkg.value.kind, pkg.value.token);
  if (task) void tasks.cancel(task.id);
}

/** One transcript row per answer the user gave to an approval, clarify or secret card. */
function decisionLabel(decision: HermesDecision): string {
  const copy = content.value.ask.decision;
  if (decision.kind === 'clarify' && decision.outcome === 'submitted') {
    return `${copy.answered} · ${copy.questions.replace('{count}', String(decision.count ?? 0))}`;
  }
  if (decision.outcome === 'allowed') {
    if (decision.scope === 'always') return copy.allowedAlways;
    if (decision.scope === 'session') return copy.allowedSession;
    return copy.allowedOnce;
  }
  if (decision.outcome === 'denied') return copy.denied;
  if (decision.outcome === 'skipped') return copy.skipped;
  if (decision.outcome === 'timeout') return copy.timeout;
  if (decision.outcome === 'submitted') return copy.answered;
  return copy.cancelled;
}

function decisionIcon(decision: HermesDecision): string {
  if (decision.outcome === 'allowed' || decision.outcome === 'submitted') return 'lucide:check';
  if (decision.outcome === 'timeout') return 'lucide:clock';
  if (decision.outcome === 'skipped' || decision.outcome === 'cancelled') return 'lucide:minus';
  return 'lucide:x';
}

function decisionTone(decision: HermesDecision): string {
  if (decision.outcome === 'allowed' || decision.outcome === 'submitted') return 'text-status-success';
  if (decision.outcome === 'denied') return 'text-status-danger';
  return 'text-ink-tertiary';
}

function attachmentIcon(kind: HermesMediaKind): string {
  if (kind === 'image') return 'lucide:image';
  if (kind === 'video') return 'lucide:film';
  if (kind === 'audio') return 'lucide:music';
  return 'lucide:paperclip';
}

async function loadAttachmentPreviews() {
  if (!(typeof window !== 'undefined' && '__TAURI_INTERNALS__' in window)) return;
  const request = ++attachmentPreviewRequest;
  for (const file of attachments.value) {
    if (!file.image || attachmentPreviews.value[file.path] !== undefined) continue;
    try {
      const preview = await unwrap(commands.hermesAttachmentPreview(file.path));
      if (request !== attachmentPreviewRequest) return;
      if (preview) attachmentPreviews.value = { ...attachmentPreviews.value, [file.path]: preview };
    } catch {
      // A preview is optional; the attachment chip still names the file.
    }
  }
}

async function openAttachmentPath(path: string) {
  try {
    const { openPath } = await import('@tauri-apps/plugin-opener');
    await openPath(path);
  } catch (error) {
    toasts.push({
      tone: 'warning',
      title: content.value.media.openFailed,
      description: error instanceof Error ? error.message : String(error)
    });
  }
}

function openUserAttachment(file: MessageAttachment) {
  if (file.image && attachmentPreviews.value[file.path]) {
    previewAttachment.value = file;
    return;
  }
  void openAttachmentPath(file.path);
}

watch(() => props.message.attachments, loadAttachmentPreviews, { immediate: true });
</script>

<template>
  <div v-if="message.role === 'user'" class="flex justify-end">
    <div class="flex max-w-[82%] flex-col items-end gap-5px">
      <div
        class="max-w-full rounded-[14px_14px_4px_14px] border border-solid px-14px py-10px text-13.5px leading-[1.65] text-ink-primary"
        :class="message.steering ? 'border-line-default bg-surface-card' : 'border-line-subtle bg-surface-raised'"
      >
        <div v-if="message.steering" class="mb-6px flex items-center gap-7px">
          <span
            class="inline-flex h-18px items-center rounded-full bg-status-warning-subtle px-8px text-10px font-600 leading-none text-status-warning"
          >
            {{ content.steering }}
          </span>
          <span class="h-px flex-1 bg-line-subtle" aria-hidden="true"></span>
        </div>
        <div v-if="attachments.length" class="mb-7px flex flex-wrap items-start gap-6px">
          <button
            v-for="file in attachments"
            :key="file.path"
            type="button"
            class="m-0 max-w-full overflow-hidden rounded-small border border-solid border-line-subtle bg-surface-control p-0 text-left outline-none transition-colors hover:border-line-strong focus-visible:shadow-focus-ring"
            @click="openUserAttachment(file)"
          >
            <img
              v-if="file.image && attachmentPreviews[file.path]"
              :src="attachmentPreviews[file.path]"
              :alt="file.name"
              class="block max-h-96px max-w-180px object-cover"
            />
            <span
              v-else
              class="inline-flex max-w-full items-center gap-4px bg-surface-control px-7px py-4px text-11px text-ink-secondary"
            >
              <OnIcon :name="attachmentIcon(file.kind)" :size="11" class="shrink-0 text-ink-tertiary" />
              <span class="min-w-0 truncate">{{ file.name }}</span>
            </span>
          </button>
        </div>
        <div v-if="text" class="whitespace-pre-wrap">{{ text }}</div>
      </div>
      <HermesCopyButton v-if="text && !message.streaming" :text="text" />
    </div>
  </div>

  <div v-else class="flex items-start gap-12px">
    <HermesMark :size="26" class="mt-1px" />
    <div class="flex min-w-0 flex-1 flex-col gap-12px">
      <div class="flex items-center gap-8px text-11px text-ink-tertiary">
        <b class="text-12.5px font-600 text-ink-primary">Hermes</b>
        <span>{{ time }}</span>
      </div>

      <details
        v-if="message.reasoning"
        class="group overflow-hidden rounded-small border border-solid border-line-subtle bg-surface-card"
      >
        <summary
          class="flex h-32px cursor-pointer list-none items-center gap-8px px-10px text-11px text-ink-tertiary outline-none focus-visible:shadow-focus-ring"
        >
          <OnIcon name="lucide:brain" :size="13" />
          {{ content.composer.reasoning }}
          <OnIcon name="lucide:chevron-down" :size="13" class="ml-auto transition-transform group-open:rotate-180" />
        </summary>
        <pre
          class="m-0 max-h-220px overflow-auto whitespace-pre-wrap break-words border-t border-t-solid border-line-subtle px-10px py-8px font-sans text-11px leading-[1.55] text-ink-tertiary"
          >{{ message.reasoning }}</pre
        >
      </details>

      <details
        v-if="message.tools?.length"
        class="group overflow-hidden rounded-small border border-solid border-line-subtle bg-surface-card"
      >
        <summary
          class="flex h-32px cursor-pointer list-none items-center gap-8px px-10px text-11px text-ink-tertiary outline-none focus-visible:shadow-focus-ring"
        >
          <OnIcon name="lucide:square-terminal" :size="13" />
          {{ toolCount }}
          <OnIcon name="lucide:chevron-down" :size="13" class="ml-auto transition-transform group-open:rotate-180" />
        </summary>
        <div class="flex flex-col gap-7px border-t border-t-solid border-line-subtle px-10px pb-9px pt-8px">
          <div
            v-for="(call, index) in message.tools"
            :key="call.toolId ?? `${call.name}-${index}`"
            class="flex flex-col gap-5px"
          >
            <div class="flex items-center gap-8px text-11px text-ink-tertiary">
              <code class="rounded-tiny bg-surface-inset px-5px py-2px font-mono text-11px text-ink-secondary">{{
                call.name
              }}</code>
              <span v-if="call.label">{{ toolText(call.label) }}</span>
              <span class="ml-auto tabular-nums">{{ call.time }}</span>
              <OnIcon
                v-if="call.error"
                name="lucide:circle-alert"
                :size="12"
                class="text-status-danger"
                :aria-label="content.tools.failed"
              />
              <OnIcon
                v-else-if="call.pending"
                name="lucide:loader-circle"
                :size="12"
                class="animate-spin text-ink-tertiary"
                :aria-label="content.tools.running"
              />
              <OnIcon v-else name="lucide:check" :size="12" class="text-status-success" />
            </div>
            <details
              v-if="call.args || call.result"
              class="group/call overflow-hidden rounded-tiny border border-solid border-line-subtle"
            >
              <summary
                class="flex h-26px cursor-pointer list-none items-center gap-6px px-8px text-10.5px text-ink-tertiary outline-none focus-visible:shadow-focus-ring"
              >
                {{ content.tools.inspect }}
                <OnIcon
                  name="lucide:chevron-down"
                  :size="12"
                  class="ml-auto transition-transform group-open/call:rotate-180"
                />
              </summary>
              <div class="flex flex-col gap-6px border-t border-t-solid border-line-subtle px-8px py-7px">
                <div v-if="call.args" class="flex flex-col gap-3px">
                  <span class="text-9.5px font-600 uppercase tracking-[0.06em] text-ink-tertiary">
                    {{ content.tools.arguments }}
                  </span>
                  <pre
                    class="m-0 max-h-180px overflow-auto whitespace-pre-wrap break-words font-mono text-10.5px leading-[1.5] text-ink-secondary"
                    >{{ call.args }}</pre
                  >
                </div>
                <div v-if="call.result" class="flex flex-col gap-3px">
                  <span class="text-9.5px font-600 uppercase tracking-[0.06em] text-ink-tertiary">
                    {{ content.tools.result }}
                  </span>
                  <pre
                    class="m-0 max-h-220px overflow-auto whitespace-pre-wrap break-words font-mono text-10.5px leading-[1.5]"
                    :class="call.error ? 'text-status-danger' : 'text-ink-secondary'"
                    >{{ call.result }}</pre
                  >
                </div>
              </div>
            </details>
          </div>
        </div>
      </details>

      <!--
        What the user allowed, denied or answered during this turn: a granted command stays
        visible after the fact, and a secret only ever names its variable.
      -->
      <div v-if="message.decisions?.length" class="flex flex-col gap-4px">
        <div
          v-for="(decision, index) in message.decisions"
          :key="`${decision.requestId}-${index}`"
          class="flex items-center gap-6px text-11px leading-[1.5] text-ink-tertiary"
        >
          <OnIcon :name="decisionIcon(decision)" :size="12" :class="decisionTone(decision)" />
          <span class="font-500">{{ decisionLabel(decision) }}</span>
          <code
            v-if="decision.summary"
            class="max-w-260px truncate rounded-tiny bg-surface-inset px-5px py-1px font-mono text-10.5px text-ink-tertiary"
            >{{ decision.summary }}</code
          >
        </div>
      </div>

      <div class="min-w-0" aria-live="polite">
        <div v-if="message.streaming && !text" class="flex items-center gap-8px pt-1px text-11px text-ink-tertiary">
          <span class="h-5px w-5px animate-pulse rounded-full bg-brand-coral"></span>
          {{ content.composer.thinking }}
        </div>
        <div v-else-if="text" class="flex min-w-0 flex-col gap-8px">
          <template v-for="(part, index) in contentParts" :key="`${message.id}-part-${index}`">
            <OnMarkdown v-if="part.type === 'text'" :source="part.text" size="sm" :heading-level="4" />
            <HermesMediaCard
              v-else
              :path="part.path"
              :kind="part.kind"
              :remote="mediaRemote"
              :profile-id="mediaProfileId"
              :profile="mediaProfile"
              :session-id="mediaSessionId"
              :cwd="mediaCwd"
            />
          </template>
        </div>
        <span
          v-if="message.streaming && text"
          class="ml-2px inline-block h-13px w-7px animate-pulse rounded-2px bg-ink-secondary align-[-2px]"
        />
      </div>

      <div
        v-if="demoCards && pkg && packageState"
        class="flex flex-col gap-12px rounded-big border border-solid border-line-subtle bg-surface-card p-14px"
      >
        <div class="flex items-start gap-12px">
          <OnAppIcon
            :kind="pkg.kind"
            :token="pkg.token"
            :name="pkg.displayName"
            :src="pkg.iconUrl"
            :accent="pkg.accentColor"
            :size="44"
          />
          <div class="min-w-0 flex-1">
            <div class="flex items-center gap-7px">
              <b class="text-15px font-600 tracking-[-0.01em] text-ink-primary">{{ pkg.displayName }}</b>
              <OnChip tone="success">{{ content.cards.package.free }}</OnChip>
            </div>
            <p v-if="pkg.summary" class="m-0 mt-3px text-12px leading-[1.55] text-ink-tertiary">
              {{ content.cards.package.summary }}
            </p>
          </div>
        </div>
        <div class="flex gap-22px border-t border-t-solid border-line-subtle pt-12px">
          <div>
            <span class="block text-11px text-ink-tertiary">{{ content.cards.package.version }}</span>
            <span class="mt-2px block text-12.5px font-600 tabular-nums text-ink-primary">{{ pkg.version }}</span>
          </div>
          <div>
            <span class="block text-11px text-ink-tertiary">{{ content.cards.package.installs }}</span>
            <span class="mt-2px block text-12.5px font-600 tabular-nums text-ink-primary">{{
              formatCount(pkg.installs30d, { locale: appLocale })
            }}</span>
          </div>
          <div>
            <span class="block text-11px text-ink-tertiary">{{ content.cards.package.category }}</span>
            <span class="mt-2px block text-12.5px font-600 text-ink-primary">{{
              content.cards.package.categoryValue
            }}</span>
          </div>
        </div>
        <div class="flex items-center gap-8px">
          <span class="inline-flex min-w-0 items-center gap-6px text-11px leading-[1.4] text-ink-tertiary">
            <OnIcon name="lucide:shield-check" :size="12" />
            {{ packageNote }}
          </span>
          <div class="ml-auto flex items-center gap-8px">
            <RouterLink
              :to="`/package/${pkg.kind}/${pkg.token}`"
              class="inline-flex h-30px items-center rounded-small border border-solid border-line-default px-12px text-12px font-600 text-ink-secondary no-underline outline-none transition-colors hover:border-line-strong hover:text-ink-primary focus-visible:shadow-focus-ring"
            >
              {{ content.cards.package.details }}
            </RouterLink>
            <OnGetButton
              :state="packageState.state"
              :progress="packageState.progress"
              appearance="button"
              @click="onPackageAction"
              @cancel="onPackageCancel"
            />
          </div>
        </div>
      </div>

      <div
        v-if="demoCards && message.card === 'cleanup'"
        class="flex flex-col gap-12px rounded-big border border-solid border-line-subtle bg-surface-card p-14px"
      >
        <div class="flex items-start gap-12px">
          <div>
            <span class="block text-11px text-ink-tertiary">{{ content.cards.cleanup.kicker }}</span>
            <b class="mt-2px block text-24px leading-[1.1] tracking-[-0.025em] text-ink-primary">{{
              content.cards.cleanup.size
            }}</b>
          </div>
          <OnChip tone="accent" class="ml-auto">{{ content.cards.cleanup.needConfirm }}</OnChip>
        </div>
        <div class="flex h-8px gap-2px overflow-hidden rounded-full bg-surface-inset">
          <i class="block h-full w-[55%] bg-status-info"></i>
          <i class="block h-full w-[25%] bg-brand-salmon"></i>
          <i class="block h-full w-[20%] bg-brand-coral"></i>
        </div>
        <div class="flex flex-col gap-8px">
          <div class="flex items-center gap-8px text-11.5px text-ink-secondary">
            <i class="h-8px w-8px rounded-[2px] bg-status-info"></i>{{ content.cards.cleanup.caches
            }}<span class="ml-auto font-600 tabular-nums text-ink-primary">{{ content.cards.cleanup.cachesSize }}</span>
          </div>
          <div class="flex items-center gap-8px text-11.5px text-ink-secondary">
            <i class="h-8px w-8px rounded-[2px] bg-brand-salmon"></i>{{ content.cards.cleanup.leftovers
            }}<span class="ml-auto font-600 tabular-nums text-ink-primary">{{
              content.cards.cleanup.leftoversSize
            }}</span>
          </div>
          <div class="flex items-center gap-8px text-11.5px text-ink-secondary">
            <i class="h-8px w-8px rounded-[2px] bg-brand-coral"></i>{{ content.cards.cleanup.installers
            }}<span class="ml-auto font-600 tabular-nums text-ink-primary">{{
              content.cards.cleanup.installersSize
            }}</span>
          </div>
        </div>
        <div class="flex items-center gap-8px">
          <span class="inline-flex items-center gap-6px text-11px text-ink-tertiary"
            ><OnIcon name="lucide:trash" :size="12" />{{ content.cards.cleanup.note }}</span
          >
          <div class="ml-auto flex items-center gap-8px">
            <RouterLink
              to="/cleanup"
              class="inline-flex h-30px items-center rounded-small border border-solid border-line-default px-12px text-12px font-600 text-ink-secondary no-underline outline-none hover:border-line-strong hover:text-ink-primary focus-visible:shadow-focus-ring"
              >{{ content.cards.cleanup.review }}</RouterLink
            >
            <RouterLink
              to="/cleanup"
              class="inline-flex h-30px items-center rounded-small bg-button-accent-bg px-12px text-12px font-600 text-button-accent-text no-underline outline-none hover:bg-button-accent-hover focus-visible:shadow-focus-ring"
              >{{ content.cards.cleanup.run }}</RouterLink
            >
          </div>
        </div>
      </div>

      <div
        v-if="demoCards && message.card === 'updates'"
        class="flex flex-col gap-12px rounded-big border border-solid border-line-subtle bg-surface-card p-14px"
      >
        <div class="flex items-start gap-12px">
          <div>
            <span class="block text-11px text-ink-tertiary">{{ content.cards.updates.kicker }}</span>
            <b class="mt-2px block text-15px font-600 text-ink-primary">{{ content.cards.updates.total }}</b>
          </div>
          <OnChip tone="accent" class="ml-auto">{{ updates.length }}</OnChip>
        </div>
        <div class="flex flex-col">
          <div
            v-for="item in updates"
            :key="item.token"
            class="flex items-center gap-10px border-b border-b-solid border-line-subtle py-9px last:border-b-0"
          >
            <OnAppIcon kind="cask" :token="item.token" :name="item.name" :src="null" :accent="item.accent" :size="30" />
            <span class="min-w-0 flex-1"
              ><b class="block text-12.5px font-600 text-ink-primary">{{ item.name }}</b
              ><span class="mt-1px block font-mono text-11px text-ink-tertiary">{{ item.version }}</span></span
            >
          </div>
        </div>
        <div class="flex items-center gap-8px">
          <span class="inline-flex items-center gap-6px text-11px text-ink-tertiary"
            ><OnIcon name="lucide:info" :size="12" />{{ content.cards.updates.note }}</span
          >
          <div class="ml-auto flex items-center gap-8px">
            <RouterLink
              to="/updates"
              class="inline-flex h-30px items-center rounded-small border border-solid border-line-default px-12px text-12px font-600 text-ink-secondary no-underline outline-none hover:border-line-strong hover:text-ink-primary focus-visible:shadow-focus-ring"
              >{{ content.cards.updates.review }}</RouterLink
            >
            <RouterLink
              to="/updates"
              class="inline-flex h-30px items-center rounded-small bg-button-accent-bg px-12px text-12px font-600 text-button-accent-text no-underline outline-none hover:bg-button-accent-hover focus-visible:shadow-focus-ring"
              >{{ content.cards.updates.run }}</RouterLink
            >
          </div>
        </div>
      </div>

      <div
        v-if="demoCards && message.card === 'explain'"
        class="flex flex-col gap-12px rounded-big border border-solid border-line-subtle bg-surface-card p-14px"
      >
        <div class="flex items-start gap-12px">
          <OnAppIcon kind="cask" token="opennavo" name="OpenNavo" :src="null" accent="#FF7356" :size="40" />
          <div class="min-w-0 flex-1">
            <div class="flex items-center gap-7px">
              <b class="text-15px font-600 text-ink-primary">{{ content.cards.explain.title }}</b
              ><OnChip tone="neutral">{{ content.cards.explain.current }}</OnChip>
            </div>
            <p class="m-0 mt-3px text-12px leading-[1.55] text-ink-tertiary">{{ content.cards.explain.body }}</p>
          </div>
        </div>
        <div class="flex items-center gap-8px">
          <span class="text-11px text-ink-tertiary">{{ content.cards.explain.noCopy }}</span>
          <RouterLink
            to="/discover"
            class="ml-auto inline-flex h-30px items-center rounded-small border border-solid border-line-default px-12px text-12px font-600 text-ink-secondary no-underline outline-none hover:border-line-strong hover:text-ink-primary focus-visible:shadow-focus-ring"
            >{{ content.cards.explain.back }}</RouterLink
          >
        </div>
      </div>

      <HermesCopyButton v-if="text && !message.streaming" :text="text" class="-mt-5px self-start" />

      <p v-if="foot" class="m-0 text-11px leading-[1.5] text-ink-tertiary">{{ foot }}</p>
    </div>
  </div>

  <OnModal
    :open="confirmOpen"
    :title="content.cards.package.confirmTitle"
    :description="content.cards.package.confirmBody"
    size="md"
    role="alertdialog"
    initial-focus="[data-hermes-install-cancel]"
    @close="confirmOpen = false"
  >
    <div
      class="flex items-center gap-8px rounded-small border border-solid border-line-subtle bg-surface-inset px-12px py-10px font-mono text-11.5px text-ink-secondary"
    >
      <OnIcon name="lucide:square-terminal" :size="14" />
      {{ content.cards.package.command }}
    </div>
    <ul class="m-0 mt-12px flex list-none flex-col gap-7px p-0 text-11.5px leading-[1.5] text-ink-tertiary">
      <li class="flex items-start gap-7px">
        <OnIcon name="lucide:shield-check" :size="12" class="mt-1px" />{{ content.cards.package.modalNote }}
      </li>
    </ul>
    <template #footer>
      <OnButton variant="ghost" data-hermes-install-cancel @click="confirmOpen = false">{{
        content.common.cancel
      }}</OnButton>
      <OnButton
        variant="accent"
        @click="
          runPackageAction();
          confirmOpen = false;
        "
        >{{ content.cards.package.confirmRun }}</OnButton
      >
    </template>
  </OnModal>

  <OnModal
    :open="Boolean(previewAttachment)"
    :title="previewAttachment?.name ?? ''"
    size="lg"
    @close="previewAttachment = null"
  >
    <img
      v-if="previewAttachment"
      :src="attachmentPreviews[previewAttachment.path]"
      :alt="previewAttachment.name"
      class="mx-auto block max-h-[70vh] max-w-full object-contain"
    />
    <template #footer>
      <OnButton variant="ghost" @click="previewAttachment && openAttachmentPath(previewAttachment.path)">
        {{ content.media.open }}
      </OnButton>
    </template>
  </OnModal>
</template>
