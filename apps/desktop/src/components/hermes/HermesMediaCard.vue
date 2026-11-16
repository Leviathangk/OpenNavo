<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import { OnButton, OnIcon, OnModal } from '@opennavo/ui';
import { convertFileSrc, isTauri } from '@tauri-apps/api/core';
import { useHermesContent } from '@/composables/useHermesContent';
import { commands, unwrap } from '@/ipc/client';
import { isExternalMediaSrc, mediaName, type HermesMediaKind } from '@/hermes/media';

interface ResolvedMediaSource {
  path: string;
  dataUrl?: string | null;
  streamUrl?: string | null;
  size?: number | null;
  remote: boolean;
}

const props = withDefaults(
  defineProps<{
    path: string;
    kind: HermesMediaKind;
    remote?: boolean;
    profileId?: string | null;
    profile?: string | null;
    sessionId?: string | null;
    cwd?: string | null;
    compact?: boolean;
  }>(),
  { remote: false, compact: false }
);

const content = useHermesContent();
const resolved = ref<ResolvedMediaSource | null>(null);
const loading = ref(false);
const imageFailed = ref(false);
const mediaFailed = ref(false);
const openError = ref(false);
const previewOpen = ref(false);
const retries = ref(0);
let request = 0;

onBeforeUnmount(() => {
  request += 1;
});

const name = computed(() => mediaName(props.path));
const external = computed(() => isExternalMediaSrc(props.path));
const openLabel = computed(() =>
  props.remote || external.value ? content.value.media.openInBrowser : content.value.media.open
);
const sizeText = computed(() => {
  const size = resolved.value?.size;
  if (!size) return '';
  if (size >= 1024 * 1024) return `${(size / (1024 * 1024)).toFixed(size >= 10 * 1024 * 1024 ? 0 : 1)} MB`;
  if (size >= 1024) return `${Math.round(size / 1024)} KB`;
  return `${size} B`;
});
const icon = computed(() => {
  if (props.kind === 'image') return 'lucide:image';
  if (props.kind === 'video') return 'lucide:film';
  if (props.kind === 'audio') return 'lucide:music';
  const ext = (name.value.split('.').pop() ?? '').toLowerCase();
  if (['pdf', 'doc', 'docx', 'odt', 'rtf', 'txt', 'md', 'epub'].includes(ext)) return 'lucide:file-text';
  if (['xls', 'xlsx', 'ods', 'csv', 'tsv'].includes(ext)) return 'lucide:file-spreadsheet';
  if (['ppt', 'pptx', 'odp', 'key'].includes(ext)) return 'lucide:presentation';
  if (['zip', 'tar', 'gz', 'tgz', 'bz2', 'xz', '7z', 'rar'].includes(ext)) return 'lucide:file-archive';
  if (['json', 'xml', 'yaml', 'yml', 'html', 'htm'].includes(ext)) return 'lucide:file-code';
  return 'lucide:file';
});

const src = computed(() => {
  if (external.value) return '';
  if (!resolved.value) return '';
  if (props.kind === 'image') {
    if (resolved.value.dataUrl) return resolved.value.dataUrl;
    return isTauri() ? convertFileSrc(resolved.value.path) : '';
  }
  if (props.kind === 'video' || props.kind === 'audio') {
    if (resolved.value.streamUrl) return resolved.value.streamUrl;
    return isTauri() ? convertFileSrc(resolved.value.path) : '';
  }
  return '';
});

async function load() {
  const current = ++request;
  resolved.value = null;
  loading.value = false;
  imageFailed.value = false;
  mediaFailed.value = false;
  openError.value = false;
  // A model-authored URL is only opened after a click; the webview never fetches it itself.
  if (external.value || !isTauri()) return;
  loading.value = true;
  try {
    const source = await unwrap(
      commands.hermesMediaResolve({
        path: props.path,
        kind: props.kind,
        profileId: props.profileId ?? null,
        profile: props.profile ?? null,
        sessionId: props.sessionId ?? null,
        cwd: props.cwd ?? null
      })
    );
    if (current !== request) return;
    resolved.value = source;
  } catch {
    // `resolved` stays empty, so the card shows its failure state.
  } finally {
    if (current === request) loading.value = false;
  }
}

