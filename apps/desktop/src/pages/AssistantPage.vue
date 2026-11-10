<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { OnButton, OnChip, OnIcon, OnModal } from '@opennavo/ui';
import HermesMark from '@/components/hermes/HermesMark.vue';
import HermesMessage from '@/components/hermes/HermesMessage.vue';
import AssistantHistory from '@/components/hermes/AssistantHistory.vue';
import HermesSettings from '@/components/hermes/HermesSettings.vue';
import { useHermesStore } from '@/stores/hermes';
import { commands, unwrap } from '@/ipc/client';
import { useHermesUiStore } from '@/stores/hermesUi';
import { useHermesContent } from '@/composables/useHermesContent';
import { useToasts } from '@/composables/useToasts';

const store = useHermesStore();
const ui = useHermesUiStore();
const content = useHermesContent();
const toasts = useToasts();

const input = ref('');
const historyOpen = ref(false);
const installOpen = ref(false);
const contextOpen = ref(false);
const attachments = ref<{ path: string; name: string; isImage: boolean; size?: number }[]>([]);
const MAX_ATTACHMENT_BYTES = 50 * 1024 * 1024;
const attachmentPreviews = ref<Record<string, string>>({});
const fileInput = ref<HTMLInputElement>();
const scroll = ref<HTMLElement>();
const composer = ref<HTMLTextAreaElement>();
const dragging = ref(false);
let unlistenDrop: (() => void) | undefined;
const SUGGESTIONS = ['rectangle', 'cleanup', 'updates', 'explain'] as const;

const modelLabel = computed(
  () => `${content.value.header.model} · ${store.modelShort} · ${content.value.effort[store.model.effort]}`
);

const macLabel = computed(() => {
  if (!store.mac.enabled) return content.value.header.macPaused;
  if (store.mac.level === 'open') return content.value.header.openOnly;
  return content.value.header.mac;
});

const macTone = computed<'success' | 'neutral'>(() =>
  store.mac.enabled && store.mac.level !== 'open' ? 'success' : 'neutral'
);

function enableMac() {
  // Header shortcut only restores the existing policy. It must not silently
  // enable every capability or turn off write confirmation.
  store.saveMac({ ...store.mac, enabled: true });
  toasts.push({
    tone: 'success',
    title: content.value.common.settingsSaved,
    description: content.value.settings.mac.masterHint
  });
}

function scrollToBottom() {
  void nextTick(() => {
    const element = scroll.value;
    if (element) element.scrollTo({ top: element.scrollHeight, behavior: 'smooth' });
  });
}

const directoryLabel = computed(() => {
  const dir = store.workingDir.trim();
  if (!dir) return content.value.composer.chooseDirectory;
  return dir.split('/').filter(Boolean).at(-1) ?? dir;
});

function addAttachment(path: string, name?: string, size?: number, image?: boolean) {
  const displayName = name || path.split('/').filter(Boolean).at(-1) || path;
  if (size && size > MAX_ATTACHMENT_BYTES) {
    toasts.push({ tone: 'warning', title: content.value.composer.attach, description: `${displayName} > 50 MB` });
    return;
  }
  const isImage = image ?? /\.(png|jpe?g|gif|webp|heic|heif|bmp|tiff)$/i.test(displayName);
  if (!attachments.value.some(item => item.path === path)) {
    attachments.value.push({ path, name: displayName, isImage, size });
    if (isImage) void loadAttachmentPreview(path);
  }
}

async function addAttachmentPaths(paths: string[]) {
  if (!paths.length) return;
  try {
    const info = await commands.hermesAttachmentInfo(paths);
    info.forEach(item => addAttachment(item.path, item.name, item.size, item.isImage));
  } catch {
    paths.forEach(path => addAttachment(path));
  }
}

async function loadAttachmentPreview(path: string) {
  if (!(typeof window !== 'undefined' && '__TAURI_INTERNALS__' in window)) return;
  try {
    const preview = await unwrap(commands.hermesAttachmentPreview(path));
    if (preview) attachmentPreviews.value = { ...attachmentPreviews.value, [path]: preview };
  } catch {
    // Preview is optional; the attachment still sends.
  }
}

function attachmentSrc(path: string) {
  return attachmentPreviews.value[path] ?? '';
}

async function openAttachment(path: string) {
  if (!(typeof window !== 'undefined' && '__TAURI_INTERNALS__' in window)) return;
  try {
    const { openPath } = await import('@tauri-apps/plugin-opener');
    await openPath(path);
  } catch (error) {
    toasts.push({ tone: 'warning', title: content.value.composer.attach, description: String(error) });
  }
}

