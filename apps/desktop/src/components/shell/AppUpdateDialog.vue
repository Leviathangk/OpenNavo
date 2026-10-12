<script setup lang="ts">
import { computed, defineAsyncComponent, onBeforeUnmount, onMounted, ref, useId, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { unwrap } from '@opennavo/api';
import { api } from '@/api';
import { formatPercent } from '@opennavo/shared';
import { OnButton, OnDialogShell, OnLogo, useUiMessages } from '@opennavo/ui';
import { useAppLocale } from '@/composables/useAppLocale';
import { useAppUpdateChecks } from '@/composables/useAppUpdateChecks';
import { useToasts } from '@/composables/useToasts';
import { useAppUpdaterStore } from '@/stores/appUpdater';
import { openExternal } from '@/utils/external';

const OnMarkdown = defineAsyncComponent(() => import('@opennavo/ui/markdown').then(module => module.OnMarkdown));
const updater = useAppUpdaterStore();
const { t } = useI18n();
const { appLocale } = useAppLocale();
const messages = useUiMessages();
const toasts = useToasts();
const titleId = useId();
const descriptionId = useId();
const blocked = ref(false);
const visible = ref(document.visibilityState !== 'hidden');
const open = computed(() => updater.dialogOpen && (updater.preview || visible.value) && !blocked.value);
const localizedNotes = ref<string | null>(null);
const notes = computed(() => localizedNotes.value ?? updater.update?.body);
watch(
  [() => updater.update?.version, appLocale, open],
  async ([version, locale, isOpen], _previous, onCleanup) => {
    localizedNotes.value = null;
    if (!version || !isOpen || updater.preview) return;
    let stale = false;
    onCleanup(() => { stale = true; });
    try {
      const release = await unwrap(api.GET('/desktop/releases/latest', {
        params: { query: { channel: version.includes('-') ? 'beta' : 'stable' } },
        headers: { 'Accept-Language': locale }
      }));
      if (!stale && release.version === version && release.notes?.trim()) localizedNotes.value = release.notes;
    } catch {
      // Keep the updater manifest's notes available when the content API is offline.
    }
  },
  { immediate: true }
);
const downloading = computed(() => updater.state === 'downloading');
const installed = computed(() => updater.state === 'installed');
const progressText = computed(() =>
  updater.percent === null
    ? t('updater.downloadingUnknown')
    : t('updater.downloading', {
        percent: formatPercent(updater.percent / 100, { locale: appLocale.value })
      })
);
let observer: MutationObserver | undefined;

useAppUpdateChecks();

function observeOverlays() {
  blocked.value = Boolean(document.querySelector('[role="dialog"]:not(.on-app-update-dialog), [role="alertdialog"]'));
  visible.value = document.visibilityState !== 'hidden';
}

onMounted(() => {
  observeOverlays();
  observer = new MutationObserver(observeOverlays);
  observer.observe(document.body, { childList: true, subtree: true });
  document.addEventListener('visibilitychange', observeOverlays);
});
onBeforeUnmount(() => {
  observer?.disconnect();
  document.removeEventListener('visibilitychange', observeOverlays);
});

async function restart() {
  try {
    await updater.restart();
  } catch (error) {
    toasts.push({
      tone: 'warning',
      title: String(error).includes('active_tasks') ? t('updater.busy') : t('errors.actionFailed')
    });
  }
}

async function releaseNotes() {
  const version = updater.update?.version;
  if (!version) return;
  try {
    await openExternal(`https://github.com/opennavo/OpenNavo/releases/tag/desktop-v${encodeURIComponent(version)}`);
  } catch {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed') });
  }
}

function install() {
  void updater.install();
}
</script>

<template>
  <OnDialogShell
    :open="open"
    :dismissible="!downloading"
    :labelledby="titleId"
    :describedby="descriptionId"
    initial-focus="[data-update-later]"
    panel-class="on-app-update-dialog box-border flex max-h-[calc(100vh-48px)] w-full max-w-560px flex-col overflow-hidden rounded-huge border border-solid border-line-default bg-component-modal-bg text-ink-primary shadow-modal"
    @close="updater.dismiss"
  >
    <header class="flex shrink-0 items-start gap-18px px-30px pt-30px">
      <span class="grid h-60px w-60px shrink-0 place-items-center rounded-big bg-surface-raised" aria-hidden="true">
        <OnLogo :size="36" />
      </span>
      <div class="min-w-0 flex-1 pt-2px">
        <h2 :id="titleId" class="m-0 text-24px font-600 tracking-[-0.02em]">
          {{ installed ? t('updater.readyTitle') : t('updater.foundTitle') }}
        </h2>
        <p :id="descriptionId" class="m-0 mt-6px text-14px text-ink-secondary">
          {{ installed ? t('updater.installed') : t('updater.foundDescription', { version: updater.update?.version }) }}
        </p>
      </div>
      <OnButton
        variant="ghost"
        size="sm"
        icon="x"
        icon-only
        :disabled="downloading"
        :aria-label="messages.dialog.close"
        @click="updater.dismiss"
      />
    </header>

    <div class="min-h-0 flex-1 overflow-y-auto px-30px">
      <p class="mb-20px mt-22px text-12px text-ink-secondary">
        {{
          t('updater.versionChange', {
            current: updater.update?.currentVersion,
            next: updater.update?.version
          })
        }}
      </p>
      <p v-if="updater.preview" class="mb-16px text-12px text-brand-salmon">
        {{ t('updater.previewHint') }}
      </p>
      <section class="border-t border-t-solid border-line-subtle pt-20px">
        <h3 class="m-0 mb-12px text-15px font-600">{{ t('updater.notesTitle') }}</h3>
        <OnMarkdown
          v-if="notes"
          :source="notes"
          size="md"
          :heading-level="4"
          class="on-app-update-notes"
        />
        <p v-else class="m-0 text-14px leading-[1.7] text-ink-secondary">
          {{ t('updater.noNotes') }}
        </p>
        <OnButton
          v-if="!updater.preview"
          variant="ghost"
          size="sm"
          icon="arrow-up-right"
          class="-ml-8px mt-14px !text-brand-salmon"
          @click="releaseNotes"
        >
          {{ t('updater.fullNotes') }}
        </OnButton>
      </section>
      <div v-if="downloading" class="mt-20px" role="status" aria-live="polite">
        <p class="m-0 mb-8px text-13px text-ink-secondary">{{ progressText }}</p>
        <progress
          class="on-app-update-progress block h-5px w-full overflow-hidden rounded-full"
          :value="updater.percent ?? undefined"
          max="100"
          :aria-label="progressText"
        />
      </div>
      <p v-if="updater.installFailed" role="alert" class="mb-0 mt-16px text-13px text-status-danger">
        {{ t('updater.installFailed') }}
      </p>
    </div>

    <footer class="mx-30px mb-26px mt-24px shrink-0 border-t border-t-solid border-line-subtle pt-18px">
      <p class="m-0 mb-16px text-12px leading-[1.6] text-ink-secondary">
        {{ updater.busy && installed ? t('updater.busy') : t('updater.restartHint') }}
      </p>
      <div class="flex flex-wrap justify-end gap-10px">
        <OnButton
          v-if="updater.preview && !installed"
          variant="secondary"
          :disabled="downloading"
          @click="updater.install(true)"
          >{{ t('updater.previewFailure') }}</OnButton
        >
        <OnButton data-update-later variant="secondary" :disabled="downloading" @click="updater.dismiss">{{
          t('updater.later')
        }}</OnButton>
        <OnButton v-if="installed" variant="primary" :disabled="updater.busy" @click="restart">{{
          t('updater.restart')
        }}</OnButton>
        <OnButton
          v-else
          variant="primary"
          icon="download"
          :loading="downloading"
          :disabled="updater.working"
          @click="install"
        >
          {{ downloading ? progressText : t('updater.install') }}
        </OnButton>
      </div>
    </footer>
  </OnDialogShell>
</template>

<style scoped>
.on-app-update-notes {
  overflow-wrap: anywhere;
  font-size: 14px;
  line-height: 1.75;
  color: var(--on-text-secondary);
}

.on-app-update-progress {
  accent-color: var(--on-brand-coral);
  background: var(--on-surface-raised);
}

.on-app-update-progress::-webkit-progress-bar {
  background: var(--on-surface-raised);
}

.on-app-update-progress::-webkit-progress-value {
  background: var(--on-brand-coral);
}
</style>