async function openFile() {
  openError.value = false;
  try {
    const { openPath, openUrl } = await import('@tauri-apps/plugin-opener');
    if (external.value) {
      await openUrl(props.path);
      return;
    }
    if (props.remote) {
      const url = await unwrap(
        commands.hermesMediaOpenUrl({
          path: props.path,
          kind: props.kind,
          profileId: props.profileId ?? null,
          profile: props.profile ?? null,
          sessionId: props.sessionId ?? null,
          cwd: null
        })
      );
      if (!url) throw new Error('No Hermes media URL.');
      // A webapp panel returns an http(s) ticket URL. A dashboard cannot mint that ticket, so the
      // file is already on disk and this string is a local path.
      if (/^https?:\/\//i.test(url)) await openUrl(url);
      else await openPath(url);
      return;
    }
    // Do not fall back to the unvalidated tag when resolve failed (for example because the path
    // names a credential store): that would bypass the backend's local media denylist.
    const path = resolved.value?.path;
    if (!path) throw new Error('Unresolved Hermes media path.');
    await openPath(path);
  } catch {
    openError.value = true;
  }
}

function onMediaError() {
  if (props.remote && retries.value < 1) {
    retries.value += 1;
    void load();
    return;
  }
  mediaFailed.value = true;
}

function retry() {
  retries.value = 0;
  void load();
}

watch(
  () => [props.path, props.kind, props.remote, props.profileId, props.profile, props.sessionId, props.cwd],
  () => {
    retries.value = 0;
    void load();
  },
  { immediate: true }
);
</script>

<template>
  <div class="min-w-0" :class="compact ? '' : 'my-1px'">
    <figure v-if="kind === 'image' && !external" class="m-0">
      <div v-if="loading" class="h-24 w-40 animate-pulse rounded-default bg-surface-inset" aria-hidden="true"></div>
      <button
        v-else-if="src && !imageFailed"
        type="button"
        class="group block max-w-640px overflow-hidden rounded-default border border-solid border-line-subtle bg-surface-inset p-0 outline-none transition-colors hover:border-line-strong focus-visible:shadow-focus-ring"
        :aria-label="content.media.preview"
        @click="previewOpen = true"
      >
        <img
          :src="src"
          :alt="content.media.imageAlt"
          class="block max-h-420px max-w-full object-contain"
          @error="imageFailed = true"
        />
      </button>
      <div
        v-else
        class="flex max-w-460px items-start gap-8px rounded-default border border-dashed border-line-default bg-surface-card px-12px py-10px text-11.5px text-ink-tertiary"
      >
        <OnIcon name="lucide:image-off" :size="14" class="mt-1px shrink-0 text-status-warning" />
        <div class="min-w-0 flex-1">
          <span class="block break-words font-500 text-ink-secondary">{{ name }}</span>
          <span class="mt-2px block">{{ content.media.failed }}</span>
          <span class="mt-1px block text-10.5px">{{ content.media.failedHint }}</span>
          <div class="mt-7px flex flex-wrap gap-6px">
            <OnButton size="xs" variant="ghost" :loading="loading" @click="retry">{{ content.media.retry }}</OnButton>
            <OnButton size="xs" variant="ghost" @click="openFile">{{ openLabel }}</OnButton>
          </div>
          <span v-if="openError" class="mt-4px block text-10.5px text-status-danger">{{
            content.media.openFailed
          }}</span>
        </div>
      </div>
    </figure>

    <div v-else-if="kind === 'video' && !external" class="flex max-w-640px flex-col gap-5px">
      <div v-if="loading" class="h-32 w-56 animate-pulse rounded-default bg-surface-inset" aria-hidden="true"></div>
      <div
        v-else-if="src && !mediaFailed"
        class="overflow-hidden rounded-default border border-solid border-line-subtle bg-black"
      >
        <video
          class="block max-h-360px w-full"
          controls
          playsinline
          preload="metadata"
          :src="src"
          @error="onMediaError"
        />
      </div>
      <div
        v-else
        class="flex max-w-460px items-start gap-8px rounded-default border border-dashed border-line-default bg-surface-card px-12px py-10px text-11.5px text-ink-tertiary"
      >
        <OnIcon name="lucide:film" :size="14" class="mt-1px shrink-0 text-status-warning" />
        <div class="min-w-0 flex-1">
          <span class="block break-words font-500 text-ink-secondary">{{ name }}</span>
          <span class="mt-2px block">{{ content.media.failed }}</span>
          <span class="mt-1px block text-10.5px">{{ content.media.failedHint }}</span>
          <div class="mt-7px flex flex-wrap gap-6px">
            <OnButton size="xs" variant="ghost" :loading="loading" @click="retry">{{ content.media.retry }}</OnButton>
            <OnButton size="xs" variant="ghost" @click="openFile">{{ openLabel }}</OnButton>
          </div>
          <span v-if="openError" class="mt-4px block text-10.5px text-status-danger">{{
            content.media.openFailed
          }}</span>
        </div>
      </div>
    </div>

    <div
      v-else-if="kind === 'audio' && !external"
      class="flex max-w-440px flex-col gap-6px rounded-default border border-solid border-line-subtle bg-surface-card px-12px py-10px"
    >
      <div class="flex min-w-0 items-center gap-7px text-11px text-ink-tertiary">
        <OnIcon name="lucide:music" :size="12" class="shrink-0" />
        <span class="min-w-0 truncate">{{ name }}</span>
        <span v-if="sizeText" class="ml-auto shrink-0 tabular-nums">{{ sizeText }}</span>
      </div>
      <div v-if="loading" class="h-8 w-full animate-pulse rounded bg-surface-inset" aria-hidden="true"></div>
      <audio
        v-else-if="src && !mediaFailed"
        class="block w-full"
        controls
        preload="metadata"
        :src="src"
        @error="onMediaError"
      />
      <div v-else class="flex flex-wrap items-center gap-6px text-11px text-ink-tertiary">
        <span>{{ content.media.failed }}</span>
        <OnButton size="xs" variant="ghost" :loading="loading" @click="retry">{{ content.media.retry }}</OnButton>
        <OnButton size="xs" variant="ghost" @click="openFile">{{ openLabel }}</OnButton>
      </div>
      <span v-if="openError" class="text-10.5px text-status-danger">{{ content.media.openFailed }}</span>
    </div>

    <div
      v-else
      class="flex max-w-480px items-center gap-10px rounded-default border border-solid border-line-subtle bg-surface-card px-12px py-10px"
    >
      <span
        class="flex h-30px w-30px shrink-0 items-center justify-center rounded-small bg-surface-inset text-ink-tertiary"
      >
        <OnIcon :name="icon" :size="16" />
      </span>
      <div class="min-w-0 flex-1">
        <b class="block truncate text-12px font-600 leading-[1.4] text-ink-primary">{{ name }}</b>
        <span class="mt-1px block truncate font-mono text-10px text-ink-tertiary" :title="path">{{ path }}</span>
      </div>
      <span v-if="sizeText" class="shrink-0 text-10.5px tabular-nums text-ink-tertiary">{{ sizeText }}</span>
      <OnButton size="xs" variant="ghost" @click="openFile">{{ openLabel }}</OnButton>
    </div>
  </div>

  <OnModal :open="previewOpen" :title="name" size="lg" @close="previewOpen = false">
    <img
      v-if="src"
      :src="src"
      :alt="content.media.imageAlt"
      class="mx-auto block max-h-[70vh] max-w-full object-contain"
    />
    <template #footer>
      <OnButton variant="ghost" @click="openFile">{{ openLabel }}</OnButton>
    </template>
  </OnModal>
</template>
