<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { isTauri } from '@tauri-apps/api/core';
import { OnButton, OnIcon, OnLogViewer, OnModal, OnToastRegion } from '@opennavo/ui';
import WindowControls from '@/components/shell/WindowControls.vue';
import QuitConfirm from '@/components/shell/QuitConfirm.vue';
import RunningTask from '@/components/updates/RunningTask.vue';
import SourceCards from '@/components/welcome/SourceCards.vue';
import type { Locale, Task } from '@/ipc/bindings';
import { IpcError, commands, unwrap } from '@/ipc/client';
import { sameMirrorChoice, toMirrorChoice, useMirrorOptions } from '@/composables/useMirrorOptions';
import type { MirrorOption } from '@/composables/useMirrorOptions';
import { resumeDeepLink } from '@/composables/useDeepLink';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { useEnvStore, useSettingsStore, useTasksStore } from '@/stores';
// Use the native app icon file (src-tauri/icons) so Welcome reflects icon changes.
import appIcon from '../../src-tauri/icons/128x128@2x.png';

// First launch (06 §13.1, 08 §10.13, owner revisions 2026-10-07, 2026-10-12 and 2026-10-28): environment check → source
// (official / mirror cards, lowest latency selected) → install Homebrew or start using the app. Only Simplified Chinese
// Macs open here on first launch (Rust settings::load); elsewhere Install opens it when Homebrew is missing. Permissions
// are asked for where they are needed (permission gate), not here.
// Save the download source only when Install Homebrew or Get Started is clicked.
const { t, te } = useI18n();
const router = useRouter();
const env = useEnvStore();
const settings = useSettingsStore();
const tasks = useTasksStore();
const toasts = useToasts();
const inBrowser = !isTauri();
const { errorText } = usePackageState();
// Use the computer's language independently of the user's manual UI language choice.
const systemLocale = ref<Locale>();
const showMirrors = computed(() => systemLocale.value === 'zh-CN');
const {
  options,
  failed: configFailed,
  probes,
  probing,
  find,
  load,
  probe,
  unreachable
} = useMirrorOptions(
  () => showMirrors.value,
  () => settings.value?.customMirrors ?? []
);

onMounted(async () => {
  systemLocale.value = await unwrap(commands.systemLocaleGet()).catch(() => 'en-US' as const);
  await load();
  await probe();
});

async function reloadMirrors() {
  await load();
  await probe();
}

// Environment check: reveal three rows at 200 ms intervals (08 §10.13); show all immediately with Reduce Motion.
const STAGGER_MS = 200;
const shown = ref(0);
let timers: ReturnType<typeof setTimeout>[] = [];

function reveal() {
  timers.forEach(clearTimeout);
  timers = [];
  const reduced =
    typeof window.matchMedia === 'function' && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
  if (reduced) {
    shown.value = 3;
    return;
  }
  shown.value = 0;
  for (let index = 0; index < 3; index += 1)
    timers.push(setTimeout(() => (shown.value = index + 1), index * STAGGER_MS));
}

watch(
  () => Boolean(env.info),
  ready => {
    if (ready) reveal();
  },
  { immediate: true }
);
onBeforeUnmount(() => timers.forEach(clearTimeout));

type CheckState = 'ok' | 'info' | 'missing';
const CHECK_STYLE: Record<CheckState, { icon: string; tone: string }> = {
  ok: { icon: 'check', tone: 'bg-status-success-subtle text-status-success' },
  // Missing command-line tools do not block installation (Homebrew installs them too); use a neutral icon.
  info: { icon: 'minus', tone: 'bg-surface-chip text-ink-tertiary' },
  missing: { icon: 'x', tone: 'bg-status-danger-subtle text-status-danger' }
};