async function pickFiles() {
  if (typeof window !== 'undefined' && '__TAURI_INTERNALS__' in window) {
    try {
      const { open } = await import('@tauri-apps/plugin-dialog');
      const selected = await open({ multiple: true, directory: false });
      const paths = Array.isArray(selected) ? selected : selected ? [selected] : [];
      await addAttachmentPaths(paths);
    } catch (error) {
      toasts.push({ tone: 'warning', title: content.value.composer.attach, description: String(error) });
    }
    return;
  }
  fileInput.value?.click();
}

async function onPaste(event: ClipboardEvent) {
  if (!(typeof window !== 'undefined' && '__TAURI_INTERNALS__' in window)) return;
  if (!event.clipboardData?.getData('text/plain')) {
    try {
      const path = await unwrap(commands.hermesClipboardImage());
      if (path) {
        event.preventDefault();
        await addAttachmentPaths([path]);
      }
    } catch {
      // Clipboard images are optional.
    }
  }
}

function onFileInput(event: Event) {
  const target = event.target as HTMLInputElement;
  // The browser input has no native path. Tauri uses the dialog path instead.
  toasts.push({
    tone: 'warning',
    title: content.value.composer.attach,
    description: 'Use the desktop app file picker to attach local files.'
  });
  target.value = '';
}

function removeAttachment(index: number) {
  attachments.value.splice(index, 1);
}

async function pickDirectory() {
  if (!(typeof window !== 'undefined' && '__TAURI_INTERNALS__' in window)) return;
  try {
    const { open } = await import('@tauri-apps/plugin-dialog');
    const selected = await open({ directory: true, multiple: false });
    if (typeof selected === 'string') {
      const previous = store.workingDir;
      store.setWorkingDir(selected);
      if (previous !== selected && store.activeId) {
        toasts.push({
          tone: 'info',
          title: content.value.composer.chooseDirectory,
          description: content.value.composer.directoryChanged
        });
      }
    }
  } catch (error) {
    toasts.push({ tone: 'warning', title: content.value.composer.chooseDirectory, description: String(error) });
  }
}

function send() {
  const value = input.value.trim();
  const files = attachments.value.map(item => item.path);
  if (!value && !files.length) return;
  if (store.stopping) return;
  if (store.thinking && files.length) {
    toasts.push({
      tone: 'warning',
      title: content.value.composer.attach,
      description: content.value.composer.attachmentsBusy
    });
    return;
  }
  store.send(value, files);
  input.value = '';
  attachments.value = [];
  scrollToBottom();
}

function onEnter(event: KeyboardEvent) {
  if (event.isComposing || event.shiftKey) return;
  event.preventDefault();
  send();
}

function suggestion(prompt: string) {
  store.send(prompt);
  scrollToBottom();
}

function newChat() {
  store.stopThinking();
  store.newChat();
  historyOpen.value = false;
  input.value = '';
  attachments.value = [];
  void nextTick(() => composer.value?.focus());
}

function selectConversation(id: string) {
  store.stopThinking();
  store.select(id);
  attachments.value = [];
  historyOpen.value = false;
  void nextTick(() => {
    scroll.value?.scrollTo({ top: 0 });
  });
}