const arch = computed(() => (env.info?.arch === 'x86_64' ? 'x86_64' : 'arm64'));
// Default Homebrew prefixes: /opt/homebrew on Apple silicon, /usr/local on Intel (06 §6.1).
const defaultPrefix = computed(() => (arch.value === 'x86_64' ? '/usr/local' : '/opt/homebrew'));

const checks = computed(() => {
  const info = env.info;
  if (!info) return [];
  return [
    {
      key: 'macos',
      state: 'ok' as CheckState,
      label: t('welcome.macos', {
        version: info.macosVersion,
        arch: t(`welcome.arch.${arch.value}`)
      }),
      status: t('welcome.macosOk')
    },
    {
      key: 'clt',
      state: (info.cltInstalled ? 'ok' : 'info') as CheckState,
      label: t('welcome.clt'),
      status: info.cltInstalled ? t('welcome.cltOk') : t('welcome.cltMissing')
    },
    {
      key: 'brew',
      state: (info.brew ? 'ok' : 'missing') as CheckState,
      label: t('welcome.homebrew'),
      status: info.brew
        ? t('welcome.homebrewOk', { version: info.brew.version, prefix: info.brew.prefix })
        : t('welcome.homebrewMissing', { prefix: defaultPrefix.value })
    }
  ];
});

async function redetect() {
  await env.detect().catch(() => undefined);
}

// Sources: official left, fastest available mirror right; select and recommend only the lower-latency option.
function latency(key: string | undefined): number | undefined {
  const result = key ? probes.value[key] : undefined;
  return result?.ok && result.latencyMs !== null ? result.latencyMs : undefined;
}

const official = computed(() => find('official') as MirrorOption);
const mirror = computed<MirrorOption | undefined>(() => {
  const mirrors = options.value.filter(option => option.key !== 'official');
  let best: MirrorOption | undefined;
  for (const option of mirrors) {
    const ms = latency(option.key);
    if (ms !== undefined && (best === undefined || ms < (latency(best.key) ?? Infinity))) best = option;
  }
  // Before probes finish or if all fail, display the first backend-ordered mirror.
  return best ?? mirrors[0];
});
const recommended = computed(() => {
  const officialMs = latency('official');
  const mirrorMs = latency(mirror.value?.key);
  if (officialMs === undefined && mirrorMs === undefined) return undefined;
  if (mirrorMs !== undefined && (officialMs === undefined || mirrorMs < officialMs)) return mirror.value?.key;
  return 'official';
});

// Track the user's selected card; without a selection, choose the lowest latency after probing, or official if all fail.
const pickedCard = ref<'official' | 'mirror'>();
const selectedKey = computed(() => {
  if (!systemLocale.value) return undefined;
  if (!showMirrors.value) return 'official';
  if (pickedCard.value === 'official') return 'official';
  if (pickedCard.value === 'mirror' && mirror.value) return mirror.value.key;
  if (recommended.value) return recommended.value;
  return probing.value ? undefined : 'official';
});

function select(option: MirrorOption) {
  pickedCard.value = option.key === 'official' ? 'official' : 'mirror';
}

/** Write the selected source unchanged to settings; skip the write if it already matches. */
async function saveSelection() {
  const option = find(selectedKey.value);
  const current = settings.value;
  if (!option || !current) throw new Error('settings_unavailable');
  const next = toMirrorChoice(option);
  if (!sameMirrorChoice(current.mirror, next)) await settings.update('mirror', next);
}

// Install Homebrew.
const installTask = computed(() => tasks.active.find(task => task.op === 'install_homebrew'));
const lastInstall = computed(() => tasks.finished.find(task => task.op === 'install_homebrew'));
// Latest install failure this session; hide it when a new install starts.
const failure = computed(() =>
  !installTask.value && lastInstall.value?.state === 'failed' ? lastInstall.value : undefined
);
const saving = ref(false);
const locked = computed(() => saving.value || Boolean(installTask.value));

const DOWNLOAD_ERRORS: ReadonlySet<string> = new Set(['E_DOWNLOAD', 'E_TIMEOUT', 'E_NETWORK']);
const failureText = computed(() => {
  const error = failure.value?.error;
  if (!error) return t('errors.E_UNKNOWN');
  const key = `welcome.failed.${error.code}`;
  return te(key) ? t(key) : errorText(new IpcError(error));
});
// If the official download fails and a mirror is reachable, offer switching in the failure panel.
const failureMirror = computed(() =>
  failure.value &&
  DOWNLOAD_ERRORS.has(failure.value.error?.code ?? '') &&
  selectedKey.value === 'official' &&
  latency(mirror.value?.key) !== undefined
    ? mirror.value
    : undefined
);

async function install() {
  if (locked.value || !selectedKey.value) return;
  saving.value = true;
  try {
    // Enqueue only after a successful write: installation selects its script and mirror variables from settings (06 §6.2).
    await saveSelection();
    tasks.applyUpdate(await tasks.enqueue('install_homebrew', null));
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  } finally {
    saving.value = false;
  }
}

/** Enter only when the environment is ready and settings save succeeds; remain on Welcome to retry failed saves. */
async function leave() {
  if (locked.value || !env.hasBrew || !selectedKey.value) return;
  saving.value = true;
  try {
    await saveSelection();
    await settings.update('onboardingCompleted', true);
    if (!(await resumeDeepLink(router))) await router.replace('/discover');
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  } finally {
    saving.value = false;
  }
}

// Installation logs.
const logOpen = ref(false);
const logLines = ref<string[]>([]);

async function viewLog(task: Task) {
  const text = await unwrap(commands.taskLog(task.id, 2000)).catch(() => '');
  logLines.value = text ? text.split('\n') : (tasks.logs[task.id] ?? []);
  logOpen.value = true;
}
</script>