const contextPercent = computed(() => {
  const context = store.contextUsage;
  return context ? Math.min(100, Math.max(0, Math.round(context.percent))) : 0;
});
const contextColor = computed(() => {
  if (!store.contextUsage) return 'var(--on-text-tertiary)';
  if (contextPercent.value >= 85) return 'var(--on-status-danger)';
  if (contextPercent.value >= 60) return 'var(--on-status-warning)';
  return 'var(--on-status-success)';
});
const contextKnown = computed(() => Boolean(store.contextUsage));
const contextRadius = 7;
const contextCircumference = 2 * Math.PI * contextRadius;
const contextDashoffset = computed(
  () => contextCircumference * (1 - Math.min(100, Math.max(0, contextPercent.value)) / 100)
);
function formatTokens(value: number) {
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(value % 1_000_000 ? 1 : 0)}m`;
  if (value >= 1_000) return `${Math.round(value / 1_000)}k`;
  return String(value);
}
const contextUsedText = computed(() => formatTokens(store.contextUsage?.used ?? 0));
const contextMaxText = computed(() => formatTokens(store.contextUsage?.max ?? 0));
const contextUsedLabel = computed(() => content.value.context.used.replace('{percent}', String(contextPercent.value)));
const contextTokensLabel = computed(() =>
  content.value.context.tokens.replace('{used}', contextUsedText.value).replace('{total}', contextMaxText.value)
);
const lastTextLength = computed(() => store.messages.at(-1)?.text?.length ?? 0);
const streamingAssistant = computed(() =>
  store.messages.some(message => message.role === 'assistant' && message.streaming === true)
);
watch(() => [store.messages.length, store.thinking, lastTextLength.value], scrollToBottom);
watch(
  () => store.activeId,
  () => {
    void nextTick(() => scroll.value?.scrollTo({ top: 0 }));
  }
);

async function installRuntime() {
  const ok = await store.installRuntime();
  if (!ok) return;
  installOpen.value = false;
  await nextTick();
  scrollToBottom();
  composer.value?.focus();
}

function closeInstall() {
  if (store.installing) return;
  installOpen.value = false;
}

function onKeydown(event: KeyboardEvent) {
  if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'y') {
    event.preventDefault();
    if (ui.settingsOpen) return;
    historyOpen.value = !historyOpen.value;
  }
}

onMounted(async () => {
  const history = store.loadHistory();
  await store.loadRuntime();
  await history;
  if (!store.runtimeReady) installOpen.value = true;
  window.addEventListener('keydown', onKeydown);
  window.addEventListener('paste', onPaste);
  if (typeof window !== 'undefined' && '__TAURI_INTERNALS__' in window) {
    try {
      const { getCurrentWebview } = await import('@tauri-apps/api/webview');
      unlistenDrop = await getCurrentWebview().onDragDropEvent(event => {
        if (event.payload.type === 'over') dragging.value = true;
        if (event.payload.type === 'leave') dragging.value = false;
        if (event.payload.type === 'drop') {
          dragging.value = false;
          void addAttachmentPaths(event.payload.paths);
        }
      });
    } catch {
      // Native drag/drop is an enhancement; the file picker still works.
    }
  }
  scrollToBottom();
  void nextTick(() => composer.value?.focus());
});
onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKeydown);
  window.removeEventListener('paste', onPaste);
  unlistenDrop?.();
});
</script>

<template>
  <div class="relative flex h-[calc(100vh-86px)] min-h-0 flex-col">
    <header class="flex h-64px shrink-0 items-center gap-10px border-b border-b-solid border-line-subtle">
      <div class="flex min-w-0 items-center gap-10px">
        <HermesMark :size="30" />
        <span class="text-17px font-600 tracking-[-0.015em] text-ink-primary">Hermes</span>
        <OnChip tone="neutral">{{ content.header.builtin }}</OnChip>
        <span class="inline-flex items-center gap-5px text-11px text-ink-tertiary"
          ><i
            class="h-6px w-6px rounded-full"
            :class="store.runtimeReady ? 'bg-status-success' : 'bg-status-warning'"
          ></i
          >{{ store.runtimeReady ? content.header.local : content.header.demo }}</span
        >
        <OnChip :tone="macTone" dot>{{ macLabel }}</OnChip>
        <OnButton v-if="!store.mac.enabled" variant="ghost" size="xs" icon="lucide:power" @click="enableMac">{{
          content.header.enableMac
        }}</OnButton>
      </div>
      <div class="ml-auto flex shrink-0 items-center gap-8px">
        <OnButton
          variant="ghost"
          size="sm"
          icon="lucide:clock"
          icon-only
          :aria-label="content.header.history"
          :aria-expanded="historyOpen"
          data-hermes-history-toggle
          :class="{ 'bg-surface-raised text-ink-primary': historyOpen }"
          @click="historyOpen = !historyOpen"
        />
        <button
          type="button"
          class="inline-flex h-28px items-center gap-6px rounded-full border border-solid border-line-default px-10px font-sans text-11.5px text-ink-secondary outline-none transition-colors hover:border-line-strong hover:text-ink-primary focus-visible:shadow-focus-ring"
          :title="`${content.header.model} · ${content.effort.label}`"
          @click="ui.openSettings('model')"
        >
          {{ modelLabel }}
          <OnIcon name="lucide:chevron-down" :size="12" />
        </button>
        <OnButton variant="ghost" size="sm" icon="lucide:plus" @click="newChat">{{ content.header.newChat }}</OnButton>
      </div>
    </header>

    <div
      v-if="!store.runtimeReady"
      class="flex min-h-0 flex-1 flex-col items-center justify-center px-24px pb-40px text-center"
    >
      <HermesMark :size="54" />
      <h2 class="m-0 mt-16px text-21px font-600 tracking-[-0.02em] text-ink-primary">
        {{ content.install.gateTitle }}
      </h2>
      <p class="m-0 mt-7px max-w-430px text-13px leading-[1.7] text-ink-secondary">{{ content.install.gateBody }}</p>
      <p class="m-0 mt-12px text-11px text-ink-tertiary">{{ content.install.size }}</p>
      <OnButton class="mt-20px" variant="accent" size="md" icon="lucide:download" @click="installOpen = true">{{
        content.install.gateButton
      }}</OnButton>
      <p
        v-if="store.runtime?.message"
        class="m-0 mt-12px max-w-480px break-words text-11px leading-[1.5] text-ink-tertiary"
      >
        {{ store.runtime.message }}
      </p>
    </div>

    <template v-else>
      <div ref="scroll" class="min-h-0 flex-1 overflow-y-auto py-18px">
        <div class="mx-auto flex w-full max-w-720px flex-col gap-22px">
          <div
            v-if="!store.messages.length && !store.thinking"
            class="flex flex-col items-center px-0 pb-18px pt-38px text-center"
          >
            <HermesMark :size="54" />
            <h2 class="m-0 mt-14px text-23px font-600 tracking-[-0.025em] text-ink-primary">
              {{ content.welcome.title }}
            </h2>
            <p class="m-0 mt-5px max-w-430px text-13px leading-[1.7] text-ink-secondary">{{ content.welcome.body }}</p>
            <div class="mt-24px grid w-full max-w-560px grid-cols-2 gap-8px">
              <button
                v-for="item in SUGGESTIONS"
                :key="item"
                type="button"
                class="flex min-w-0 items-start gap-10px rounded-default border border-solid border-line-subtle bg-surface-card p-12px text-left outline-none transition-colors hover:border-line-strong hover:bg-surface-raised focus-visible:shadow-focus-ring"
                @click="suggestion(content.welcome.suggestions[item].prompt)"
              >
                <span
                  class="flex h-24px w-24px shrink-0 items-center justify-center rounded-small bg-accent-coral-subtle text-ink-accent"
                >
                  <OnIcon
                    :name="
                      item === 'rectangle'
                        ? 'lucide:mouse-pointer-click'
                        : item === 'cleanup'
                          ? 'lucide:hard-drive'
                          : item === 'updates'
                            ? 'lucide:circle-arrow-down'
                            : 'lucide:app-window'
                    "
                    :size="14"
                  />
                </span>
                <span class="min-w-0 flex-1">
                  <b class="block text-12px font-600 leading-[1.4] text-ink-primary">{{
                    content.welcome.suggestions[item].title
                  }}</b>
                  <span class="mt-2px block text-11px leading-[1.45] text-ink-tertiary">{{
                    content.welcome.suggestions[item].sub
                  }}</span>
                </span>
                <OnIcon name="lucide:arrow-up-right" :size="13" class="mt-3px text-ink-tertiary" />
              </button>
            </div>
          </div>

          <HermesMessage v-for="message in store.messages" :key="message.id" :message="message" />

          <div v-if="store.thinking && !streamingAssistant" class="flex items-start gap-12px">
            <HermesMark :size="26" class="mt-1px" />
            <div class="flex items-center gap-8px pt-4px text-11px text-ink-tertiary">
              <span class="h-5px w-5px animate-pulse rounded-full bg-brand-coral"></span>
              {{ content.composer.thinking }}
            </div>
          </div>
        </div>
      </div>

      <div class="shrink-0 pb-16px pt-10px">
        <div class="mx-auto w-full max-w-720px">
          <form
            class="rounded-big border border-solid bg-surface-control px-10px pb-8px pt-9px transition-colors focus-within:border-line-strong"
            :class="dragging ? 'border-accent-coral bg-accent-coral-subtle' : 'border-line-default'"
            @submit.prevent="send"
          >
            <div v-if="attachments.length" class="flex flex-wrap gap-5px px-4px pb-7px">
              <span
                v-for="(file, index) in attachments"
                :key="file.path"
                class="inline-flex max-w-full cursor-pointer items-center gap-5px rounded-tiny border border-solid border-line-subtle bg-surface-raised px-7px py-4px text-11px text-ink-secondary hover:border-line-strong"
                @click="openAttachment(file.path)"
              >
                <img
                  v-if="file.isImage && attachmentSrc(file.path)"
                  :src="attachmentSrc(file.path)"
                  :alt="file.name"
                  class="h-22px w-22px shrink-0 rounded-3px object-cover"
                />
                <OnIcon
                  v-else
                  :name="file.isImage ? 'lucide:image' : 'lucide:paperclip'"
                  :size="12"
                  class="shrink-0 text-ink-tertiary"
                />
                <span class="min-w-0 truncate">{{ file.name }}</span>
                <span v-if="file.size" class="shrink-0 text-10px text-ink-tertiary"
                  >{{ Math.ceil(file.size / 1024) }} KB</span
                >
                <button
                  type="button"
                  class="flex shrink-0 items-center justify-center text-ink-tertiary outline-none hover:text-ink-primary focus-visible:shadow-focus-ring"
                  :aria-label="`Remove ${file.name}`"
                  @click.stop="removeAttachment(index)"
                >
                  <OnIcon name="lucide:x" :size="11" />
                </button>
              </span>
            </div>
            <textarea
              id="hermes-composer"
              ref="composer"
              v-model="input"
              rows="1"
              class="block max-h-110px min-h-42px w-full resize-none border-none bg-transparent px-4px pb-8px pt-4px font-sans text-13.5px leading-[1.55] text-ink-primary outline-none placeholder:text-ink-tertiary"
              :placeholder="content.composer.placeholder"
              :aria-label="content.composer.placeholder"
              @keydown.enter="onEnter"
            ></textarea>
            <div class="flex items-center gap-8px">
              <button
                type="button"
                class="flex h-26px w-26px items-center justify-center rounded-small text-ink-tertiary outline-none hover:bg-surface-raised hover:text-ink-primary focus-visible:shadow-focus-ring"
                :aria-label="content.composer.attach"
                @click="pickFiles"
              >
                <OnIcon name="lucide:paperclip" :size="15" />
              </button>
              <input ref="fileInput" type="file" multiple class="hidden" @change="onFileInput" />
              <button
                type="button"
                class="inline-flex h-24px max-w-220px items-center gap-5px rounded-tiny bg-surface-raised px-8px text-11px text-ink-secondary outline-none hover:bg-surface-control focus-visible:shadow-focus-ring"
                :title="store.workingDir || content.composer.directoryHint"
                @click="pickDirectory"
              >
                <OnIcon name="lucide:folder" :size="12" />
                <span class="min-w-0 truncate">{{ directoryLabel }}</span>
                <OnIcon name="lucide:chevron-down" :size="11" class="shrink-0 text-ink-tertiary" />
              </button>
              <div class="ml-auto flex items-center gap-8px">
                <div
                  class="relative flex items-center"
                  @mouseenter="contextOpen = true"
                  @mouseleave="contextOpen = false"
                >
                  <button
                    type="button"
                    class="relative flex h-28px w-28px items-center justify-center rounded-full text-ink-secondary outline-none transition-colors hover:bg-surface-raised hover:text-ink-primary focus-visible:shadow-focus-ring"
                    :aria-label="contextKnown ? `${content.context.title} ${contextPercent}%` : content.context.title"
                    :aria-expanded="contextOpen"
                  >
                    <svg viewBox="0 0 20 20" class="h-18px w-18px -rotate-90 overflow-visible" aria-hidden="true">
                      <circle
                        cx="10"
                        cy="10"
                        :r="contextRadius"
                        fill="none"
                        stroke="currentColor"
                        :stroke-width="1.8"
                        :stroke-dasharray="contextKnown ? undefined : '2.4 2.8'"
                        stroke-linecap="round"
                        :opacity="contextKnown ? 0.5 : 0.9"
                      />
                      <circle
                        v-if="contextKnown"
                        cx="10"
                        cy="10"
                        :r="contextRadius"
                        fill="none"
                        :stroke="contextColor"
                        stroke-width="2"
                        :stroke-dasharray="contextCircumference"
                        :stroke-dashoffset="contextDashoffset"
                        stroke-linecap="round"
                        class="transition-[stroke-dashoffset] duration-300 ease-out"
                      />
                    </svg>
                  </button>
                  <div
                    v-if="contextOpen"
                    class="absolute bottom-30px right-0 z-30 w-220px rounded-big border border-solid border-line-default bg-component-modal-bg p-13px shadow-modal"
                  >
                    <b class="block text-12px font-600 text-ink-primary">{{ content.context.title }}</b>
                    <p class="m-0 mt-5px text-12px text-ink-secondary">
                      {{ store.contextUsage ? contextUsedLabel : content.context.unknown }}
                    </p>
                    <p v-if="store.contextUsage" class="m-0 mt-2px font-mono text-11px text-ink-tertiary">
                      {{ contextTokensLabel }}
                    </p>
                  </div>
                </div>
                <button
                  type="button"
                  class="font-sans text-11px text-ink-tertiary outline-none hover:text-ink-secondary focus-visible:shadow-focus-ring"
                  @click="ui.openSettings('model')"
                >
                  {{ modelLabel }}
                </button>
                <OnButton
                  v-if="store.thinking"
                  variant="ghost"
                  size="sm"
                  icon="lucide:square"
                  icon-only
                  :aria-label="store.stopping ? content.composer.stopping : content.composer.stop"
                  :disabled="store.stopping"
                  @click="store.stop"
                />
                <OnButton
                  type="submit"
                  variant="accent"
                  size="sm"
                  icon="lucide:arrow-up"
                  icon-only
                  :aria-label="content.composer.send"
                  :disabled="store.stopping || (!input.trim() && !attachments.length)"
                />
              </div>
            </div>
          </form>
          <div class="mt-7px flex items-center justify-between gap-12px text-11px text-ink-tertiary">
            <span>{{ content.composer.hint }}</span>
            <span>↵ {{ content.composer.send }}</span>
          </div>
        </div>
      </div>
    </template>

    <AssistantHistory :open="historyOpen" @close="historyOpen = false" @select="selectConversation" />
    <HermesSettings :open="ui.settingsOpen" :tab="ui.settingsTab" @close="ui.closeSettings()" />

    <OnModal
      :open="Boolean(store.approvalRequest)"
      :title="content.approval.title"
      :description="content.approval.body"
      size="md"
      role="dialog"
      @close="store.respondApproval('deny')"
    >
      <div v-if="store.approvalRequest" class="flex flex-col gap-8px">
        <span class="text-11px text-ink-tertiary">{{ content.approval.command }}</span>
        <pre
          class="m-0 max-h-220px overflow-auto whitespace-pre-wrap break-words rounded-small border border-solid border-line-subtle bg-surface-inset px-10px py-8px font-mono text-11px leading-[1.5] text-ink-secondary"
          >{{ store.approvalRequest.command || store.approvalRequest.description || '—' }}</pre
        >
      </div>
      <template #footer>
        <OnButton variant="ghost" @click="store.respondApproval('deny')">{{ content.approval.deny }}</OnButton>
        <OnButton variant="accent" @click="store.respondApproval('once')">{{ content.approval.allowOnce }}</OnButton>
      </template>
    </OnModal>

    <OnModal
      :open="installOpen"
      :title="content.install.title"
      :description="content.install.body"
      size="md"
      role="dialog"
      @close="closeInstall"
    >
      <div class="flex flex-col gap-12px">
        <div
          class="flex items-center gap-8px rounded-small border border-solid border-line-subtle bg-surface-inset px-12px py-10px text-11.5px text-ink-secondary"
        >
          <OnIcon name="lucide:hard-drive" :size="14" />
          {{ content.install.size }}
        </div>
        <div v-if="store.installing" class="flex flex-col gap-7px">
          <div class="h-6px overflow-hidden rounded-full bg-surface-inset">
            <div
              class="h-full rounded-full bg-brand-coral transition-[width] duration-300"
              :style="{ width: (store.installProgress?.percent ?? 0) + '%' }"
            ></div>
          </div>
          <span class="text-11px text-ink-tertiary"
            >{{ content.install.installing }} · {{ store.installProgress?.percent ?? 0 }}%</span
          >
        </div>
        <p v-else-if="store.runtime?.message" class="m-0 break-words text-11px leading-[1.5] text-ink-tertiary">
          {{ store.runtime.message }}
        </p>
      </div>
      <template #footer>
        <OnButton variant="ghost" :disabled="store.installing" @click="closeInstall">{{
          content.install.later
        }}</OnButton>
        <OnButton v-if="store.installing" variant="ghost" @click="store.cancelInstall">{{
          content.install.cancelInstall
        }}</OnButton>
        <OnButton v-else variant="accent" icon="lucide:download" @click="installRuntime">
          {{ content.install.install }}
        </OnButton>
      </template>
    </OnModal>
  </div>
</template>