<template>
  <div class="relative flex h-dvh flex-col overflow-hidden bg-surface-card">
    <div class="h-52px shrink-0" data-tauri-drag-region></div>
    <WindowControls v-if="inBrowser" />
    <main class="min-h-0 flex-1 overflow-y-auto px-32px pb-28px" :aria-label="t('welcome.title')">
      <div class="mx-auto flex min-h-full w-full max-w-540px flex-col">
        <header class="flex items-center gap-14px" data-tauri-drag-region>
          <img :src="appIcon" alt="" class="-m-4px h-64px w-64px shrink-0 select-none" draggable="false" />
          <div class="min-w-0">
            <h1 class="m-0 text-24px font-600 leading-[30px] tracking-[-0.02em] text-ink-primary">
              {{ t('welcome.title') }}
            </h1>
            <p class="m-0 mt-4px text-14px text-ink-secondary">{{ t('welcome.subtitle') }}</p>
          </div>
        </header>

        <section class="mt-24px" :aria-label="t('welcome.checks')">
          <ul v-if="checks.length" class="m-0 list-none p-0">
            <li
              v-for="(check, index) in checks"
              :key="check.key"
              class="flex items-center gap-12px border-t border-t-solid border-line-subtle py-11px transition-opacity duration-medium ease-standard first:border-t-0 first:pt-0"
              :class="index < shown ? 'opacity-100' : 'opacity-0'"
            >
              <span
                class="grid h-22px w-22px shrink-0 place-items-center rounded-full"
                :class="CHECK_STYLE[check.state].tone"
              >
                <OnIcon :name="CHECK_STYLE[check.state].icon" :size="13" />
              </span>
              <span class="min-w-0 flex-1 text-14px text-ink-primary">{{ check.label }}</span>
              <span class="max-w-[55%] truncate text-right text-13px text-ink-tertiary">{{ check.status }}</span>
            </li>
          </ul>
          <div v-else class="flex items-center justify-between gap-12px text-14px" role="status">
            <span class="text-ink-secondary">{{
              env.detecting || !env.info ? (env.detecting ? t('welcome.checking') : t('welcome.detectFailed')) : ''
            }}</span>
            <OnButton v-if="!env.detecting" variant="secondary" size="sm" icon="refresh-cw" @click="redetect">{{
              t('welcome.redetect')
            }}</OnButton>
          </div>
        </section>

        <section class="mt-24px flex flex-col gap-10px" aria-labelledby="welcome-source">
          <div class="flex items-center justify-between gap-12px">
            <h2 id="welcome-source" class="m-0 text-14px font-600 text-ink-primary">
              {{ t('welcome.source.title') }}
            </h2>
            <OnButton
              variant="ghost"
              size="xs"
              icon="refresh-cw"
              :loading="probing"
              :disabled="locked"
              @click="probe"
              >{{ probing ? t('mirrors.probing') : t('mirrors.probe') }}</OnButton
            >
          </div>
          <SourceCards
            :official="official"
            :mirror="mirror"
            :show-mirror="showMirrors"
            :probes="probes"
            :probing="probing"
            :selected="selectedKey"
            :recommended="recommended"
            :config-failed="showMirrors && configFailed"
            :disabled="locked"
            @select="select"
            @retry="reloadMirrors"
          />
          <p
            v-if="selectedKey && !failure && unreachable(selectedKey)"
            class="m-0 flex items-center gap-6px text-12.5px text-status-warning"
            role="status"
          >
            <OnIcon name="triangle-alert" :size="13" />{{ t('mirrors.selectedFailed') }}
          </p>
          <p class="m-0 text-12.5px leading-[1.55] text-ink-tertiary">
            {{ env.hasBrew ? t('welcome.source.hintBrew') : t('welcome.source.hint') }}
          </p>
        </section>

        <template v-if="env.hasBrew">
          <footer class="mt-auto flex flex-col gap-8px pt-24px">
            <OnButton variant="accent" size="lg" block :loading="saving" :disabled="!selectedKey" @click="leave()">{{
              t('welcome.done')
            }}</OnButton>
          </footer>
        </template>

        <footer v-else-if="env.info" class="mt-auto flex flex-col gap-10px pt-24px">
          <RunningTask v-if="installTask?.state === 'running'" :task="installTask" />
          <template v-else>
            <div v-if="failure" class="rounded-default bg-status-danger-subtle px-14px py-12px" role="alert">
              <p class="m-0 text-14px font-600 text-status-danger">
                {{ t('welcome.failed.title') }}
              </p>
              <p class="m-0 mt-4px text-13px text-ink-secondary">{{ failureText }}</p>
              <div class="mt-10px flex flex-wrap gap-8px">
                <OnButton variant="ghost" size="sm" icon="file-text" @click="viewLog(failure)">{{
                  t('welcome.failed.viewLog')
                }}</OnButton>
                <OnButton v-if="failureMirror" variant="secondary" size="sm" @click="pickedCard = 'mirror'">{{
                  t('mirrors.useSuggested', { name: failureMirror.name })
                }}</OnButton>
              </div>
            </div>
            <OnButton variant="accent" size="lg" block :loading="locked" :disabled="!selectedKey" @click="install">{{
              t('welcome.install')
            }}</OnButton>
            <p class="m-0 text-center text-12.5px leading-[1.55] text-ink-tertiary">
              {{ t('welcome.passwordHint') }}
            </p>
          </template>
        </footer>
      </div>
    </main>

    <QuitConfirm />
    <OnToastRegion :items="toasts.items.value" @dismiss="toasts.dismiss" />

    <OnModal v-model:open="logOpen" size="lg" :title="t('welcome.failed.logTitle')">
      <OnLogViewer v-if="logLines.length" :lines="logLines" :height="360" :label="t('welcome.failed.logTitle')" />
      <p v-else class="m-0 text-13px text-ink-tertiary">{{ t('updates.log.empty') }}</p>
    </OnModal>
  </div>
</template>
